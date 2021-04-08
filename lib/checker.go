package lib

import (
	"time"
)

// CheckerResults contains results from online checking algorithm
type CheckerResults struct {
	Updates []StatusUpdate
	Images  map[string]string
}

// Checker is the interface for a checker for specific site
type Checker interface {
	CheckSingle(modelID string) StatusKind
	Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
		statusRequests chan StatusRequest,
		resultsCh chan CheckerResults,
		errorsCh chan struct{},
		elapsedCh chan time.Duration,
	)
	Init(usersOnlineEndpoints []string, clients []*Client, headers [][2]string, dbg bool, specificConfig map[string]string)
}

// CheckerCommon contains common fields for all the checkers
type CheckerCommon struct {
	usersOnlineEndpoint []string
	clients             []*Client
	headers             [][2]string
	dbg                 bool
	specificConfig      map[string]string
	clientsLoop         clientsLoop
}

type endpointChecker interface {
	checkEndpoint(endpoint string) (onlineModels map[string]bool, images map[string]string, err error)
}

// Init initializes checker common fields
func (c *CheckerCommon) Init(
	usersOnlineEndpoints []string,
	clients []*Client,
	headers [][2]string,
	dbg bool,
	specificConfig map[string]string) {

	c.usersOnlineEndpoint = usersOnlineEndpoints
	c.clients = clients
	c.headers = headers
	c.dbg = dbg
	c.specificConfig = specificConfig
	c.clientsLoop = clientsLoop{clients: clients}
}

func getUpdates(prev, next map[string]bool) []StatusUpdate {
	var result []StatusUpdate
	new, removed := HashDiffNewRemoved(prev, next)
	for _, i := range removed {
		result = append(result, StatusUpdate{ModelID: i, Status: StatusOffline})
	}
	for _, i := range new {
		result = append(result, StatusUpdate{ModelID: i, Status: StatusOnline})
	}
	return result
}

func checkEndpoints(c endpointChecker, endpoints []string, dbg bool) (map[string]bool, map[string]string, error) {
	allNextOnline := map[string]bool{}
	allImages := map[string]string{}
	for _, endpoint := range endpoints {
		nextOnline, images, err := c.checkEndpoint(endpoint)
		if err != nil {
			return nil, nil, err
		}
		if dbg {
			Ldbg("online models for endpoint: %d", len(nextOnline))
		}
		for m := range nextOnline {
			allNextOnline[m] = true
		}
		for k, v := range images {
			allImages[k] = v
		}
	}
	return allNextOnline, allImages, nil
}
