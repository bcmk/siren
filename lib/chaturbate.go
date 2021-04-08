package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ChaturbateChecker implements a checker for Chaturbate
type ChaturbateChecker struct{ CheckerCommon }

var _ FullChecker = &ChaturbateChecker{}

var chaturbateModelRegex = regexp.MustCompile(`^(?:http(?:s)?://)?(?:[A-Za-z]+\.)?chaturbate\.com(?:/p|/b)?/([A-Za-z0-9\-_@]+)(?:/)?(?:\?.*)?$`)

// ChaturbateCanonicalModelID preprocesses model ID string to canonical for Chaturbate form
func ChaturbateCanonicalModelID(name string) string {
	m := chaturbateModelRegex.FindStringSubmatch(name)
	if len(m) == 2 {
		name = m[1]
	}
	return strings.ToLower(name)
}

type chaturbateModel struct {
	Username string `json:"username"`
	ImageURL string `json:"image_url"`
}

type chaturbateResponse struct {
	RoomStatus string `json:"room_status"`
	Status     *int   `json:"status"`
	Code       string `json:"code"`
}

// CheckSingle checks Chaturbate model status
func (c *ChaturbateChecker) CheckSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	req, err := http.NewRequest("GET", fmt.Sprintf("https://en.chaturbate.com/api/chatvideocontext/%s/", modelID), nil)
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
	parsed := &chaturbateResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		Lerr("[%v] cannot parse response for model %s, %v", client.Addr, modelID, err)
		if c.dbg {
			Ldbg("response: %s", buf.String())
		}
		return StatusUnknown
	}
	if parsed.Status != nil {
		return chaturbateStatus(parsed.Code)
	}
	return chaturbateRoomStatus(parsed.RoomStatus)
}

func chaturbateStatus(status string) StatusKind {
	switch status {
	case "access-denied":
		return StatusDenied
	case "unauthorized":
		return StatusDenied
	}
	return StatusUnknown
}

func chaturbateRoomStatus(roomStatus string) StatusKind {
	switch roomStatus {
	case "public":
		return StatusOnline
	case "private":
		return StatusOnline
	case "group":
		return StatusOnline
	case "hidden":
		return StatusOnline
	case "connecting":
		return StatusOnline
	case "password protected":
		return StatusOnline
	case "away":
		return StatusOffline
	case "offline":
		return StatusOffline
	}
	Lerr("cannot parse room status \"%s\"", roomStatus)
	return StatusUnknown
}

// checkEndpoint returns Chaturbate online models on the endpoint
func (c *ChaturbateChecker) checkEndpoint(endpoint string) (onlineModels map[string]bool, images map[string]string, err error) {
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
	var parsed []chaturbateModel
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = true
		images[modelID] = m.ImageURL
	}
	return
}

// CheckFull returns Chaturbate online models
func (c *ChaturbateChecker) CheckFull() (onlineModels map[string]bool, images map[string]string, err error) {
	return checkEndpoints(c, c.usersOnlineEndpoint, c.dbg)
}

// Start starts a daemon
func (c *ChaturbateChecker) Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return fullDaemonStart(c, siteOnlineModels, intervalMs, dbg)
}
