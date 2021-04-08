package lib

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

// BongaCamsChecker implements a checker for BongaCams
type BongaCamsChecker struct{ CheckerCommon }

var _ FullChecker = &BongaCamsChecker{}

type bongacamsModel struct {
	Username      string `json:"username"`
	ProfileImages struct {
		ThumbnailImageMediumLive string `json:"thumbnail_image_medium_live"`
	} `json:"profile_images"`
}

// CheckSingle checks BongaCams model status
func (c *BongaCamsChecker) CheckSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	req, err := http.NewRequest("GET", fmt.Sprintf("https://en.bongacams.com/%s", modelID), nil)
	CheckErr(err)
	for _, h := range c.headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := client.Client.Do(req)
	if err != nil {
		Lerr("[%v] cannot send a query, %v", client.Addr, err)
		return StatusUnknown
	}
	CheckErr(resp.Body.Close())
	if c.dbg {
		Ldbg("query status for %s: %d", modelID, resp.StatusCode)
	}
	switch resp.StatusCode {
	case 200:
		return StatusOnline
	case 302:
		return StatusOffline
	case 404:
		return StatusNotFound
	}
	return StatusUnknown
}

// checkEndpoint returns BongaCams online models on the endpoint
func (c *BongaCamsChecker) checkEndpoint(endpoint string) (onlineModels map[string]bool, images map[string]string, err error) {
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
	var parsed []bongacamsModel
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}

	if len(parsed) == 0 {
		return nil, nil, errors.New("zero online models reported")
	}

	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = true
		images[modelID] = "https:" + m.ProfileImages.ThumbnailImageMediumLive
	}
	return
}

// CheckFull returns BongaCams online models
func (c *BongaCamsChecker) CheckFull() (onlineModels map[string]bool, images map[string]string, err error) {
	return checkEndpoints(c, c.usersOnlineEndpoint, c.dbg)
}

// Start starts a daemon
func (c *BongaCamsChecker) Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return fullDaemonStart(c, siteOnlineModels, intervalMs, dbg)
}
