// In-memory online-models state for adapter-mfc, plus the methods that
// mutate and read it. The snapshot is updated by the websocket reader and
// served by the HTTP handlers.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// streamer is one online model's last known state, keyed by uid in the snapshot.
// Fields without a known value remain at zero. USERNAMELOOKUP dedupe state
// lives on nameEntry (keyed by uid in nameCache) so it survives bulk replaces.
type streamer struct {
	name    string
	uid     int
	vs      int
	rc      int
	topic   string
	camserv int
}

// snapshot is the in-memory online-models state, updated by the reader and
// read by the HTTP handlers. bulkApplied tracks whether the current session
// installed its initial bulk dump and gates /online's readiness signal, so
// we return 503 until a bulk lands. nameCache serves two purposes: it caches
// resolved names so delta SESSIONSTATEs can fill them in without paying for
// a USERNAMELOOKUP, and it tracks in-flight USERNAMELOOKUP claims (via
// nameEntry.lookupAt) so a fresh bulk during warmup doesn't re-fire pending
// queries. nameCache survives bulk replaces; pending-lookup entries (no
// name yet) are dropped on disconnect since any pending response is gone
// with the socket.
// videoHosts maps a model's camserv to the host that serves their live
// snapshot (e.g. 1857 → "video1157"); refreshed on each reconnect and
// every 10 minutes in the background, since a healthy connection can run
// for weeks while MFC reassigns video servers underneath us.
type snapshot struct {
	mu                           sync.RWMutex
	online                       map[int]*streamer
	nameCache                    map[int]nameEntry
	videoHosts                   map[int]string
	bulkApplied                  bool
	lifetimeDisconnects          atomic.Int32
	lifetimeServerConfigFailures atomic.Int32
}

// nameEntry is the per-uid state we keep in nameCache. A non-empty name
// means we've resolved it. A non-zero lookupAt means we've sent a
// USERNAMELOOKUP and are waiting for the response. The two coexist
// transiently: when a name lands, rememberName overwrites the entry and
// implicitly clears lookupAt.
type nameEntry struct {
	name     string
	lastSeen time.Time
	lookupAt time.Time
}

func newSnapshot() *snapshot {
	return &snapshot{
		online:     map[int]*streamer{},
		nameCache:  map[int]nameEntry{},
		videoHosts: map[int]string{},
	}
}

// setVideoHosts atomically replaces the camserv → video host map.
func (s *snapshot) setVideoHosts(hosts map[int]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.videoHosts = hosts
}

// rememberName records uid → name with the current timestamp. Overwriting
// the entry implicitly clears any pending lookupAt: a name lands, the
// USERNAMELOOKUP we were dedupe-tracking is now answered. Empty names are
// ignored so callers don't have to pre-check. Caller must hold s.mu
// (write lock).
func (s *snapshot) rememberName(uid int, name string) {
	if name == "" {
		return
	}
	s.nameCache[uid] = nameEntry{name: name, lastSeen: time.Now()}
}

// validate returns a non-nil error if the snapshot is in a state
// that should not occur on a healthy MFC session — specifically, bulk
// applied but zero entries. The caller should propagate to end the session
// and reconnect. MFC has thousands of streamers online at any time, so an
// empty snapshot after a bulk landed indicates upstream inconsistency, and
// a fresh dial is the only recovery.
func (s *snapshot) validate() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bulkApplied && len(s.online) == 0 {
		return errors.New("post-bulk snapshot empty, MFC data inconsistent")
	}
	return nil
}

// replaceFromBulk replaces the snapshot with a fresh bulk dump. Returns the
// uids of rows that arrived without a name and weren't satisfied by the
// name cache; the caller can fire USERNAMELOOKUP for each. A nameless row
// whose nameCache entry has a lookupAt newer than lookupTTL is omitted from
// toLookup — the previous in-session lookup is still considered in flight.
func (s *snapshot) replaceFromBulk(rows bulk, lookupTTL time.Duration) (toLookup []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.online = make(map[int]*streamer, len(rows))
	for _, r := range rows {
		// MFC's bulk in practice contains only online rows; this branch
		// is defensive against a rare stray offline entry.
		if r.vs == mfcFCVideoOffline {
			cmdlib.Ldbg("bulk contains offline row: @uid = %d, @nm = %s", r.uid, r.name)
			continue
		}
		name := r.name
		if name == "" {
			if cached, found := s.nameCache[r.uid]; found && cached.name != "" {
				name = cached.name
				cmdlib.Ldbg("bulk cache hit: @uid = %d, name = %s", r.uid, name)
			}
		}
		s.online[r.uid] = &streamer{
			name:    name,
			uid:     r.uid,
			vs:      r.vs,
			rc:      r.rc,
			topic:   decodeMFCTopic(r.topic),
			camserv: r.camserv,
		}
		if name == "" {
			if s.claimLookupLocked(r.uid, lookupTTL) {
				toLookup = append(toLookup, r.uid)
				cmdlib.Ldbg("bulk cache miss: @uid = %d, @nm = <null>, lookup queued", r.uid)
			} else {
				cmdlib.Ldbg("bulk cache miss: @uid = %d, @nm = <null>, recent lookup deduped", r.uid)
			}
		} else {
			s.rememberName(r.uid, name)
		}
	}
	s.bulkApplied = true
	return toLookup
}

// applyUpdate merges one SESSIONSTATE update. A vs of FCVIDEO.OFFLINE removes
// the streamer. Returns a non-nil error when the snapshot exceeded
// cfg.MaxSnapshotSize; the caller should propagate to end the session.
func (s *snapshot) applyUpdate(u sessionUpdate, cfg *config) error {
	if u.UID == nil {
		cmdlib.Ldbg("sessionstate with @uid = <null>")
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := *u.UID
	if u.VS != nil && *u.VS == mfcFCVideoOffline {
		s.handleOffline(u)
		return nil
	}
	cur, existed := s.online[uid]
	oldName := ""
	if existed {
		oldName = cur.name
	}
	if !existed {
		if len(s.online) >= cfg.MaxSnapshotSize {
			return fmt.Errorf("snapshot exceeded %d entries", cfg.MaxSnapshotSize)
		}
		cur = &streamer{uid: uid}
		s.online[uid] = cur
	}
	oldVS := cur.vs
	if u.Name != nil {
		cur.name = *u.Name
	}
	// Fall back to the cache when the resolved name is empty — covers both
	// wire-absent (u.Name == nil) and wire-empty (*u.Name == "") cases, plus
	// existing placeholder entries whose name we still haven't resolved.
	// Skip pending-lookup cache entries (no name yet).
	if cur.name == "" {
		if cached, found := s.nameCache[uid]; found && cached.name != "" {
			cur.name = cached.name
		}
	}
	if u.VS != nil {
		cur.vs = *u.VS
	}
	if rc := u.rc(); rc != nil {
		cur.rc = *rc
	}
	if topic := u.topic(); topic != nil {
		cur.topic = decodeMFCTopic(*topic)
	}
	if cs := u.camserv(); cs != nil {
		cur.camserv = *cs
	}
	if cur.name != "" {
		s.rememberName(uid, cur.name)
	}
	nm := displayWireStr(u.Name)
	vs := displayWireInt(u.VS)
	resolved := displayResolved(cur.name)
	if !existed {
		cmdlib.Ldbg("update: @uid = %d, @nm = %s, @vs = %s, name = %s, vs change = untracked -> %s",
			uid, nm, vs, resolved, mfcStateName(cur.vs))
	} else {
		// Aggregate name and vs deltas onto one status-change line so a
		// single SESSIONSTATE that flips both is fully visible.
		var diffs []string
		if cur.name != oldName {
			diffs = append(diffs, fmt.Sprintf("name change = %s -> %s", displayResolved(oldName), resolved))
		}
		if cur.vs != oldVS {
			diffs = append(diffs, fmt.Sprintf("vs change = %s -> %s", mfcStateName(oldVS), mfcStateName(cur.vs)))
		}
		if len(diffs) == 0 {
			return nil
		}
		cmdlib.Ldbg("update: @uid = %d, @nm = %s, @vs = %s, name = %s, %s",
			uid, nm, vs, resolved, strings.Join(diffs, ", "))
	}
	s.logCounts()
	return nil
}

// handleOffline marks uid offline: removes it from the online map if
// present, otherwise just logs that an unknown uid went offline. Either
// branch emits a status-change line and a counts line. Caller must hold
// s.mu (write lock) and pass an update whose UID is non-nil.
func (s *snapshot) handleOffline(u sessionUpdate) {
	uid := *u.UID
	nm := displayWireStr(u.Name)
	vs := displayWireInt(u.VS)
	cur, ok := s.online[uid]

	// Pick the best name available: wire > tracked > cache.
	name := ""
	switch {
	case u.Name != nil && *u.Name != "":
		name = *u.Name
	case ok && cur.name != "":
		name = cur.name
	default:
		if cached, found := s.nameCache[uid]; found {
			name = cached.name
		}
	}
	if name != "" {
		s.rememberName(uid, name)
	}

	label := "update (unknown uid)"
	oldVS := "untracked"
	if ok {
		delete(s.online, uid)
		label = "update"
		oldVS = mfcStateName(cur.vs)
	}
	cmdlib.Ldbg("%s: @uid = %d, @nm = %s, @vs = %s, name = %s, vs change = %s -> %s",
		label, uid, nm, vs, displayResolved(name), oldVS, mfcStateName(mfcFCVideoOffline))
	s.logCounts()
}

// markDisconnected wipes the online map and clears bulkApplied so /online
// reports 503 until the next bulk lands. Pending-lookup entries in
// nameCache (no name yet) are dropped too: any in-flight USERNAMELOOKUP
// is gone with the socket, so the next session must be free to re-fire.
// Resolved-name entries and videoHosts survive across sessions. Returns
// the prior bulkApplied so the caller can tell in one atomic step
// whether the just-ended session was healthy.
func (s *snapshot) markDisconnected() (hadBulkApplied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hadBulkApplied = s.bulkApplied
	clear(s.online)
	s.bulkApplied = false
	for uid, e := range s.nameCache {
		if e.name == "" {
			delete(s.nameCache, uid)
		}
	}
	return hadBulkApplied
}

// applyRoomData updates room counts in bulk from an FCType=ROOMDATA frame.
// Returns (matched, unknown) — how many uids in the payload were tracked
// online vs. unknown to us.
func (s *snapshot) applyRoomData(counts map[int]int) (matched, unknown int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for uid, rc := range counts {
		if cur, ok := s.online[uid]; ok {
			cur.rc = rc
			matched++
		} else {
			unknown++
		}
	}
	return matched, unknown
}

// claimLookup reports whether uid is online without a name and its previous
// USERNAMELOOKUP claim (in nameCache) is older than ttl, stamping a fresh
// claim time on success. The caller fires a USERNAMELOOKUP frame on true.
// The online-membership gate is here so we don't lookup uids we no longer
// care about; the cache-side dedupe is in claimLookupLocked.
func (s *snapshot) claimLookup(uid int, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.online[uid]
	if !ok || cur.name != "" {
		return false
	}
	return s.claimLookupLocked(uid, ttl)
}

// claimLookupLocked is the cache-side core of claimLookup: dedupes against
// nameCache[uid].lookupAt and bumps it on success. Caller must hold s.mu
// (write lock). Used directly by replaceFromBulk, where the online check
// is implicit (we just inserted the row and know it's nameless).
func (s *snapshot) claimLookupLocked(uid int, ttl time.Duration) bool {
	e := s.nameCache[uid]
	if e.name != "" {
		return false
	}
	if !e.lookupAt.IsZero() && time.Since(e.lookupAt) < ttl {
		return false
	}
	now := time.Now()
	e.lookupAt = now
	if e.lastSeen.IsZero() {
		e.lastSeen = now
	}
	s.nameCache[uid] = e
	return true
}

// pruneNameCache drops nameCache entries whose lastSeen is older than the
// applicable TTL: resolvedTTL for entries with a name, pendingTTL for
// in-flight lookups (which only need to live long enough to dedupe a
// re-attempt). Returns how many were removed.
func (s *snapshot) pruneNameCache(resolvedTTL, pendingTTL time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	resolvedCutoff := now.Add(-resolvedTTL)
	pendingCutoff := now.Add(-pendingTTL)
	removed := 0
	for uid, e := range s.nameCache {
		cutoff := resolvedCutoff
		if e.name == "" {
			cutoff = pendingCutoff
		}
		if e.lastSeen.Before(cutoff) {
			delete(s.nameCache, uid)
			removed++
		}
	}
	return removed
}

// logCounts emits a snapshot-counts line. Called after each event log so the
// per-event line can stay terse and the counts get their own readable line.
// Caller must hold s.mu (any kind). Skipped at non-debug verbosity so the
// O(online) placeholder walk is paid only when it would actually log.
func (s *snapshot) logCounts() {
	if cmdlib.Verbosity < cmdlib.DbgVerbosity {
		return
	}
	pending := 0
	for _, cur := range s.online {
		if cur.name == "" {
			pending++
		}
	}
	cmdlib.Ldbg("snapshot: online = %d, name cache = %d, pending lookups = %d, disconnects = %d, server config failures = %d",
		len(s.online), len(s.nameCache), pending,
		s.lifetimeDisconnects.Load(), s.lifetimeServerConfigFailures.Load())
}

// collectIfReady returns the streamers map and a readiness flag in one
// atomic step. ok is gated on bulkApplied alone (not len(online) > 0)
// since a SESSIONSTATE can land before the bulk and populate online
// without giving us a complete view. Nameless entries are skipped.
func (s *snapshot) collectIfReady() (streamers map[string]cmdlib.StreamerInfo, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bulkApplied {
		return nil, false
	}
	out := make(map[string]cmdlib.StreamerInfo, len(s.online))
	for _, cur := range s.online {
		if cur.name == "" {
			continue
		}
		viewers := cur.rc
		img := mfcLiveSnapshotURL(cur.uid, cur.vs, s.videoHosts[cur.camserv])
		if img == "" {
			img = mfcImageURL(cur.uid)
		}
		out[strings.ToLower(cur.name)] = cmdlib.StreamerInfo{
			ImageURL: img,
			Viewers:  &viewers,
			ShowKind: mfcShowKind(cur.vs),
			Subject:  cur.topic,
		}
	}
	return out, true
}

// marshalOnline returns the JSON-serialised online list. When no bulk has
// been applied yet, the body is failed=true so the response shape is
// uniform across ready/unready states.
func (s *snapshot) marshalOnline() []byte {
	var result *cmdlib.OnlineListResults
	if streamers, ok := s.collectIfReady(); ok {
		result = cmdlib.NewOnlineListResults(streamers, 0)
	} else {
		result = cmdlib.NewOnlineListResultsFailed()
	}
	body, err := json.Marshal(result)
	if err != nil {
		cmdlib.Lerr("marshal online list: %v", err)
		return []byte(`{"streamers":null,"duration":0,"failed":true}`)
	}
	return body
}

// applyBulk fetches the deferred MANAGELIST/CAMS payload referenced by ext
// and replaces the snapshot with it. Returns uids of bulk rows that arrived
// without a name and weren't satisfied by the local cache; the caller can
// fire USERNAMELOOKUP for each. A non-nil error means the bulk could not
// be installed (fetch failure or size cap exceeded); the caller should end
// the session so we reconnect and try again.
func (s *snapshot) applyBulk(
	ctx context.Context,
	cfg *config,
	client *cmdlib.Client,
	ext *mfcExtData,
) ([]int, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, cfg.ExtDataFetchTimeout)
	defer cancel()
	rows, err := fetchExtData(fetchCtx, client, cfg, ext)
	if err != nil {
		return nil, err
	}
	if err := rows.validate(cfg); err != nil {
		return nil, err
	}
	toLookup := s.replaceFromBulk(rows, cfg.LookupTTL)
	cmdlib.Linf("bulk dump applied: %s", rows.breakdownByShowKind())
	return toLookup, nil
}
