package cmdlib

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// StatusRequest represents a request of model status
type StatusRequest struct {
	SpecialModels map[string]bool
	Specific      map[string]bool
	Callback      func(StatusResults)
	CheckMode     CheckMode
}

// StatusResultsData contains data from online checking algorithm
type StatusResultsData struct {
	Statuses map[string]StatusKind
	Images   map[string]string
	Elapsed  time.Duration
}

// StatusResults contains results from online checking algorithm
type StatusResults struct {
	Data   *StatusResultsData
	Errors int
}

// CheckerConfig represents checker config
type CheckerConfig struct {
	UsersOnlineEndpoints []string
	Clients              []*Client
	Headers              [][2]string
	Dbg                  bool
	SpecificConfig       map[string]string
	QueueSize            int
	SiteOnlineModels     map[string]bool
	Subscriptions        map[string]StatusKind
	IntervalMs           int
}

// Checker is the interface for a checker for specific site
type Checker interface {
	CheckStatusSingle(modelID string) StatusKind
	CheckStatusesMany(specific QueryModelList, checkMode CheckMode) (statuses map[string]StatusKind, images map[string]string, err error)
	Start()
	Init(checker Checker, config CheckerConfig)
	Updater() Updater
	PushStatusRequest(statusRequest StatusRequest) error
	CreateUpdater() Updater
}

// CheckerCommon contains common fields for all the checkers
type CheckerCommon struct {
	CheckerConfig
	ClientsLoop    clientsLoop
	updater        Updater
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

// Updater returns default updater
func (c *CheckerCommon) Updater() Updater { return c.updater }

// PushStatusRequest adds a status request to the queue
func (c *CheckerCommon) PushStatusRequest(statusRequest StatusRequest) error {
	select {
	case c.statusRequests <- statusRequest:
		return nil
	default:
		return ErrFullQueue
	}
}

type endpointChecker interface {
	CheckEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error)
}

// Init initializes checker common fields
func (c *CheckerCommon) Init(checker Checker, config CheckerConfig) {
	c.UsersOnlineEndpoints = config.UsersOnlineEndpoints
	c.Headers = config.Headers
	c.Dbg = config.Dbg
	c.SpecificConfig = config.SpecificConfig
	c.QueueSize = config.QueueSize
	c.SiteOnlineModels = config.SiteOnlineModels
	c.Subscriptions = config.Subscriptions
	c.IntervalMs = config.IntervalMs
	c.ClientsLoop = clientsLoop{clients: config.Clients}
	c.statusRequests = make(chan StatusRequest, config.QueueSize)
	c.updater = checker.CreateUpdater()
}

// CheckEndpoints performs queries to all available endpoints
func CheckEndpoints(c endpointChecker, endpoints []string, dbg bool) (map[string]StatusKind, map[string]string, error) {
	allStatuses := map[string]StatusKind{}
	allImages := map[string]string{}
	for _, endpoint := range endpoints {
		statuses, images, err := c.CheckEndpoint(endpoint)
		if err != nil {
			return nil, nil, err
		}
		if dbg {
			Ldbg("got statuses for endpoint: %d", len(statuses))
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

// StartFullCheckerDaemon starts a checker for all streams
func (c *CheckerCommon) StartFullCheckerDaemon(checker Checker) {
	go func() {
	requests:
		for request := range c.statusRequests {
			start := time.Now()
			statuses := map[string]StatusKind{}
			images := map[string]string{}
			var err error
			if request.Specific == nil {
				statuses, images, err = checker.CheckStatusesMany(AllModels, request.CheckMode)
				if err != nil {
					Lerr("%v", err)
					request.Callback(StatusResults{Errors: 1})
					continue requests
				}
			}
			nErrors := 0
			manual := request.SpecialModels
			if request.Specific != nil {
				manual = request.Specific
			}
			for modelID := range manual {
				time.Sleep(time.Duration(c.IntervalMs) * time.Millisecond)
				status := checker.CheckStatusSingle(modelID)
				if status == StatusUnknown || (status&StatusNotFound != 0 && request.Specific == nil) {
					Lerr("status for model %s reported: %v", modelID, status)
					nErrors++
				}
				statuses[modelID] = status
			}
			time.Sleep(time.Duration(c.IntervalMs) * time.Millisecond)
			elapsed := time.Since(start)
			if c.Dbg {
				Ldbg("got statuses: %d", len(statuses))
			}
			request.Callback(StatusResults{Data: &StatusResultsData{Statuses: statuses, Images: images, Elapsed: elapsed}, Errors: nErrors})
		}
	}()
}

// StartSelectiveCheckerDaemon starts a checker for selected streams
func (c *CheckerCommon) StartSelectiveCheckerDaemon(checker Checker) {
	go func() {
	requests:
		for request := range c.statusRequests {
			start := time.Now()
			var statuses map[string]StatusKind
			var images map[string]string
			var err error
			statuses, images, err = checker.CheckStatusesMany(NewQueryModelList(setToSlice(request.Specific)), request.CheckMode)
			if err != nil {
				Lerr("%v", err)
				request.Callback(StatusResults{Errors: 1})
				continue requests
			}
			time.Sleep(time.Duration(c.IntervalMs) * time.Millisecond)
			elapsed := time.Since(start)
			if c.Dbg {
				Ldbg("online streamers: %d", len(statuses))
			}
			request.Callback(StatusResults{Data: &StatusResultsData{Statuses: statuses, Images: images, Elapsed: elapsed}, Errors: 0})
		}
	}()
}

// CreateFullUpdater creates an updater quering for all streams
func (c *CheckerCommon) CreateFullUpdater(checker Checker) Updater {
	return &fullUpdater{checker: checker, siteOnlineModels: c.SiteOnlineModels}
}

// CreateSelectiveUpdater creates an updater for selected streams
func (c *CheckerCommon) CreateSelectiveUpdater(checker Checker) Updater {
	return &selectiveUpdater{checker: checker, siteOnlineModels: c.SiteOnlineModels, knowns: selectKnowns(c.Subscriptions)}
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
