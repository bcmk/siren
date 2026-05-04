// Package main implements adapter-mfc:
// a microservice that adapts MyFreeCams' FCS WebSocket protocol to a simple
// HTTP/JSON interface, maintaining a live online-models list and serving the
// snapshot over HTTP.
//
// On connect the service ingests the bulk MANAGELIST/CAMS dump as the initial snapshot,
// then applies SESSIONSTATE updates as MFC pushes them.
// On disconnect it reconnects with exponential backoff.
//
// HTTP routes:
//   - GET /online   — current snapshot as OnlineListResults JSON. Always 200;
//     when no bulk has been applied yet, the body has failed=true so the
//     caller distinguishes daemon-level failure from transport failure (5xx).
//   - GET /status?name=<name> — backs the bot's QueryStatus for MFC.
//     Returns {"status": <int>} using cmdlib.StatusKind. See handleStatus.
//   - GET /healthz  — liveness probe; always 200 "ok" while the process is
//     responsive. Readiness is signalled by /online's failed flag.
//   - GET /version  — the build's cmdlib.Version string.
//
// Log convention: @-prefixed keys (@nm, @vs, @uid) are raw protocol fields;
// bare keys are values resolved from local state.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/coder/websocket"
)

// External resource URLs and connection-setup constants. The wire-format
// constants and parsing live in ws_parse.go.
const (
	mfcServerConfigURL = "https://www.myfreecams.com/_js/serverconfig.js"
	mfcExtDataURL      = "https://www.myfreecams.com/php/FcwExtResp.php?respkey=%d&type=%d&opts=%d&serv=%d"

	mfcWSOrigin = "https://m.myfreecams.com"

	wsReadLimit             = 8 * 1024 * 1024
	wsKeepAliveEvery        = 15 * time.Second
	reconnectBackoffInitial = time.Second
	httpReadTimeout         = 10 * time.Second
	httpWriteTimeout        = 30 * time.Second
	httpShutdownTimeout     = 5 * time.Second
	wsWriteTimeout          = 10 * time.Second
	pendingLookupTTL        = time.Hour
	nameCachePruneEvery     = time.Hour
	videoHostsRefreshEvery  = 10 * time.Minute
	// statusLookupTimeout caps how long /status waits for an MFC reply
	// before returning 504. MFC normally answers in well under a second;
	// the cap protects the handler from a silently-dropping upstream.
	statusLookupTimeout = 5 * time.Second
)

func main() {
	cfg := readConfig()
	switch {
	case cfg.Trace:
		cmdlib.Verbosity = cmdlib.TraceVerbosity
	case cfg.Debug:
		cmdlib.Verbosity = cmdlib.DbgVerbosity
	default:
		cmdlib.Verbosity = cmdlib.InfVerbosity
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client := cmdlib.HTTPClientWithTimeoutAndAddress(
		cfg.TimeoutSeconds, cfg.SourceIPAddress, false)

	if !*daemonMode {
		runOnceMode(rootCtx, cfg, client)
		return
	}
	if err := runDaemonMode(rootCtx, cfg, client); err != nil {
		cmdlib.Lerr("%v", err)
		os.Exit(1)
	}
}

// runOnceMode fetches the bulk online list once, prints it to stdout in the
// "<nickname> <image_url>" format used by siren-online-list, and exits.
// An overall deadline (from --once-timeout) caps dial-through-bulk so a
// dead upstream can't hang the process indefinitely.
func runOnceMode(ctx context.Context, cfg *config, client *cmdlib.Client) {
	if *onceTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *onceTimeout)
		defer cancel()
	}
	snap := newSnapshot()
	err := runWebsocketSession(ctx, snap, cfg, client, func(sessCtx context.Context, _ *wsSession, f frame) (bool, error) {
		msg, err := decodeFrame(f)
		if err != nil {
			return false, fmt.Errorf("decode @fctype = %d, %w", f.fcType, err)
		}
		bulk, ok := msg.(*bulkRefMsg)
		if !ok {
			return false, nil
		}
		// Drop the to-lookup list: we exit before any USERNAMELOOKUP could reply.
		if _, err := snap.applyBulk(sessCtx, cfg, client, bulk.ext); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		// User-initiated shutdown (SIGINT/SIGTERM) cancels ctx, which
		// surfaces as a non-nil error from the session loop. Exit cleanly
		// in that case so wrappers don't see a spurious failure. A
		// deadline-exceeded ctx is the --once-timeout firing — that's a
		// real failure, fall through and report it.
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		cmdlib.Lerr("%v", err)
		os.Exit(1)
	}
	streamers, ok := snap.collectIfReady()
	if !ok {
		panic("unreachable: stop implies bulkApplied")
	}
	if len(streamers) == 0 {
		cmdlib.Lerr("bulk produced no streamers with resolved names")
		os.Exit(1)
	}
	for _, nickname := range slices.Sorted(maps.Keys(streamers)) {
		fmt.Printf("%s %s\n", nickname, streamers[nickname].ImageURL)
	}
}

// runDaemonMode runs the long-lived websocket reader and HTTP server. It
// derives an internal context from parentCtx so that an http listen failure
// can shut down the websocket and refresher goroutines without touching the
// signal-cancel context owned by main. Returns nil on graceful shutdown,
// or the http listen error otherwise.
//
// Background goroutines started here:
//   - manageWebsocketSessions: keeps the FCS websocket connected and applies
//     incoming frames to snap.
//   - runNameCachePruner: drops nameCache entries older than cfg.NameCacheTTL.
//   - runVideoHostsRefresher: re-fetches the camserv → video host map.
//   - runSnapshotCountsLogger: emits the snapshot-counts line at info level
//     every snapshotCountsLogEvery so production logs carry it regardless
//     of verbosity.
//   - http shutdown watcher: blocks on ctx.Done() then calls srv.Shutdown.
func runDaemonMode(parentCtx context.Context, cfg *config, client *cmdlib.Client) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	snap := newSnapshot()

	srv := &http.Server{
		Addr:         cfg.ListenAddress,
		Handler:      buildMux(snap),
		ReadTimeout:  httpReadTimeout,
		WriteTimeout: httpWriteTimeout,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		manageWebsocketSessions(ctx, snap, cfg, client)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		runNameCachePruner(ctx, snap, cfg)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		runVideoHostsRefresher(ctx, snap, cfg, client)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		runSnapshotCountsLogger(ctx, snap, cfg)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	cmdlib.Linf("listening on %s", cfg.ListenAddress)
	err := srv.ListenAndServe()
	cancel()
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("http server, %w", err)
}

// buildMux wires up the HTTP routes. All routes are read-only and accept
// GET (and HEAD, which net/http handles for free); other methods get 405.
func buildMux(snap *snapshot) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/online", getOnly(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(snap.marshalOnline())
	}))
	mux.HandleFunc("/status", getOnly(handleStatus(snap)))
	mux.HandleFunc("/healthz", getOnly(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	}))
	mux.HandleFunc("/version", getOnly(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintln(w, cmdlib.Version)
	}))
	return mux
}

// statusResult is the JSON body of /status. The Status field carries a
// cmdlib.StatusKind value so the bot's OnlineListAdapter.QueryStatus
// can decode without translation.
type statusResult struct {
	Status cmdlib.StatusKind `json:"status"`
}

// handleStatus answers GET /status?name=<name> by sending an
// FCTYPE.USERNAMELOOKUP on the live websocket and mapping the reply to a
// cmdlib.StatusKind: Online when the reply has a uid and vs != 127,
// Offline when uid present but vs == 127, NotFound when no uid. Returns
// 503 when no session is connected, 504 when MFC does not reply within
// statusLookupTimeout, and 400 on a missing or malformed name. Transport
// failures surface as 5xx so the caller can map them to StatusUnknown.
func handleStatus(snap *snapshot) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "missing name", http.StatusBadRequest)
			return
		}
		if !mfcUsernameRegexp.MatchString(name) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		sess := snap.currentSession.Load()
		if sess == nil {
			http.Error(w, "no live mfc session", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), statusLookupTimeout)
		defer cancel()
		reply, err := sess.lookupByName(ctx, name)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				http.Error(w, "lookup timeout", http.StatusGatewayTimeout)
				return
			}
			if errors.Is(err, context.Canceled) {
				// 499: nginx's Client Closed Request (no stdlib constant).
				http.Error(w, "client cancelled", 499)
				return
			}
			cmdlib.Linf("/status lookup failed: name = %s, %v", name, err)
			http.Error(w, "lookup failed", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statusResult{Status: mfcLookupStatus(reply, name)})
	}
}

// mfcLookupStatus maps a USERNAMELOOKUP reply to a cmdlib.StatusKind.
// MFC has two not-found shapes — a raw-string echo of the queried name,
// and a JSON object missing uid — both collapse to StatusNotFound here.
// The echo case is detected by exact byte-equality with queriedName so
// the test is strict (the JSON-shape sniff in decodeFrame is just a
// parser-safety guard, not the not-found signal).
func mfcLookupStatus(m *lookupResponseMsg, queriedName string) cmdlib.StatusKind {
	if string(m.payload) == queriedName {
		return cmdlib.StatusNotFound
	}
	if m.update.UID == nil || *m.update.UID == 0 {
		return cmdlib.StatusNotFound
	}
	if m.update.VS != nil && *m.update.VS == mfcFCVideoOffline {
		return cmdlib.StatusOffline
	}
	return cmdlib.StatusOnline
}

// lookupReplyDesc renders a one-liner describing a USERNAMELOOKUP reply
// for the dispatcher's debug log. Shows the parsed uid/name when the
// payload was JSON, or a dump of the raw bytes otherwise so the two
// cases stop looking identical.
func lookupReplyDesc(m *lookupResponseMsg) string {
	if m.update.UID == nil && m.update.Name == nil {
		return fmt.Sprintf("payload = %s", dumpBytes(m.payload))
	}
	return fmt.Sprintf("@uid = %s, @nm = %s",
		displayWireInt(m.update.UID), displayWireStr(m.update.Name))
}

// mfcUsernameRegexp restricts /status's name parameter to the character
// class MFC actually uses (ASCII letters, digits, underscore) plus a
// length bound. The narrow set keeps the URL-encoded payload predictable
// and prevents a caller from sneaking in spaces or control bytes that
// would desync the FCS frame.
var mfcUsernameRegexp = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// getOnly rejects requests with methods other than GET or HEAD, advertising
// the allowed set per RFC 9110 § 10.2.1.
func getOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// Diagnostic-log formatters with three distinct missing-value markers so a
// glance at the log tells which layer the missing value came from:
// "<null>" — raw wire field absent, "<empty>" — wire explicitly empty, and
// "<unknown>" — locally-resolved name we haven't filled in yet.

func displayWireStr(p *string) string {
	if p == nil {
		return "<null>"
	}
	if *p == "" {
		return "<empty>"
	}
	return *p
}

func displayWireInt(p *int) string {
	if p == nil {
		return "<null>"
	}
	return strconv.Itoa(*p)
}

func displayResolved(s string) string {
	if s == "" {
		return "<unknown>"
	}
	return s
}

// runNameCachePruner periodically drops expired entries from the name cache.
func runNameCachePruner(ctx context.Context, snap *snapshot, cfg *config) {
	t := time.NewTicker(nameCachePruneEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n := snap.pruneNameCache(cfg.NameCacheTTL, pendingLookupTTL); n > 0 {
				cmdlib.Linf("name cache pruned %d entries (resolved TTL %v, pending-lookup TTL %v)",
					n, cfg.NameCacheTTL, pendingLookupTTL)
			}
		}
	}
}

// runVideoHostsRefresher periodically re-fetches MFC's serverconfig.js to
// keep the camserv → video host map current. We can't rely on dial-time
// refresh alone: a healthy connection may run for weeks, by which point
// MFC has reassigned video servers and our snapshot URLs would 404.
// On fetch failure we log and keep the existing map.
func runVideoHostsRefresher(ctx context.Context, snap *snapshot, cfg *config, client *cmdlib.Client) {
	t := time.NewTicker(videoHostsRefreshEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sc, err := fetchMFCServerConfig(ctx, snap, client, cfg)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				cmdlib.Linf("refresh video hosts: %v", err)
				continue
			}
			snap.setVideoHosts(sc.videoHosts)
			cmdlib.Linf("refreshed video hosts: %d entries", len(sc.videoHosts))
		}
	}
}

// runSnapshotCountsLogger emits the snapshot-counts line at info level on a
// fixed interval. The same line is logged after every event at debug level
// by snapshot.logCounts; this runner duplicates it at info so production
// logs (which run at info verbosity) carry a periodic heartbeat of online,
// name cache, pending lookups, and lifetime disconnect/serverconfig
// failure counters.
func runSnapshotCountsLogger(ctx context.Context, snap *snapshot, cfg *config) {
	t := time.NewTicker(cfg.SnapshotCountsLogEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			snap.logCountsInfo()
		}
	}
}

// dispatchFrame routes one parsed FCS frame to its handler. A non-nil
// return ends the session and triggers a reconnect; both decode errors
// (malformed payload of a known FCType) and handler errors are fatal.
// Decode errors look local but aren't safe to skip: dropping a bad
// SESSIONSTATE could leave a model "forever online" if the lost event
// was its vs=127 (offline) — only a fresh bulk via reconnect resyncs.
func dispatchFrame(
	sessCtx context.Context,
	snap *snapshot,
	cfg *config,
	client *cmdlib.Client,
	sess *wsSession,
	f frame,
) error {
	if f.fcType == mfcFCTypeNull {
		cmdlib.Ltrace("heartbeat")
		return nil
	}
	msg, err := decodeFrame(f)
	if err != nil {
		return fmt.Errorf("decode @fctype = %d, %w", f.fcType, err)
	}
	if msg == nil {
		cmdlib.Ltrace("unhandled @fctype = %d, @arg2 = %d, @payload = %s", f.fcType, f.arg2, f.payload)
		return nil
	}
	switch m := msg.(type) {
	case *bulkRefMsg:
		toLookup, err := snap.applyBulk(sessCtx, cfg, client, m.ext)
		if err != nil {
			return err
		}
		for _, uid := range toLookup {
			if err := sess.requestNameLookup(sessCtx, uid); err != nil {
				return err
			}
		}
		return nil
	case *sessionStateMsg:
		if err := snap.applyUpdate(m.update, cfg); err != nil {
			return err
		}
		if m.update.UID != nil && snap.claimLookup(*m.update.UID, cfg.LookupTTL) {
			if err := sess.requestNameLookup(sessCtx, *m.update.UID); err != nil {
				return err
			}
		}
		return nil
	case *lookupResponseMsg:
		// HTTP-driven lookups (e.g. /status) registered their qid; hand the
		// reply straight to the waiter and skip the snapshot apply so an
		// existence check for an offline model does not fold into the
		// online list.
		if sess.deliverLookup(m.qid, m) {
			cmdlib.Ldbg("lookup response (delivered): @qid = %d, %s",
				m.qid, lookupReplyDesc(m))
			return nil
		}
		if err := snap.applyUpdate(m.update, cfg); err != nil {
			return err
		}
		cmdlib.Ldbg("lookup response: @qid = %d, %s", m.qid, lookupReplyDesc(m))
		return nil
	case *roomDataMsg:
		matched, unknown := snap.applyRoomData(m.counts)
		cmdlib.Ldbg("roomdata applied: %d matched, %d unknown", matched, unknown)
		return nil
	}
	return nil
}

// manageWebsocketSessions keeps a websocket connected for the lifetime of ctx,
// applying frames to snap. It reconnects with exponential backoff on errors.
func manageWebsocketSessions(ctx context.Context, snap *snapshot, cfg *config, client *cmdlib.Client) {
	backoff := reconnectBackoffInitial
	for ctx.Err() == nil {
		err := runWebsocketSession(ctx, snap, cfg, client, func(sessCtx context.Context, sess *wsSession, f frame) (bool, error) {
			return false, dispatchFrame(sessCtx, snap, cfg, client, sess, f)
		})
		// markDisconnected reports whether the bulk had landed during this
		// session in the same atomic step as the wipe. Bulk arrival means
		// dial, login, and the first server-pushed bulk all worked — the
		// direct signal of a healthy session. Reset backoff so the next
		// reconnect retries promptly instead of inheriting the previous
		// attempt's cap.
		if snap.markDisconnected() {
			backoff = reconnectBackoffInitial
		}
		if ctx.Err() != nil {
			return
		}
		cmdlib.Linf("%v; reconnecting in %s", err, backoff)
		snap.lifetimeDisconnects.Add(1)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > cfg.ReconnectBackoffMax {
			backoff = cfg.ReconnectBackoffMax
		}
	}
}

// runWebsocketSession dials, runs keepalive, and feeds each parsed frame to
// handle until handle reports stop, returns an error, the read times out,
// or ctx is cancelled. The context passed to handle is bound to the
// connection's lifetime; the wsSession lets handle send outbound requests
// on the same connection. The fresh camserv → video host map fetched at
// dial time is installed on snap so live-snapshot URLs stay accurate
// across reconnects.
func runWebsocketSession(
	ctx context.Context,
	snap *snapshot,
	cfg *config,
	client *cmdlib.Client,
	handle func(sessCtx context.Context, sess *wsSession, f frame) (stop bool, err error),
) (err error) {
	dialCtx, dialCancel := context.WithTimeout(ctx, cfg.WSConnectTimeout)
	conn, videoHosts, err := dialMFC(dialCtx, snap, client, cfg)
	dialCancel()
	if err != nil {
		return err
	}
	snap.setVideoHosts(videoHosts)
	sess := newWSSession(conn)
	snap.currentSession.Store(sess)

	// Keepalive: MFC drops the session if it does not see periodic NULL
	// frames from the client. Run for the connection's lifetime. Cleanup
	// order matters: cancel the context (so keepalive exits its select),
	// wait for it to acknowledge, then close the conn. Close status
	// reflects the outcome: normal closure on a clean stop or parent-ctx
	// cancellation (graceful shutdown), internal error otherwise. Clearing
	// snap.currentSession and draining pending lookups happens before the
	// physical close so HTTP handlers see "no session" rather than a stale
	// pointer to a closed connection.
	connCtx, connCancel := context.WithCancel(ctx)
	keepAliveDone := make(chan struct{})
	go func() {
		defer close(keepAliveDone)
		runKeepAlive(connCtx, sess)
	}()
	defer func() {
		snap.currentSession.CompareAndSwap(sess, nil)
		sess.closePendingLookups()
		connCancel()
		<-keepAliveDone
		status := websocket.StatusNormalClosure
		if err != nil && ctx.Err() == nil {
			status = websocket.StatusInternalError
		}
		closeAndLog(conn, status, "", "session close")
	}()

	return runFrameLoop(connCtx, conn, cfg.WSIdleTimeout, func(data []byte) (bool, error) {
		var (
			stopErr error
			stopOK  bool
		)
		framesWalked, walkErr := walkFrames(data, func(f frame) bool {
			stop, err := handle(connCtx, sess, f)
			if err != nil {
				stopErr = err
				return true
			}
			if stop {
				stopOK = true
				return true
			}
			return false
		})
		// Handler errors take precedence; only fall through to walkErr
		// if the visitor finished cleanly. Both classes are fatal —
		// reconnect either way.
		if stopErr != nil {
			return false, stopErr
		}
		if walkErr != nil {
			// Dump the message head so we can tell whether the failure
			// was a non-FCS payload (head won't start with 6 ASCII
			// digits), a misalignment from an earlier wrong-length
			// frame (framesWalked > 0, head looks like normal FCS),
			// or some new framing variant. %q escapes non-printable
			// bytes so a single log line stays readable.
			cmdlib.Lerr("frame walk failed: %v after %d ok frames; head = %s",
				walkErr, framesWalked, dumpBytes(data))
			return false, walkErr
		}
		if stopOK {
			return true, nil
		}
		return false, snap.validate()
	})
}

// wsSession owns a single MFC websocket connection plus the per-connection
// state we need for outbound requests (write serialisation, query-id counter).
// USERNAMELOOKUP dedupe for snapshot-fill traffic lives on the snapshot via
// nameEntry.lookupAt; pending tracks qids of HTTP-driven lookups so the
// frame dispatcher can hand the response to the waiting caller instead of
// folding it into the snapshot.
type wsSession struct {
	conn        *websocket.Conn
	writeMu     sync.Mutex
	nextQueryID atomic.Int64
	pendingMu   sync.Mutex
	pending     map[int64]chan *lookupResponseMsg
}

func newWSSession(conn *websocket.Conn) *wsSession {
	return &wsSession{
		conn:    conn,
		pending: map[int64]chan *lookupResponseMsg{},
	}
}

// registerPendingLookup reserves qid as a HTTP-driven lookup and returns the
// reply channel. The channel is buffered so deliverLookup never blocks on a
// caller that has already given up.
func (s *wsSession) registerPendingLookup(qid int64) chan *lookupResponseMsg {
	ch := make(chan *lookupResponseMsg, 1)
	s.pendingMu.Lock()
	s.pending[qid] = ch
	s.pendingMu.Unlock()
	return ch
}

// cancelPendingLookup drops the qid registration; used when the caller's
// context fires before a response arrives, or when the write itself failed.
func (s *wsSession) cancelPendingLookup(qid int64) {
	s.pendingMu.Lock()
	delete(s.pending, qid)
	s.pendingMu.Unlock()
}

// deliverLookup hands msg to the waiter registered for qid, returning true on
// hit. The dispatcher uses this to route HTTP-driven lookup replies; misses
// fall through to the snapshot-apply path.
func (s *wsSession) deliverLookup(qid int, msg *lookupResponseMsg) bool {
	s.pendingMu.Lock()
	ch, ok := s.pending[int64(qid)]
	if ok {
		delete(s.pending, int64(qid))
	}
	s.pendingMu.Unlock()
	if !ok {
		return false
	}
	ch <- msg
	return true
}

// closePendingLookups closes every pending reply channel; waiters see a
// zero-value receive and return a "session ended" error. Called from the
// session teardown path.
func (s *wsSession) closePendingLookups() {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for qid, ch := range s.pending {
		close(ch)
		delete(s.pending, qid)
	}
}

// write serialises writes on the websocket. coder/websocket allows reads to
// run concurrently with writes, but writes must not be concurrent with each
// other.
func (s *wsSession) write(ctx context.Context, payload string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageText, []byte(payload))
}

// closeAndLog closes conn; if Close returns an error, trace-logs it tagged
// with op. Used in cleanup paths where a close failure is not actionable
// but we still want the trace breadcrumb for diagnosis.
func closeAndLog(conn *websocket.Conn, code websocket.StatusCode, reason, op string) {
	if err := conn.Close(code, reason); err != nil {
		cmdlib.Ltrace("%s: %v", op, err)
	}
}

// requestNameLookup sends an FCTYPE.USERNAMELOOKUP for uid. Dedupe lives in
// the caller via snapshot.claimLookup. The server answers with an
// FCTYPE.USERNAMELOOKUP frame whose payload looks like a SESSIONSTATE record,
// which our normal dispatcher applies to fill in the placeholder.
func (s *wsSession) requestNameLookup(ctx context.Context, uid int) error {
	qid := s.nextQueryID.Add(1)
	frame := encodeUsernameLookupByUID(qid, uid)
	writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	if err := s.write(writeCtx, frame); err != nil {
		return fmt.Errorf("lookup write failed for uid %d, %w", uid, err)
	}
	cmdlib.Ldbg("lookup requested: @uid = %d, @qid = %d", uid, qid)
	return nil
}

// lookupByName sends an FCTYPE.USERNAMELOOKUP whose payload is a username
// (MFC accepts either uid or name in that slot) and waits for the matching
// reply. Unlike requestNameLookup, the response is delivered straight to
// this caller via the pending-qid registry; the snapshot is left untouched
// so HTTP existence checks for offline models do not pollute the online
// list. Returns the full reply message (including the notFound flag) so
// the caller can distinguish MFC's two not-found shapes.
func (s *wsSession) lookupByName(ctx context.Context, name string) (*lookupResponseMsg, error) {
	qid := s.nextQueryID.Add(1)
	ch := s.registerPendingLookup(qid)
	frame := encodeUsernameLookupByName(qid, name)
	writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	err := s.write(writeCtx, frame)
	cancel()
	if err != nil {
		s.cancelPendingLookup(qid)
		return nil, fmt.Errorf("lookup write failed for name %s, %w", name, err)
	}
	cmdlib.Ldbg("lookup by name requested: name = %s, @qid = %d", name, qid)
	select {
	case <-ctx.Done():
		s.cancelPendingLookup(qid)
		return nil, ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return nil, errors.New("session ended before lookup reply")
		}
		return msg, nil
	}
}

// runFrameLoop reads frames from conn with an idle timeout and invokes
// handle for each batch. It returns nil when handle reports stop, the
// handle's error on failure, or a translated read failure.
func runFrameLoop(
	ctx context.Context,
	conn *websocket.Conn,
	idleTimeout time.Duration,
	handle func(data []byte) (stop bool, err error),
) error {
	for {
		readCtx, readCancel := context.WithTimeout(ctx, idleTimeout)
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			return translateWSError(err, idleTimeout)
		}
		stop, err := handle(data)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
}

// dumpHeadBytes bounds the diagnostic-dump head so a maximally-large
// websocket message can't blow up the log line.
const dumpHeadBytes = 256

/*
dumpBytes renders b for a single-line diagnostic log:
printable ASCII passes through verbatim,
\\ for backslash,
\n, \r, \t for common control chars,
\xNN for the rest.

Decode with:

	( printf '%b' "$(cat)" ) <<'EOF'
	<paste head value>
	EOF
*/
func dumpBytes(b []byte) string {
	if len(b) > dumpHeadBytes {
		b = b[:dumpHeadBytes]
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		switch {
		case c == '\\':
			sb.WriteString(`\\`)
		case c == '\n':
			sb.WriteString(`\n`)
		case c == '\r':
			sb.WriteString(`\r`)
		case c == '\t':
			sb.WriteString(`\t`)
		case c >= 0x20 && c < 0x7f:
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, `\x%02x`, c)
		}
	}
	return sb.String()
}

// translateHTTPError converts an http.Client.Do error into a human label.
// net.Error.Timeout() is the canonical timeout predicate and covers dial
// timeouts, read deadlines, and Client.Timeout uniformly. Cancelled
// contexts (graceful shutdown) get a separate label. Other errors pass
// through.
func translateHTTPError(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("timeout")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("shutdown in progress")
	}
	return err
}

// translateWSError converts a coder/websocket read error into a human
// description of the actual cause: server-initiated close, idle timeout, or
// the raw error if we can't classify it.
func translateWSError(err error, idleTimeout time.Duration) error {
	if status := websocket.CloseStatus(err); status != -1 {
		if name := wsCloseStatusName(status); name != "" {
			return fmt.Errorf("server closed connection (code=%d, %s)", status, name)
		}
		return fmt.Errorf("server closed connection (code=%d)", status)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("idle timeout: no frames in %s", idleTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("shutdown in progress")
	}
	return err
}

// wsCloseStatusName maps common websocket close codes to short labels.
func wsCloseStatusName(s websocket.StatusCode) string {
	switch s {
	case websocket.StatusNormalClosure:
		return "normal closure"
	case websocket.StatusGoingAway:
		return "going away"
	case websocket.StatusProtocolError:
		return "protocol error"
	case websocket.StatusUnsupportedData:
		return "unsupported data"
	case websocket.StatusNoStatusRcvd:
		return "no status given"
	case websocket.StatusAbnormalClosure:
		return "abnormal close"
	case websocket.StatusInvalidFramePayloadData:
		return "invalid frame payload"
	case websocket.StatusPolicyViolation:
		return "policy violation"
	case websocket.StatusMessageTooBig:
		return "message too big"
	case websocket.StatusInternalError:
		return "server internal error"
	case websocket.StatusServiceRestart:
		return "service restart"
	case websocket.StatusTryAgainLater:
		return "try again later"
	}
	return ""
}

// dialMFC picks a random MFC websocket server, dials it, and completes the
// FCS handshake and guest login. Returns the ready-to-read connection along
// with the camserv → video host map fetched from the same serverconfig.js,
// so the caller can install it on the snapshot.
func dialMFC(ctx context.Context, snap *snapshot, client *cmdlib.Client, cfg *config) (*websocket.Conn, map[int]string, error) {
	sc, err := fetchMFCServerConfig(ctx, snap, client, cfg)
	if err != nil {
		return nil, nil, err
	}
	wsURL := fmt.Sprintf("wss://%s.myfreecams.com/fcsl", sc.wsServer)
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: client.Client,
		HTTPHeader: http.Header{"Origin": []string{mfcWSOrigin}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("ws dial %s, %w", wsURL, err)
	}
	conn.SetReadLimit(wsReadLimit)

	if err := conn.Write(ctx, websocket.MessageText, []byte(mfcWSHandshakeFrame)); err != nil {
		closeAndLog(conn, websocket.StatusInternalError, "", "handshake close")
		return nil, nil, fmt.Errorf("ws handshake, %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(encodeLogin())); err != nil {
		closeAndLog(conn, websocket.StatusInternalError, "", "login close")
		return nil, nil, fmt.Errorf("ws login, %w", err)
	}
	cmdlib.Linf("connected to %s", wsURL)
	return conn, sc.videoHosts, nil
}

// runKeepAlive writes a NULL frame on a ticker until ctx is cancelled or a
// write fails. A failed write means the connection is effectively dead; close
// it so the reader errors out and reconnects. A failure caused by ctx being
// cancelled (graceful shutdown) is silent — the surrounding session teardown
// will close the conn anyway. The exchange is bidirectional: MFC also sends
// NULL frames to us on roughly the same cadence, which keeps the read-side
// idle timer fed even on accounts with no organic traffic.
func runKeepAlive(ctx context.Context, sess *wsSession) {
	t := time.NewTicker(wsKeepAliveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := sess.write(writeCtx, mfcNullFrame)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				cmdlib.Linf("keepalive write failed: %v, closing for reconnect", err)
				closeAndLog(sess.conn, websocket.StatusInternalError, "keepalive write failed", "keepalive close")
				return
			}
		}
	}
}

// validate reports whether a freshly fetched bulk is fit to install. A
// non-nil error means the caller should reject the dump and end the session
// so we reconnect for a fresh one. MFC has thousands of streamers online at
// any time, so an empty bulk is treated as upstream inconsistency.
func (b bulk) validate(cfg *config) error {
	switch {
	case len(b) == 0:
		return errors.New("empty bulk")
	case len(b) > cfg.MaxSnapshotSize:
		return fmt.Errorf("bulk exceeded %d entries", cfg.MaxSnapshotSize)
	}
	return nil
}

// breakdownByShowKind formats a per-show-kind count summary for a bulk dump.
// Categories follow cmdlib.ShowKind so they match what consumers see in the
// JSON response.
func (b bulk) breakdownByShowKind() string {
	var public, private, group, club, away, other, offline int
	for _, r := range b {
		if r.vs == mfcFCVideoOffline {
			offline++
			continue
		}
		switch mfcShowKind(r.vs) {
		case cmdlib.ShowPublic:
			public++
		case cmdlib.ShowPrivate:
			private++
		case cmdlib.ShowGroup:
			group++
		case cmdlib.ShowTicket:
			club++
		case cmdlib.ShowAway:
			away++
		default:
			other++
		}
	}
	total := public + private + group + club + away + other
	parts := []string{fmt.Sprintf("%d total", total)}
	for _, p := range []struct {
		label string
		n     int
	}{
		{"public", public},
		{"private", private},
		{"group", group},
		{"club", club},
		{"away", away},
		{"other", other},
		{"offline", offline},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", p.label, p.n))
		}
	}
	return strings.Join(parts, ", ")
}

// mfcServerConfig holds the parts of MFC's serverconfig.js the daemon
// uses: a randomly picked rfc6455-capable chat server for the FCS dial,
// plus the camserv → video host map for live-snapshot URLs.
type mfcServerConfig struct {
	wsServer   string
	videoHosts map[int]string
}

// fetchBounded issues req and reads up to limit bytes, erroring on non-200 or
// when the response would exceed the cap. Defends against runaway upstream
// responses OOMing the daemon.
func fetchBounded(req *http.Request, client *cmdlib.Client, limit int) ([]byte, error) {
	resp, err := client.Client.Do(req)
	if err != nil {
		return nil, translateHTTPError(err)
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read response, %w", err)
	}
	if len(body) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

// fetchMFCServerConfig fetches and parses MFC's serverconfig.js. The video
// host map is the union of h5video_servers, ngvideo_servers, wzext_servers
// and wzobs_servers; each camserv appears in exactly one of them. Bumps
// snap.lifetimeServerConfigFailures on any error so callers don't have
// to remember to do it themselves.
func fetchMFCServerConfig(
	ctx context.Context,
	snap *snapshot,
	client *cmdlib.Client,
	cfg *config,
) (_ *mfcServerConfig, err error) {
	defer func() {
		if err != nil {
			snap.lifetimeServerConfigFailures.Add(1)
		}
	}()
	req, err := http.NewRequestWithContext(
		ctx, "GET", mfcServerConfigURL, nil)
	if err != nil {
		return nil, err
	}
	body, err := fetchBounded(req, client, cfg.HTTPResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch serverconfig: %w", err)
	}
	var raw struct {
		WebSocketServers map[string]string `json:"websocket_servers"`
		H5VideoServers   map[string]string `json:"h5video_servers"`
		NgVideoServers   map[string]string `json:"ngvideo_servers"`
		WzExtServers     map[string]string `json:"wzext_servers"`
		WzObsServers     map[string]string `json:"wzobs_servers"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse serverconfig, %w", err)
	}
	wsServers := make([]string, 0, len(raw.WebSocketServers))
	for name, proto := range raw.WebSocketServers {
		if proto == "rfc6455" {
			wsServers = append(wsServers, name)
		}
	}
	if len(wsServers) == 0 {
		return nil, errors.New("no websocket servers in serverconfig")
	}
	videoHosts := make(map[int]string,
		len(raw.H5VideoServers)+len(raw.NgVideoServers)+
			len(raw.WzExtServers)+len(raw.WzObsServers))
	for _, m := range []map[string]string{
		raw.H5VideoServers, raw.NgVideoServers,
		raw.WzExtServers, raw.WzObsServers,
	} {
		for csStr, host := range m {
			cs, err := strconv.Atoi(csStr)
			if err != nil {
				continue
			}
			videoHosts[cs] = host
		}
	}
	return &mfcServerConfig{
		wsServer:   wsServers[rand.IntN(len(wsServers))],
		videoHosts: videoHosts,
	}, nil
}

// fetchExtData retrieves and parses a MANAGELIST/CAMS payload referenced by an EXTDATA envelope.
func fetchExtData(
	ctx context.Context,
	client *cmdlib.Client,
	cfg *config,
	ext *mfcExtData,
) (bulk, error) {
	u := fmt.Sprintf(mfcExtDataURL,
		ext.RespKey, ext.Type, ext.Opts, ext.Serv)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	body, err := fetchBounded(req, client, cfg.HTTPResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch extdata: %w", err)
	}
	var list struct {
		RData []any `json:"rdata"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parse extdata, %w", err)
	}
	return parseBulkRData(list.RData)
}

// mfcLiveSnapshotURL builds a 853x480 live snapshot URL, matching MFC's
// player.livesnapUrl() in mfccore.js. videoHost is the per-camserv host
// from serverconfig.js (e.g. "video1157"); when "" the caller should fall
// back to the static avatar. Supports public (vs=0/90) and away (vs=2).
// Other states (private/group/club) aren't worked out yet, so we skip them.
func mfcLiveSnapshotURL(uid, vs int, videoHost string) string {
	if videoHost == "" {
		return ""
	}
	var snapshotPrefix string
	switch vs {
	case 0, 90: // free chat / idle — render as public
	case 2: // away
		snapshotPrefix = "s_"
	default:
		return ""
	}
	return fmt.Sprintf("https://%s.myfreecams.com/snaps/mfc_%s%d_853x480.jpg?no-cache=%d",
		videoHost, snapshotPrefix, uid, time.Now().UnixNano())
}

// mfcImageURL returns the avatar URL for a uid. The bucket is the first three
// digits of the uid rendered as a string, matching MFC's site JS
// (sUserId.substring(0, 3)). 300x300 is the largest size MFC actually serves;
// requests for bigger sizes return a 49-byte clear.gif placeholder.
func mfcImageURL(uid int) string {
	s := strconv.Itoa(uid)
	return fmt.Sprintf(
		"https://img.mfcimg.com/photos2/%s/%s/avatar.300x300.jpg",
		s[:min(3, len(s))], s)
}
