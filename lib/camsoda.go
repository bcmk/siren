package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
)

// CamSodaChecker implements a checker for CamSoda
type CamSodaChecker struct{ CheckerCommon }

var _ Checker = &CamSodaChecker{}

type camSodaOnlineResponse struct {
	Status  bool
	Error   string
	Results []struct {
		Username string
		Status   string
		Thumb    string
	}
}

// CheckStatusSingle checks CamSoda model status
func (c *CamSodaChecker) CheckStatusSingle(modelID string) StatusKind {
	return StatusUnknown
}

// checkEndpoint returns CamSoda online models on the endpoint
func (c *CamSodaChecker) checkEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	client := c.clientsLoop.nextClient()
	onlineModels = map[string]StatusKind{}
	images = map[string]string{}
	resp, buf, err := onlineQuery(endpoint, client, c.Headers)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	var parsed camSodaOnlineResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if !parsed.Status {
		return nil, nil, fmt.Errorf("API error, %s", parsed.Error)
	}
	for _, m := range parsed.Results {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = StatusOnline
		images[modelID] = m.Thumb
	}
	return
}

// CheckStatusesMany returns CamSoda online models
func (c *CamSodaChecker) CheckStatusesMany(QueryModelList, CheckMode) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return checkEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *CamSodaChecker) Start()                 { c.startFullCheckerDaemon(c) }
func (c *CamSodaChecker) createUpdater() Updater { return c.createFullUpdater(c) }
