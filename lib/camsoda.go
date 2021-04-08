package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

// CamSodaChecker implements a checker for CamSoda
type CamSodaChecker struct{ CheckerCommon }

var _ FullChecker = &CamSodaChecker{}

type camSodaUserResponse struct {
	Status bool
	Error  string
	User   struct {
		Chat struct {
			Status string
		}
	}
}

type camSodaOnlineResponse struct {
	Status  bool
	Error   string
	Results []struct {
		Username string
		Status   string
		Thumb    string
	}
}

// CheckSingle checks CamSoda model status
func (c *CamSodaChecker) CheckSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	req, err := http.NewRequest("GET", fmt.Sprintf("https://feed.camsoda.com/api/v1/user/%s", modelID), nil)
	CheckErr(err)
	for _, h := range c.headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := client.Client.Do(req)
	if err != nil {
		Lerr("[%v] cannot send a query, %v", client.Addr, err)
		return StatusUnknown
	}
	defer func() {
		CheckErr(resp.Body.Close())
	}()
	if c.dbg {
		Ldbg("[%v] query status for %s: %d", client.Addr, modelID, resp.StatusCode)
	}
	if resp.StatusCode == 401 {
		return StatusDenied
	}
	if resp.StatusCode == 404 {
		return StatusNotFound
	}
	buf := bytes.Buffer{}
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		Lerr("[%v] cannot read response for model %s, %v", client.Addr, modelID, err)
		return StatusUnknown
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &camSodaUserResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		Lerr("[%v] cannot parse response for model %s, %v", client.Addr, modelID, err)
		if c.dbg {
			Ldbg("response: %s", buf.String())
		}
		return StatusUnknown
	}
	if !parsed.Status {
		Lerr("[%v] API error for model %s, %s", client.Addr, modelID, parsed.Error)
		return StatusUnknown
	}
	return camSodaStatus(parsed.User.Chat.Status)
}

func camSodaStatus(roomStatus string) StatusKind {
	switch roomStatus {
	case "online":
		return StatusOnline
	case "limited":
		return StatusOnline
	case "private":
		return StatusOnline
	case "offline":
		return StatusOffline
	}
	Lerr("cannot parse room status \"%s\"", roomStatus)
	return StatusUnknown
}

// checkEndpoint returns CamSoda online models on the endpoint
func (c *CamSodaChecker) checkEndpoint(endpoint string) (onlineModels map[string]bool, images map[string]string, err error) {
	client := c.clientsLoop.nextClient()
	onlineModels = map[string]bool{}
	images = map[string]string{}
	resp, buf, err := onlineQuery(endpoint, client, c.headers)
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
		if c.dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if !parsed.Status {
		return nil, nil, fmt.Errorf("API error, %s", parsed.Error)
	}
	for _, m := range parsed.Results {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = true
		images[modelID] = m.Thumb
	}
	return
}

// CheckFull returns CamSoda online models
func (c *CamSodaChecker) CheckFull() (onlineModels map[string]bool, images map[string]string, err error) {
	return checkEndpoints(c, c.usersOnlineEndpoint, c.dbg)
}

// Start starts a daemon
func (c *CamSodaChecker) Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return fullDaemonStart(c, siteOnlineModels, intervalMs, dbg)
}
