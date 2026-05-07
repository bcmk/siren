package cmdlib

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"time"
)

// StatusRequest is the interface for status requests
type StatusRequest interface {
	isStatusRequest()
}

// CheckerResults is the interface for status results
type CheckerResults interface {
	isCheckerResults()
	Duration() time.Duration
	Failed() bool
	Count() int
	ExtraLogFields() map[string]any
}

// OnlineListRequest requests statuses for all online streamers.
// Names in Poll fall back to QueryStatus when not in the bulk result.
type OnlineListRequest struct {
	Poll      []string
	ResultsCh chan<- CheckerResults
}

func (r *OnlineListRequest) isStatusRequest() {}

// OnlineListResults contains results for OnlineListRequest
type OnlineListResults struct {
	Streamers  map[string]StreamerInfo
	PollCount  int
	PollErrors []string
	duration   time.Duration
	failed     bool
}

func (r *OnlineListResults) isCheckerResults() {}

// MarshalJSON serialises the result so consumers (e.g. adapter-mfc)
// can return it over HTTP without exposing the unexported fields.
func (r *OnlineListResults) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Streamers  map[string]StreamerInfo `json:"streamers"`
		PollCount  int                     `json:"poll_count,omitempty"`
		PollErrors []string                `json:"poll_errors,omitempty"`
		Duration   time.Duration           `json:"duration"`
		Failed     bool                    `json:"failed"`
	}{r.Streamers, r.PollCount, r.PollErrors, r.duration, r.failed})
}

// Duration returns the elapsed time for the request.
func (r *OnlineListResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *OnlineListResults) Failed() bool { return r.failed }

// Count returns the number of streamers in the result.
func (r *OnlineListResults) Count() int { return len(r.Streamers) }

// ExtraLogFields returns extra fields for performance logging.
func (r *OnlineListResults) ExtraLogFields() map[string]any {
	return map[string]any{
		"poll_count":  r.PollCount,
		"poll_errors": len(r.PollErrors),
	}
}

// NewOnlineListResults creates a successful OnlineListResults.
func NewOnlineListResults(streamers map[string]StreamerInfo, duration time.Duration) *OnlineListResults {
	return &OnlineListResults{Streamers: streamers, duration: duration}
}

// NewOnlineListResultsFailed creates a failed OnlineListResults.
func NewOnlineListResultsFailed() *OnlineListResults {
	return &OnlineListResults{failed: true}
}

// FixedListOnlineRequest requests statuses for specific streamers
type FixedListOnlineRequest struct {
	Streamers map[string]bool
	ResultsCh chan<- CheckerResults
}

func (r *FixedListOnlineRequest) isStatusRequest() {}

// FixedListOnlineResults contains results for FixedListOnlineRequest
type FixedListOnlineResults struct {
	RequestedStreamers map[string]bool
	Streamers          map[string]StreamerInfo
	duration           time.Duration
	failed             bool
}

func (r *FixedListOnlineResults) isCheckerResults() {}

// Duration returns the elapsed time for the request.
func (r *FixedListOnlineResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *FixedListOnlineResults) Failed() bool { return r.failed }

// Count returns the number of streamers in the result.
func (r *FixedListOnlineResults) Count() int { return len(r.Streamers) }

// ExtraLogFields returns extra fields for performance logging.
func (r *FixedListOnlineResults) ExtraLogFields() map[string]any { return nil }

// NewFixedListOnlineResults creates a successful FixedListOnlineResults.
func NewFixedListOnlineResults(
	requestedStreamers map[string]bool,
	streamers map[string]StreamerInfo,
	duration time.Duration,
) *FixedListOnlineResults {
	return &FixedListOnlineResults{
		RequestedStreamers: requestedStreamers,
		Streamers:          streamers,
		duration:           duration,
	}
}

// NewFixedListOnlineResultsFailed creates a failed FixedListOnlineResults.
func NewFixedListOnlineResultsFailed() *FixedListOnlineResults {
	return &FixedListOnlineResults{failed: true}
}

// FixedListStatusRequest checks if specific streamers exist
type FixedListStatusRequest struct {
	Streamers map[string]bool
	ResultsCh chan<- *ExistenceListResults
}

func (r *FixedListStatusRequest) isStatusRequest() {}

// SingleStatusRequest is FixedListStatusRequest dispatched via QueryStatus
// for one streamer; used when the checker has no batched surface.
type SingleStatusRequest struct {
	Streamer  string
	ResultsCh chan<- *ExistenceListResults
}

func (r *SingleStatusRequest) isStatusRequest() {}

// ExistenceListResults contains results for ExistenceListRequest
type ExistenceListResults struct {
	Streamers map[string]StreamerInfoWithStatus
	duration  time.Duration
	failed    bool
}

func (r *ExistenceListResults) isCheckerResults() {}

// Duration returns the elapsed time for the request.
func (r *ExistenceListResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *ExistenceListResults) Failed() bool { return r.failed }

// Count returns the number of streamers in the result.
func (r *ExistenceListResults) Count() int { return len(r.Streamers) }

// ExtraLogFields returns extra fields for performance logging.
func (r *ExistenceListResults) ExtraLogFields() map[string]any { return nil }

// NewExistenceListResults creates a successful ExistenceListResults.
func NewExistenceListResults(
	streamers map[string]StreamerInfoWithStatus,
	duration time.Duration,
) *ExistenceListResults {
	return &ExistenceListResults{Streamers: streamers, duration: duration}
}

// NewExistenceListResultsFailed creates a failed ExistenceListResults.
func NewExistenceListResultsFailed() *ExistenceListResults {
	return &ExistenceListResults{failed: true}
}

// ShowKind represents the kind of show
type ShowKind int

const (
	// ShowUnknown means the show kind is unknown
	ShowUnknown ShowKind = 0
	// ShowPublic means the show is public
	ShowPublic ShowKind = 1
	// ShowGroup means the show is a group show
	ShowGroup ShowKind = 2
	// ShowTicket means the show is a ticket show
	ShowTicket ShowKind = 3
	// ShowHidden means the show is hidden
	ShowHidden ShowKind = 4
	// ShowPrivate means the show is private
	ShowPrivate ShowKind = 5
	// ShowAway means the model is away
	ShowAway ShowKind = 6
)

// String returns the symbolic name of the show kind.
func (s ShowKind) String() string {
	switch s {
	case ShowUnknown:
		return "unknown"
	case ShowPublic:
		return "public"
	case ShowGroup:
		return "group"
	case ShowTicket:
		return "ticket"
	case ShowHidden:
		return "hidden"
	case ShowPrivate:
		return "private"
	case ShowAway:
		return "away"
	}
	return fmt.Sprintf("ShowKind(%d)", int(s))
}

// StreamerInfo carries presentation data for a streamer: cover image,
// viewer count, current show kind, and the room subject when supported.
type StreamerInfo struct {
	ImageURL string   `json:"image_url,omitempty"`
	Viewers  *int     `json:"viewers,omitempty"`
	ShowKind ShowKind `json:"show_kind,omitempty"`
	Subject  string   `json:"subject,omitempty"`
}

// StreamerInfoWithStatus is StreamerInfo plus a StatusKind verdict.
type StreamerInfoWithStatus struct {
	StreamerInfo
	Status StatusKind `json:"status"`
}

// StatusUpdate represents an update of streamer status
type StatusUpdate struct {
	Nickname string
	Status   StatusKind
}

// CheckerConfig represents checker config
type CheckerConfig struct {
	UsersOnlineEndpoints []string
	Clients              []*Client
	Headers              [][2]string
	Dbg                  bool
	SpecificConfig       map[string]Secret
	QueueSize            int
	MinRequestIntervalMs int
}

// Capabilities reports which optional checker entry points are
// implemented (i.e. don't return ErrNotImplemented).
type Capabilities struct {
	QueryOnlineStreamers          bool
	QueryFixedListOnlineStreamers bool
	QueryFixedListStatuses        bool
	QueryStatus                   bool
}

// UsesFixedListOnline picks the online-poll request type, preferring
// QueryOnlineStreamers when both are set; panics if neither is set.
func (c Capabilities) UsesFixedListOnline() bool {
	if c.QueryOnlineStreamers {
		return false
	}
	if c.QueryFixedListOnlineStreamers {
		return true
	}
	panic("checker capabilities: at least one of QueryOnlineStreamers / QueryFixedListOnlineStreamers must be true")
}

// Checker is the interface for a checker for specific site
type Checker interface {
	QueryStatus(nickname string) (StreamerInfoWithStatus, error)
	QueryOnlineStreamers() (map[string]StreamerInfo, error)
	QueryFixedListOnlineStreamers(streamers []string, checkMode CheckMode) (map[string]StreamerInfo, error)
	QueryFixedListStatuses(streamers []string, checkMode CheckMode) (map[string]StreamerInfoWithStatus, error)
	Capabilities() Capabilities
	Init(config CheckerConfig)
	PushStatusRequest(request StatusRequest) error
	StatusRequestsQueue() chan StatusRequest
	MinRequestInterval() time.Duration
	Debug() bool
	SubjectSupported() bool
	NicknamePreprocessing(name string) string
	NicknameRegexp() *regexp.Regexp
}

// CheckerCommon contains common fields for all the checkers
type CheckerCommon struct {
	CheckerConfig
	ClientsLoop    clientsLoop
	statusRequests chan StatusRequest
}

// ErrFullQueue emerges whenever we unable to add a request because the queue is full
var ErrFullQueue = errors.New("queue is full")

// ErrNotImplemented emerges when a method is not implemented
var ErrNotImplemented = errors.New("not implemented")

// PushStatusRequest adds a status request to the queue
func (c *CheckerCommon) PushStatusRequest(request StatusRequest) error {
	select {
	case c.statusRequests <- request:
		return nil
	default:
		return ErrFullQueue
	}
}

// QueryFixedListStatuses returns ErrNotImplemented by default.
// Checkers that support querying streamer existence should override this.
func (c *CheckerCommon) QueryFixedListStatuses(_ []string, _ CheckMode) (map[string]StreamerInfoWithStatus, error) {
	return nil, ErrNotImplemented
}

// Init initializes checker common fields
func (c *CheckerCommon) Init(config CheckerConfig) {
	c.UsersOnlineEndpoints = config.UsersOnlineEndpoints
	c.Headers = config.Headers
	c.Dbg = config.Dbg
	c.SpecificConfig = config.SpecificConfig
	c.QueueSize = config.QueueSize
	c.MinRequestIntervalMs = config.MinRequestIntervalMs
	c.ClientsLoop = clientsLoop{clients: config.Clients}
	c.statusRequests = make(chan StatusRequest, config.QueueSize)
}

// StatusRequestsQueue returns the channel for status requests
func (c *CheckerCommon) StatusRequestsQueue() chan StatusRequest { return c.statusRequests }

// MinRequestInterval returns the interval between requests
func (c *CheckerCommon) MinRequestInterval() time.Duration {
	return time.Duration(c.MinRequestIntervalMs) * time.Millisecond
}

// Debug returns whether debug mode is enabled
func (c *CheckerCommon) Debug() bool { return c.Dbg }

// SubjectSupported returns whether the checker supports room subjects
func (c *CheckerCommon) SubjectSupported() bool { return false }

// NicknamePreprocessing preprocesses nickname to canonical form
func (c *CheckerCommon) NicknamePreprocessing(name string) string {
	return CanonicalNicknamePreprocessing(name)
}

// NicknameRegexp returns the regular expression to validate nicknames
func (c *CheckerCommon) NicknameRegexp() *regexp.Regexp {
	return CommonNicknameRegexp
}

// StartCheckerDaemon starts a checker daemon
func StartCheckerDaemon(checker Checker) {
	go func() {
		for request := range checker.StatusRequestsQueue() {
			start := time.Now()

			switch req := request.(type) {
			case *OnlineListRequest:
				onlineStreamers, err := checker.QueryOnlineStreamers()
				if err != nil {
					Lerr("%v", err)
					req.ResultsCh <- NewOnlineListResultsFailed()
					continue
				}
				delete(onlineStreamers, "")
				var pollErrors []string
				for _, nickname := range req.Poll {
					if _, inBulk := onlineStreamers[nickname]; inBulk {
						continue
					}
					time.Sleep(checker.MinRequestInterval())
					info, err := checker.QueryStatus(nickname)
					if err != nil {
						Lerr("%v", err)
					}
					if err != nil || info.Status == StatusUnknown {
						pollErrors = append(pollErrors, nickname)
						continue
					}
					if info.Status == StatusOnline {
						onlineStreamers[nickname] = info.StreamerInfo
					}
				}
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(onlineStreamers))
				}
				result := NewOnlineListResults(onlineStreamers, elapsed)
				result.PollCount = len(req.Poll)
				result.PollErrors = pollErrors
				req.ResultsCh <- result
			case *FixedListOnlineRequest:
				streamers, err := checker.QueryFixedListOnlineStreamers(setToSlice(req.Streamers), CheckOnline)
				if err != nil {
					Lerr("%v", err)
					req.ResultsCh <- NewFixedListOnlineResultsFailed()
					continue
				}
				delete(streamers, "")
				filtered := make(map[string]StreamerInfo, len(req.Streamers))
				for nickname := range req.Streamers {
					if info, ok := streamers[nickname]; ok {
						filtered[nickname] = info
					}
				}
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(streamers))
				}
				req.ResultsCh <- NewFixedListOnlineResults(req.Streamers, filtered, elapsed)
			case *FixedListStatusRequest:
				streamers, err := checker.QueryFixedListStatuses(setToSlice(req.Streamers), CheckStatuses)
				if err != nil {
					Lerr("%v", err)
					req.ResultsCh <- NewExistenceListResultsFailed()
					continue
				}
				delete(streamers, "")
				filtered := make(map[string]StreamerInfoWithStatus, len(req.Streamers))
				for nickname := range req.Streamers {
					if info, ok := streamers[nickname]; ok {
						filtered[nickname] = info
					}
				}
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(streamers))
				}
				req.ResultsCh <- NewExistenceListResults(filtered, elapsed)
			case *SingleStatusRequest:
				info, err := checker.QueryStatus(req.Streamer)
				if err != nil {
					Lerr("%v", err)
					info = StreamerInfoWithStatus{Status: StatusUnknown}
				}
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got status: %s = %s", req.Streamer, info.Status)
				}
				req.ResultsCh <- NewExistenceListResults(
					map[string]StreamerInfoWithStatus{req.Streamer: info},
					elapsed,
				)
			}
			time.Sleep(checker.MinRequestInterval())
		}
	}()
}

// DoGetRequest performs a GET request respecting the configuration
func (c *CheckerCommon) DoGetRequest(url string) (net.Addr, *http.Response) {
	client := c.ClientsLoop.NextClient()
	req, err := http.NewRequest("GET", url, nil)
	CheckErr(err)
	for _, h := range c.Headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := client.Client.Do(req)
	if err != nil {
		Lerr("[%v] cannot send a query, %v", client.Addr, err)
		return client.Addr, nil
	}
	if c.Dbg {
		Ldbg("[%v] query status for %s: %d", client.Addr, url, resp.StatusCode)
	}
	return client.Addr, resp
}

// QueryStatusCode performs a GET request and returns only the status code
func (c *CheckerCommon) QueryStatusCode(url string) int {
	_, resp := c.DoGetRequest(url)
	if resp == nil {
		return -1
	}
	CloseBody(resp.Body)
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
