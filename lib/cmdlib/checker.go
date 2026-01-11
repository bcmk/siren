package cmdlib

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// StatusRequest represents a request for channel statuses
type StatusRequest struct {
	Channels  map[string]bool // nil for all online channels, non-nil for specific channels
	CheckMode CheckMode
	ResultsCh chan<- StatusResults
}

// ChannelInfo contains status and image URL for a channel
type ChannelInfo struct {
	Status   StatusKind
	ImageURL string
}

// StatusResults contains results from querying channels
type StatusResults struct {
	Request  *StatusRequest
	Channels map[string]ChannelInfo
	Elapsed  time.Duration
	Error    bool
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
	QueryOnlineChannels(checkMode CheckMode) (map[string]ChannelInfo, error)
	QueryChannelListStatuses(channels []string, checkMode CheckMode) (map[string]ChannelInfo, error)
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
	requests:
		for request := range checker.StatusRequestsQueue() {
			start := time.Now()
			var channels map[string]ChannelInfo
			var err error
			if request.Channels == nil {
				channels, err = checker.QueryOnlineChannels(request.CheckMode)
			} else {
				channels, err = checker.QueryChannelListStatuses(
					setToSlice(request.Channels),
					request.CheckMode,
				)
				if errors.Is(err, ErrNotImplemented) {
					channels, err = checker.QueryOnlineChannels(request.CheckMode)
				}
				if err == nil {
					filtered := make(map[string]ChannelInfo, len(request.Channels))
					for channelID := range request.Channels {
						if info, ok := channels[channelID]; ok {
							filtered[channelID] = info
						} else {
							filtered[channelID] = ChannelInfo{Status: StatusUnknown}
						}
					}
					channels = filtered
				}
			}
			if err != nil {
				Lerr("%v", err)
				request.ResultsCh <- StatusResults{Request: &request, Error: true}
				continue requests
			}
			time.Sleep(checker.RequestInterval())
			elapsed := time.Since(start)
			if checker.Debug() {
				Ldbg("got statuses: %d", len(channels))
			}
			request.ResultsCh <- StatusResults{
				Request:  &request,
				Channels: channels,
				Elapsed:  elapsed,
			}
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
