package cmdlib

import (
	"errors"
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
}

// OnlineListRequest requests statuses for all online streamers
type OnlineListRequest struct {
	ResultsCh chan<- CheckerResults
}

func (r *OnlineListRequest) isStatusRequest() {}

// OnlineListResults contains results for OnlineListRequest
type OnlineListResults struct {
	Streamers map[string]StreamerInfo
	duration  time.Duration
	failed    bool
}

func (r *OnlineListResults) isCheckerResults() {}

// Duration returns the elapsed time for the request.
func (r *OnlineListResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *OnlineListResults) Failed() bool { return r.failed }

// Count returns the number of streamers in the result.
func (r *OnlineListResults) Count() int { return len(r.Streamers) }

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

// StreamerInfo contains image URL for a streamer
type StreamerInfo struct {
	ImageURL string
	Viewers  *int
	ShowKind ShowKind
	Subject  string
}

// StreamerInfoWithStatus contains status and image URL for a streamer
type StreamerInfoWithStatus struct {
	Status   StatusKind
	ImageURL string
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
	IntervalMs           int
}

// Checker is the interface for a checker for specific site
type Checker interface {
	CheckStatusSingle(nickname string) StatusKind
	QueryOnlineStreamers() (map[string]StreamerInfo, error)
	QueryFixedListOnlineStreamers(streamers []string, checkMode CheckMode) (map[string]StreamerInfo, error)
	QueryFixedListStatuses(streamers []string, checkMode CheckMode) (map[string]StreamerInfoWithStatus, error)
	Init(config CheckerConfig)
	PushStatusRequest(request StatusRequest) error
	UsesFixedList() bool
	StatusRequestsQueue() chan StatusRequest
	RequestInterval() time.Duration
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
	c.IntervalMs = config.IntervalMs
	c.ClientsLoop = clientsLoop{clients: config.Clients}
	c.statusRequests = make(chan StatusRequest, config.QueueSize)
}

// StatusRequestsQueue returns the channel for status requests
func (c *CheckerCommon) StatusRequestsQueue() chan StatusRequest { return c.statusRequests }

// RequestInterval returns the interval between requests
func (c *CheckerCommon) RequestInterval() time.Duration {
	return time.Duration(c.IntervalMs) * time.Millisecond
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
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(onlineStreamers))
				}
				req.ResultsCh <- NewOnlineListResults(onlineStreamers, elapsed)
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
				if errors.Is(err, ErrNotImplemented) {
					// Checker does not support status queries — deny all as unknown
					streamers = make(map[string]StreamerInfoWithStatus, len(req.Streamers))
					for nickname := range req.Streamers {
						streamers[nickname] = StreamerInfoWithStatus{Status: StatusUnknown}
					}
				} else if err != nil {
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
			}
			time.Sleep(checker.RequestInterval())
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
