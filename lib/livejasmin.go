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

// LiveJasminChecker implements a checker for LiveJasmin
type LiveJasminChecker struct{ CheckerCommon }

var _ FullChecker = &LiveJasminChecker{}

type liveJasminModel struct {
	PerformerID       string `json:"performerId"`
	Status            string `json:"status"`
	ProfilePictureURL struct {
		Size896x503 string `json:"size896x504"`
	} `json:"profilePictureUrl"`
}

type liveJasminResponse struct {
	Status    string `json:"status"`
	ErrorCode int    `json:"errorCode"`
	Data      struct {
		Models []liveJasminModel `json:"models"`
	} `json:"data"`
}

// CheckSingle checks LiveJasmin model status
func (c *LiveJasminChecker) CheckSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	psID := c.specificConfig["ps_id"]
	accessKey := c.specificConfig["access_key"]
	url := fmt.Sprintf("https://pt.potawe.com/api/model/status?performerId=%s&psId=%s&accessKey=%s&legacyRedirect=1", modelID, psID, accessKey)
	req, err := http.NewRequest("GET", url, nil)
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
	return liveJasminStatus(buf.String())
}

func liveJasminStatus(roomStatus string) StatusKind {
	switch roomStatus {
	case "free_chat":
		return StatusOnline
	case "member_chat":
		return StatusOnline
	case "members_only":
		return StatusOnline
	case "offline":
		return StatusOffline
	case "invalid":
		return StatusNotFound
	}
	Lerr("cannot parse room status \"%s\"", roomStatus)
	return StatusUnknown
}

// checkEndpoint returns LiveJasmin online models
func (c *LiveJasminChecker) checkEndpoint(endpoint string) (onlineModels map[string]bool, images map[string]string, err error) {
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
	var parsed liveJasminResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if parsed.Status != "OK" {
		if c.dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot query a list of models, %d", parsed.ErrorCode)
	}
	for _, m := range parsed.Data.Models {
		modelID := strings.ToLower(m.PerformerID)
		onlineModels[modelID] = true
		images[modelID] = "https:" + m.ProfilePictureURL.Size896x503
	}
	return
}

// CheckFull returns LiveJasmin online models
func (c *LiveJasminChecker) CheckFull() (onlineModels map[string]bool, images map[string]string, err error) {
	return checkEndpoints(c, c.usersOnlineEndpoint, c.dbg)
}

// Start starts a daemon
func (c *LiveJasminChecker) Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return fullDaemonStart(c, siteOnlineModels, intervalMs, dbg)
}
