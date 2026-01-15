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
	ResultElapsed() time.Duration
	ResultError() bool
}

// OnlineListRequest requests statuses for all online channels
type OnlineListRequest struct {
	ResultsCh chan<- CheckerResults
}

func (r *OnlineListRequest) isStatusRequest() {}

// OnlineListResults contains results for OnlineListRequest
type OnlineListResults struct {
	Channels map[string]ChannelInfo
	Elapsed  time.Duration
	Error    bool
}

func (r OnlineListResults) isCheckerResults()            {}
func (r OnlineListResults) ResultElapsed() time.Duration { return r.Elapsed } //nolint:revive
func (r OnlineListResults) ResultError() bool            { return r.Error }   //nolint:revive

// FixedListRequest requests statuses for specific channels
type FixedListRequest struct {
	Channels  map[string]bool
	ResultsCh chan<- CheckerResults
}

func (r *FixedListRequest) isStatusRequest() {}

// FixedListResults contains results for FixedListRequest
type FixedListResults struct {
	RequestedChannels map[string]bool
	Channels          map[string]ChannelInfoWithStatus
	Elapsed           time.Duration
	Error             bool
}

func (r FixedListResults) isCheckerResults()            {}
func (r FixedListResults) ResultElapsed() time.Duration { return r.Elapsed } //nolint:revive
func (r FixedListResults) ResultError() bool            { return r.Error }   //nolint:revive

// ExistenceListRequest checks if specific channels exist
type ExistenceListRequest struct {
	Channels  map[string]bool
	ResultsCh chan<- ExistenceListResults
}

func (r *ExistenceListRequest) isStatusRequest() {}

// ExistenceListResults contains results for ExistenceListRequest
type ExistenceListResults struct {
	Channels map[string]ChannelInfoWithStatus
	Elapsed  time.Duration
	Error    bool
}

// ChannelInfo contains image URL for a channel
type ChannelInfo struct {
	ImageURL string
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
	QueryChannelListStatuses(channels []string, checkMode CheckMode) (map[string]ChannelInfoWithStatus, error)
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
					req.ResultsCh <- OnlineListResults{Error: true}
					continue
				}
				time.Sleep(checker.RequestInterval())
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(onlineChannels))
				}
				req.ResultsCh <- OnlineListResults{Channels: onlineChannels, Elapsed: elapsed}
			case *FixedListRequest:
				channels, err := queryChannelList(checker, req.Channels, CheckOnline)
				if err != nil {
					Lerr("%v", err)
					req.ResultsCh <- FixedListResults{Error: true}
					continue
				}
				time.Sleep(checker.RequestInterval())
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(channels))
				}
				req.ResultsCh <- FixedListResults{
					RequestedChannels: req.Channels,
					Channels:          channels,
					Elapsed:           elapsed,
				}
			case *ExistenceListRequest:
				channels, err := queryChannelList(checker, req.Channels, CheckStatuses)
				if err != nil {
					Lerr("%v", err)
					req.ResultsCh <- ExistenceListResults{Error: true}
					continue
				}
				time.Sleep(checker.RequestInterval())
				elapsed := time.Since(start)
				if checker.Debug() {
					Ldbg("got statuses: %d", len(channels))
				}
				req.ResultsCh <- ExistenceListResults{Channels: channels, Elapsed: elapsed}
			}
		}
	}()
}

func toChannelInfoWithStatus(channels map[string]ChannelInfo, status StatusKind) map[string]ChannelInfoWithStatus {
	result := make(map[string]ChannelInfoWithStatus, len(channels))
	for k, v := range channels {
		result[k] = ChannelInfoWithStatus{Status: status, ImageURL: v.ImageURL}
	}
	return result
}

func queryChannelList(
	checker Checker,
	requestChannels map[string]bool,
	checkMode CheckMode,
) (map[string]ChannelInfoWithStatus, error) {
	channels, err := checker.QueryChannelListStatuses(setToSlice(requestChannels), checkMode)
	if errors.Is(err, ErrNotImplemented) {
		onlineChannels, onlineErr := checker.QueryOnlineChannels()
		if onlineErr != nil {
			return nil, onlineErr
		}
		channels = toChannelInfoWithStatus(onlineChannels, StatusOnline)
		err = nil
	}
	if err != nil {
		return nil, err
	}
	filtered := make(map[string]ChannelInfoWithStatus, len(requestChannels))
	for channelID := range requestChannels {
		if info, ok := channels[channelID]; ok {
			filtered[channelID] = info
		} else {
			filtered[channelID] = ChannelInfoWithStatus{Status: StatusUnknown}
		}
	}
	return filtered, nil
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
