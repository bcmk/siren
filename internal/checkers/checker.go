package checkers

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// BaseCheckerConfig holds the HTTP/network settings every per-site
// checker config embeds.
type BaseCheckerConfig struct {
	TimeoutSeconds       int `mapstructure:"timeout_seconds"`         // HTTP timeout
	MinRequestIntervalMs int `mapstructure:"min_request_interval_ms"` // minimum interval between requests for rate-limited upstreams
	RequestQueueSize     int `mapstructure:"queue_size"`              // status-request queue size; 0 means defaultQueueSize
}

// validateBase checks the universal HTTP settings.
// Sites that don't make HTTP requests skip this.
func (b *BaseCheckerConfig) validateBase() error {
	if b.TimeoutSeconds == 0 {
		return errors.New("configure timeout_seconds")
	}
	if b.MinRequestIntervalMs == 0 {
		return errors.New("configure min_request_interval_ms")
	}
	return nil
}

// Timeout returns the configured HTTP timeout.
func (b *BaseCheckerConfig) Timeout() time.Duration {
	return time.Duration(b.TimeoutSeconds) * time.Second
}

// MinRequestInterval returns the minimum interval between outbound requests.
func (b *BaseCheckerConfig) MinRequestInterval() time.Duration {
	return time.Duration(b.MinRequestIntervalMs) * time.Millisecond
}

// QueueSize returns the configured status-request queue size,
// falling back to defaultQueueSize when unset.
func (b *BaseCheckerConfig) QueueSize() int {
	if b.RequestQueueSize == 0 {
		return defaultQueueSize
	}
	return b.RequestQueueSize
}

// CheckerConfig is satisfied by every per-site config (via the
// embedded BaseCheckerConfig). BaseChecker[T] stores it typed, so
// site code reads c.Cfg.Field directly while Config() exposes it as
// the interface for the daemon and JSON marshalling.
type CheckerConfig interface {
	Timeout() time.Duration
	MinRequestInterval() time.Duration
	QueueSize() int
}

// defaultQueueSize buffers BaseChecker's status-request channel
// when the config doesn't override it.
const defaultQueueSize = 1000

// Capabilities reports which surfaces dispatchers should use
// and which invocation contexts the checker fits.
// A Supports* flag may be false even when the method is defined —
// it then exists but should not be used.
// Must work on an uninitialised checker.
type Capabilities struct {
	SupportsQueryOnlineStreamers          bool
	SupportsQueryFixedListOnlineStreamers bool
	SupportsQueryFixedListStatuses        bool
	SupportsQueryStatus                   bool
	SupportsCLI                           bool // true when the checker fits standalone CLI tools.
	SupportsSubject                       bool // true when room subjects are surfaced.
}

// UsesFixedListOnline picks the online-poll request type, preferring
// QueryOnlineStreamers when both are set; panics if neither is set.
func (c Capabilities) UsesFixedListOnline() bool {
	if c.SupportsQueryOnlineStreamers {
		return false
	}
	if c.SupportsQueryFixedListOnlineStreamers {
		return true
	}
	panic("checker capabilities: at least one of QueryOnlineStreamers / QueryFixedListOnlineStreamers must be true")
}

// Checker is the interface for a per-site checker.
// Init must be called before any Query* method;
// Site() and Capabilities() work on an uninitialised checker.
type Checker interface {
	QueryStatus(nickname string) (cmdlib.StreamerInfoWithStatus, error)
	QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error)
	QueryFixedListOnlineStreamers(streamers []string, checkMode cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error)
	QueryFixedListStatuses(streamers []string, checkMode cmdlib.CheckMode) (map[string]cmdlib.StreamerInfoWithStatus, error)
	Capabilities() Capabilities
	Config() CheckerConfig
	Site() string
	Init(checkerCfgPath string) error
	PushStatusRequest(request cmdlib.StatusRequest) error
	StatusRequestsQueue() <-chan cmdlib.StatusRequest
	NicknamePreprocessing(name string) string
	NicknameRegexp() *regexp.Regexp
}

// BaseChecker holds the runtime state shared by every site checker.
// Cfg is the typed per-site config; site code reads c.Cfg.Field directly.
type BaseChecker[T CheckerConfig] struct {
	Cfg            T
	Client         *http.Client
	statusRequests chan cmdlib.StatusRequest
}

// ErrFullQueue emerges whenever we unable to add a request because the queue is full
var ErrFullQueue = errors.New("queue is full")

// ErrNotImplemented emerges when a method is not implemented
var ErrNotImplemented = errors.New("not implemented")

// ensureUninitialised returns an error if Init has already been called.
// Per-site Init methods call it first to fail fast on a double-Init.
func (c *BaseChecker[T]) ensureUninitialised() error {
	if c.statusRequests != nil {
		return errors.New("checker already initialised")
	}
	return nil
}

// PushStatusRequest adds a status request to the queue
func (c *BaseChecker[T]) PushStatusRequest(request cmdlib.StatusRequest) error {
	select {
	case c.statusRequests <- request:
		return nil
	default:
		return ErrFullQueue
	}
}

// QueryFixedListStatuses returns ErrNotImplemented by default.
// Checkers that support querying streamer existence should override this.
func (c *BaseChecker[T]) QueryFixedListStatuses(_ []string, _ cmdlib.CheckMode) (map[string]cmdlib.StreamerInfoWithStatus, error) {
	return nil, ErrNotImplemented
}

// Config returns the per-site checker config. Concrete underlying
// type is the site's XCheckerConfig — json.Marshal sees it fully.
func (c *BaseChecker[T]) Config() CheckerConfig { return c.Cfg }

// NewBaseChecker builds a BaseChecker for a per-site checker.
func NewBaseChecker[T CheckerConfig](cfg T) BaseChecker[T] {
	return BaseChecker[T]{
		Cfg:            cfg,
		Client:         cmdlib.HTTPClientWithTimeout(cfg.Timeout()),
		statusRequests: make(chan cmdlib.StatusRequest, cfg.QueueSize()),
	}
}

// StatusRequestsQueue returns the receive end of the status-request
// channel. Producers must go through PushStatusRequest.
func (c *BaseChecker[T]) StatusRequestsQueue() <-chan cmdlib.StatusRequest {
	return c.statusRequests
}

// NicknamePreprocessing preprocesses nickname to canonical form
func (c *BaseChecker[T]) NicknamePreprocessing(name string) string {
	return cmdlib.CanonicalNicknamePreprocessing(name)
}

// NicknameRegexp returns the regular expression to validate nicknames
func (c *BaseChecker[T]) NicknameRegexp() *regexp.Regexp {
	return cmdlib.CommonNicknameRegexp
}

// sleepCtx blocks for d or until ctx is cancelled.
// Returns false if cancelled, true if the full duration elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d == 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// StartCheckerDaemon starts a checker daemon.
// The goroutine exits when ctx is cancelled.
func StartCheckerDaemon(ctx context.Context, checker Checker) {
	interval := checker.Config().MinRequestInterval()
	queue := checker.StatusRequestsQueue()
	go func() {
		for {
			var request cmdlib.StatusRequest
			select {
			case <-ctx.Done():
				return
			case request = <-queue:
			}
			start := time.Now()

			// The failed-result cases send a failed result and break out
			// of the switch (not continue) so the rate-limit sleep below
			// still runs before the next request.
			switch req := request.(type) {
			case *cmdlib.OnlineListRequest:
				onlineStreamers, err := checker.QueryOnlineStreamers()
				if err != nil {
					cmdlib.Lerr("%v", err)
					req.ResultsCh <- cmdlib.NewOnlineListResultsFailed()
					break
				}
				delete(onlineStreamers, "")
				var pollErrors []string
				for _, nickname := range req.Poll {
					if _, inBulk := onlineStreamers[nickname]; inBulk {
						continue
					}
					if !sleepCtx(ctx, interval) {
						return
					}
					info, err := checker.QueryStatus(nickname)
					if err != nil {
						cmdlib.Lerr("%v", err)
					}
					if err != nil || info.Status == cmdlib.StatusUnknown {
						pollErrors = append(pollErrors, nickname)
						continue
					}
					if info.Status == cmdlib.StatusOnline {
						onlineStreamers[nickname] = info.StreamerInfo
					}
				}
				elapsed := time.Since(start)
				cmdlib.Ldbg("got statuses: %d", len(onlineStreamers))
				result := cmdlib.NewOnlineListResults(onlineStreamers, elapsed)
				result.PollCount = len(req.Poll)
				result.PollErrors = pollErrors
				req.ResultsCh <- result
			case *cmdlib.FixedListOnlineRequest:
				streamers, err := checker.QueryFixedListOnlineStreamers(setToSlice(req.Streamers), cmdlib.CheckOnline)
				if err != nil {
					cmdlib.Lerr("%v", err)
					req.ResultsCh <- cmdlib.NewFixedListOnlineResultsFailed()
					break
				}
				delete(streamers, "")
				filtered := make(map[string]cmdlib.StreamerInfo, len(req.Streamers))
				for nickname := range req.Streamers {
					if info, ok := streamers[nickname]; ok {
						filtered[nickname] = info
					}
				}
				elapsed := time.Since(start)
				cmdlib.Ldbg("got statuses: upstream = %d, returned = %d", len(streamers), len(filtered))
				req.ResultsCh <- cmdlib.NewFixedListOnlineResults(req.Streamers, filtered, elapsed)
			case *cmdlib.FixedListStatusRequest:
				streamers, err := checker.QueryFixedListStatuses(setToSlice(req.Streamers), cmdlib.CheckStatuses)
				if err != nil {
					cmdlib.Lerr("%v", err)
					req.ResultsCh <- cmdlib.NewExistenceListResultsFailed()
					break
				}
				delete(streamers, "")
				filtered := make(map[string]cmdlib.StreamerInfoWithStatus, len(req.Streamers))
				for nickname := range req.Streamers {
					if info, ok := streamers[nickname]; ok {
						filtered[nickname] = info
					}
				}
				elapsed := time.Since(start)
				cmdlib.Ldbg("got statuses: upstream = %d, returned = %d", len(streamers), len(filtered))
				req.ResultsCh <- cmdlib.NewExistenceListResults(filtered, elapsed)
			case *cmdlib.SingleStatusRequest:
				info, err := checker.QueryStatus(req.Streamer)
				if err != nil {
					cmdlib.Lerr("%v", err)
					info = cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}
				}
				elapsed := time.Since(start)
				cmdlib.Ldbg("got status: %s = %s", req.Streamer, info.Status)
				req.ResultsCh <- cmdlib.NewExistenceListResults(
					map[string]cmdlib.StreamerInfoWithStatus{req.Streamer: info},
					elapsed,
				)
			}
			if !sleepCtx(ctx, interval) {
				return
			}
		}
	}()
}

// DoGetRequest performs a GET request with the given headers.
func (c *BaseChecker[T]) DoGetRequest(url string, headers [][2]string) *http.Response {
	req, err := http.NewRequest("GET", url, nil)
	cmdlib.CheckErr(err)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		cmdlib.Lerr("cannot send a query, %v", err)
		return nil
	}
	cmdlib.Ldbg("query status for %s: %d", url, resp.StatusCode)
	return resp
}

// QueryStatusCode performs a GET request and returns only the status code.
func (c *BaseChecker[T]) QueryStatusCode(url string, headers [][2]string) int {
	resp := c.DoGetRequest(url, headers)
	if resp == nil {
		return -1
	}
	cmdlib.CloseBody(resp.Body)
	return resp.StatusCode
}

func setToSlice(xs map[string]bool) []string {
	if xs == nil {
		return nil
	}
	result := make([]string, len(xs))
	i := 0
	for k := range xs {
		result[i] = k
		i++
	}
	return result
}
