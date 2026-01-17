package cmdlib

import (
	"errors"
	"net"
	"net/http"
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

// OnlineListRequest requests statuses for all online channels
type OnlineListRequest struct {
	ResultsCh chan<- CheckerResults
}

func (r *OnlineListRequest) isStatusRequest() {}

// OnlineListResults contains results for OnlineListRequest
type OnlineListResults struct {
	Channels map[string]ChannelInfo
	duration time.Duration
	failed   bool
}

func (r *OnlineListResults) isCheckerResults() {}

// Duration returns the elapsed time for the request.
func (r *OnlineListResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *OnlineListResults) Failed() bool { return r.failed }

// Count returns the number of channels in the result.
func (r *OnlineListResults) Count() int { return len(r.Channels) }

// NewOnlineListResults creates a successful OnlineListResults.
func NewOnlineListResults(channels map[string]ChannelInfo, duration time.Duration) *OnlineListResults {
	return &OnlineListResults{Channels: channels, duration: duration}
}

// NewOnlineListResultsFailed creates a failed OnlineListResults.
func NewOnlineListResultsFailed() *OnlineListResults {
	return &OnlineListResults{failed: true}
}

// FixedListOnlineRequest requests statuses for specific channels
type FixedListOnlineRequest struct {
	Channels  map[string]bool
	ResultsCh chan<- CheckerResults
}

func (r *FixedListOnlineRequest) isStatusRequest() {}

// FixedListOnlineResults contains results for FixedListOnlineRequest
type FixedListOnlineResults struct {
	RequestedChannels map[string]bool
	Channels          map[string]ChannelInfo
	duration          time.Duration
	failed            bool
}

func (r *FixedListOnlineResults) isCheckerResults() {}

// Duration returns the elapsed time for the request.
func (r *FixedListOnlineResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *FixedListOnlineResults) Failed() bool { return r.failed }

// Count returns the number of channels in the result.
func (r *FixedListOnlineResults) Count() int { return len(r.Channels) }

// NewFixedListOnlineResults creates a successful FixedListOnlineResults.
func NewFixedListOnlineResults(
	requestedChannels map[string]bool,
	channels map[string]ChannelInfo,
	duration time.Duration,
) *FixedListOnlineResults {
	return &FixedListOnlineResults{
		RequestedChannels: requestedChannels,
		Channels:          channels,
		duration:          duration,
	}
}

// NewFixedListOnlineResultsFailed creates a failed FixedListOnlineResults.
func NewFixedListOnlineResultsFailed() *FixedListOnlineResults {
	return &FixedListOnlineResults{failed: true}
}

// FixedListStatusRequest checks if specific channels exist
type FixedListStatusRequest struct {
	Channels  map[string]bool
	ResultsCh chan<- *ExistenceListResults
}

func (r *FixedListStatusRequest) isStatusRequest() {}

// ExistenceListResults contains results for ExistenceListRequest
type ExistenceListResults struct {
	Channels map[string]ChannelInfoWithStatus
	duration time.Duration
	failed   bool
}

func (r *ExistenceListResults) isCheckerResults() {}

// Duration returns the elapsed time for the request.
func (r *ExistenceListResults) Duration() time.Duration { return r.duration }

// Failed returns whether the request failed.
func (r *ExistenceListResults) Failed() bool { return r.failed }

// Count returns the number of channels in the result.
func (r *ExistenceListResults) Count() int { return len(r.Channels) }

// NewExistenceListResults creates a successful ExistenceListResults.
func NewExistenceListResults(
	channels map[string]ChannelInfoWithStatus,
	duration time.Duration,
) *ExistenceListResults {
	return &ExistenceListResults{Channels: channels, duration: duration}
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
	// ShowHidden means the show is hidden
	ShowHidden ShowKind = 3
	// ShowPrivate means the show is private
	ShowPrivate ShowKind = 4
	// ShowAway means the model is away
	ShowAway ShowKind = 5
)

// ChannelInfo contains image URL for a channel
type ChannelInfo struct {
	ImageURL string
	Viewers  *int
	ShowKind ShowKind
}

// ChannelInfoWithStatus contains status and image URL for a channel
type ChannelInfoWithStatus struct {
	Status   StatusKind
	ImageURL string
}

// StatusUpdate represents an update of channel status
type StatusUpdate struct {
	ChannelID string
	Status    StatusKind
}

// CheckerConfig represents checker config
type CheckerConfig struct {
	UsersOnlineEndpoints []string
	Clients              []*Client
	Headers              [][2]string
	Dbg                  bool
	SpecificConfig       map[string]string
	QueueSize            int
	IntervalMs           int
}

// Checker is the interface for a checker for specific site
type Checker interface {
	CheckStatusSingle(channelID string) StatusKind
	QueryOnlineChannels() (map[string]ChannelInfo, error)
	QueryFixedListOnlineChannels(channels []string, checkMode CheckMode) (map[string]ChannelInfo, error)
	QueryFixedListStatuses(channels []string, checkMode CheckMode) (map[string]ChannelInfoWithStatus, error)
	Init(config CheckerConfig)
	PushStatusRequest(request StatusRequest) error
	UsesFixedList() bool
	StatusRequestsQueue() chan StatusRequest
	RequestInterval() time.Duration
	Debug() bool
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
// Checkers that support querying channel existence should override this.
func (c *CheckerCommon) QueryFixedListStatuses(_ []string, _ CheckMode) (map[string]ChannelInfoWithStatus, error) {
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

// StartCheckerDaemon starts a checker daemon
func StartCheckerDaemon(checker Checker) {
	go func() {
		for request := range checker.StatusRequestsQueue() {
			start := time.Now()

			switch req := request.(type) {
			case *OnlineListRequest:
				onlineChannels, err := checker.QueryOnlineChannels()
				if err != nil {
					Lerr("%v", err)
					req.ResultsCh <- NewOnlineListResultsFailed()
					continue
				}
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(onlineChannels))
				}
				req.ResultsCh <- NewOnlineListResults(onlineChannels, elapsed)
			case *FixedListOnlineRequest:
				channels, err := checker.QueryFixedListOnlineChannels(setToSlice(req.Channels), CheckOnline)
				if err != nil {
					Lerr("%v", err)
					req.ResultsCh <- NewFixedListOnlineResultsFailed()
					continue
				}
				filtered := make(map[string]ChannelInfo, len(req.Channels))
				for channelID := range req.Channels {
					if info, ok := channels[channelID]; ok {
						filtered[channelID] = info
					}
				}
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(channels))
				}
				req.ResultsCh <- NewFixedListOnlineResults(req.Channels, filtered, elapsed)
			case *FixedListStatusRequest:
				channels, err := checker.QueryFixedListStatuses(setToSlice(req.Channels), CheckStatuses)
				if errors.Is(err, ErrNotImplemented) {
					// Checker does not support status queries — deny all as unknown
					channels = make(map[string]ChannelInfoWithStatus, len(req.Channels))
					for channelID := range req.Channels {
						channels[channelID] = ChannelInfoWithStatus{Status: StatusUnknown}
					}
				} else if err != nil {
					Lerr("%v", err)
					req.ResultsCh <- NewExistenceListResultsFailed()
					continue
				}
				filtered := make(map[string]ChannelInfoWithStatus, len(req.Channels))
				for channelID := range req.Channels {
					if info, ok := channels[channelID]; ok {
						filtered[channelID] = info
					}
				}
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(channels))
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
