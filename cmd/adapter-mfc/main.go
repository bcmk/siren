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
	"net/http"
	"os"
	"os/signal"
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

	mfcWSOrigin    = "https://m.myfreecams.com"
	mfcWSHandshake = "fcsws_20180422\n\x00"

	mfcLoginVersion = "20080910"
	mfcLoginCreds   = "guest:guest"

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
	mux.HandleFunc("/healthz", getOnly(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	}))
	mux.HandleFunc("/version", getOnly(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintln(w, cmdlib.Version)
	}))
	return mux
}

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
		if err := snap.applyUpdate(m.update, cfg); err != nil {
			return err
		}
		uid := 0
		if m.update.UID != nil {
			uid = *m.update.UID
		}
		cmdlib.Ldbg("lookup response: @qid = %d, @uid = %d, @nm = %s",
			m.qid, uid, displayWireStr(m.update.Name))
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

	// Keepalive: MFC drops the session if it does not see periodic NULL
	// frames from the client. Run for the connection's lifetime. Cleanup
	// order matters: cancel the context (so keepalive exits its select),
	// wait for it to acknowledge, then close the conn. Close status
	// reflects the outcome: normal closure on a clean stop or parent-ctx
	// cancellation (graceful shutdown), internal error otherwise.
	connCtx, connCancel := context.WithCancel(ctx)
	keepAliveDone := make(chan struct{})
	go func() {
		defer close(keepAliveDone)
		runKeepAlive(connCtx, sess)
	}()
	defer func() {
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
		walkErr := walkFrames(data, func(f frame) bool {
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
// USERNAMELOOKUP dedupe lives on the snapshot via nameEntry.lookupAt.
type wsSession struct {
	conn        *websocket.Conn
	writeMu     sync.Mutex
	nextQueryID atomic.Int64
}

func newWSSession(conn *websocket.Conn) *wsSession {
	return &wsSession{conn: conn}
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
	frame := fmt.Sprintf("%d 0 0 %d %d\n\x00", mfcFCTypeUsernameLookup, qid, uid)
	writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	if err := s.write(writeCtx, frame); err != nil {
		return fmt.Errorf("lookup write failed for uid %d, %w", uid, err)
	}
	cmdlib.Ldbg("lookup requested: @uid = %d, @qid = %d", uid, qid)
	return nil
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
		return errors.New("context cancelled")
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
		return nil, nil, fmt.Errorf("server config, %w", err)
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

	if err := conn.Write(ctx, websocket.MessageText, []byte(mfcWSHandshake)); err != nil {
		closeAndLog(conn, websocket.StatusInternalError, "", "handshake close")
		return nil, nil, fmt.Errorf("ws handshake, %w", err)
	}
	login := fmt.Sprintf("%d 0 0 %s 0 %s\n\x00",
		mfcFCTypeLogin, mfcLoginVersion, mfcLoginCreds)
	if err := conn.Write(ctx, websocket.MessageText, []byte(login)); err != nil {
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
// will close the conn anyway.
func runKeepAlive(ctx context.Context, sess *wsSession) {
	const nullFrame = "0 0 0 0 0\n\x00"
	t := time.NewTicker(wsKeepAliveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := sess.write(writeCtx, nullFrame)
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
		return nil, fmt.Errorf("send request, %w", err)
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
		return nil, fmt.Errorf("fetch serverconfig, %w", err)
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
		return nil, fmt.Errorf("fetch extdata, %w", err)
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
