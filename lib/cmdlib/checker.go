package cmdlib

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// StatusRequest represents a request for model statuses
type StatusRequest struct {
	Models    map[string]bool // nil for all online models, non-nil for specific models
	CheckMode CheckMode
	Callback  func(StatusResults)
}

// StatusResults contains results from querying models
type StatusResults struct {
	Request  *StatusRequest
	Statuses map[string]StatusKind
	Images   map[string]string
	Elapsed  time.Duration
	Error    bool
}

// StatusUpdate represents an update of model status
type StatusUpdate struct {
	ModelID string
	Status  StatusKind
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
	CheckStatusSingle(modelID string) StatusKind
	CheckStatusesMany(
		specific QueryModelList,
		checkMode CheckMode,
	) (statuses map[string]StatusKind, images map[string]string, err error)
	Start()
	Init(config CheckerConfig)
	PushStatusRequest(request StatusRequest) error
	UsesFixedList() bool
}

// CheckerCommon contains common fields for all the checkers
type CheckerCommon struct {
	CheckerConfig
	ClientsLoop    clientsLoop
	statusRequests chan StatusRequest
}

// QueryModelList represents a model list to query
type QueryModelList struct {
	All  bool
	List []string
}

// ErrFullQueue emerges whenever we unable to add a request because the queue is full
var ErrFullQueue = errors.New("queue is full")

// AllModels should be used to query all statuses
var AllModels = QueryModelList{All: true}

// NewQueryModelList returns a query list for specific models
func NewQueryModelList(list []string) QueryModelList { return QueryModelList{List: list} }

// PushStatusRequest adds a status request to the queue
func (c *CheckerCommon) PushStatusRequest(request StatusRequest) error {
	select {
	case c.statusRequests <- request:
		return nil
	default:
		return ErrFullQueue
	}
}

type endpointChecker interface {
	CheckEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error)
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

// CheckEndpoints performs queries to all available endpoints
func CheckEndpoints(
	c endpointChecker,
	endpoints []string,
	dbg bool,
) (map[string]StatusKind, map[string]string, error) {
	allStatuses := map[string]StatusKind{}
	allImages := map[string]string{}
	for _, endpoint := range endpoints {
		statuses, images, err := c.CheckEndpoint(endpoint)
		if err != nil {
			return nil, nil, err
		}
		if dbg {
			Ldbg("got statuses for endpoint: %d", len(statuses))
			Ldbg("got images for endpoint: %d", len(images))
		}
		for m, s := range statuses {
			allStatuses[m] = s
		}
		for k, v := range images {
			allImages[k] = v
		}
	}
	return allStatuses, allImages, nil
}

// StartOnlineListCheckerDaemon starts a checker for all streams
func (c *CheckerCommon) StartOnlineListCheckerDaemon(checker Checker) {
	go func() {
	requests:
		for request := range c.statusRequests {
			start := time.Now()
			var queryList QueryModelList
			if request.Models == nil {
				queryList = AllModels
			} else {
				queryList = NewQueryModelList(setToSlice(request.Models))
			}
			statuses, images, err := checker.CheckStatusesMany(queryList, request.CheckMode)
			if err != nil {
				Lerr("%v", err)
				request.Callback(StatusResults{Request: &request, Error: true})
				continue requests
			}
			if request.Models != nil {
				filtered := make(map[string]StatusKind, len(request.Models))
				for modelID := range request.Models {
					if status, ok := statuses[modelID]; ok {
						filtered[modelID] = status
					} else {
						filtered[modelID] = StatusUnknown
					}
				}
				statuses = filtered
				time.Sleep(time.Duration(c.IntervalMs) * time.Millisecond)
			}
			elapsed := time.Since(start)
			if c.Dbg {
				Ldbg("got statuses: %d", len(statuses))
			}
			request.Callback(StatusResults{
				Request:  &request,
				Statuses: statuses,
				Images:   images,
				Elapsed:  elapsed,
			})
		}
	}()
}

// StartFixedListCheckerDaemon starts a checker for a fixed list of streams
func (c *CheckerCommon) StartFixedListCheckerDaemon(checker Checker) {
	go func() {
	requests:
		for request := range c.statusRequests {
			start := time.Now()
			statuses, images, err := checker.CheckStatusesMany(
				NewQueryModelList(setToSlice(request.Models)),
				request.CheckMode,
			)
			if err != nil {
				Lerr("%v", err)
				request.Callback(StatusResults{Request: &request, Error: true})
				continue requests
			}
			time.Sleep(time.Duration(c.IntervalMs) * time.Millisecond)
			elapsed := time.Since(start)
			if c.Dbg {
				Ldbg("online streamers: %d", len(statuses))
			}
			request.Callback(StatusResults{
				Request:  &request,
				Statuses: statuses,
				Images:   images,
				Elapsed:  elapsed,
			})
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

func onlineStatuses(ss map[string]bool) map[string]StatusKind {
	statusMap := map[string]StatusKind{}
	for k, s := range ss {
		if s {
			statusMap[k] = StatusOnline
		}
	}
	return statusMap
}
