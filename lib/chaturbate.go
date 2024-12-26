package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// ChaturbateChecker implements a checker for Chaturbate
type ChaturbateChecker struct{ CheckerCommon }

var _ Checker = &ChaturbateChecker{}

var chaturbateModelRegex = regexp.MustCompile(`^(?:https?://)?(?:[A-Za-z]+\.)?chaturbate\.com(?:/p|/b)?/([A-Za-z0-9\-_@]+)/?(?:\?.*)?$`)

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

// CheckStatusSingle checks Chaturbate model status
func (c *ChaturbateChecker) CheckStatusSingle(modelID string) StatusKind {
	addr, resp := c.doGetRequest(fmt.Sprintf("https://chaturbate.com/api/chatvideocontext/%s/", modelID))
	if resp == nil {
		return StatusUnknown
	}
	defer CloseBody(resp.Body)
	if resp.StatusCode == 404 {
		return StatusNotFound
	}
	buf := bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	if err != nil {
		Lerr("[%v] cannot read response for model %s, %v", addr, modelID, err)
		return StatusUnknown
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &chaturbateResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		Lerr("[%v] cannot parse response for model %s, %v", addr, modelID, err)
		if c.Dbg {
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
func (c *ChaturbateChecker) checkEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error) {
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
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	var parsed []chaturbateModel
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = StatusOnline
		images[modelID] = m.ImageURL
	}
	return
}

// CheckStatusesMany returns Chaturbate online models
func (c *ChaturbateChecker) CheckStatusesMany(QueryModelList, CheckMode) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return checkEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *ChaturbateChecker) Start()                 { c.startFullCheckerDaemon(c) }
func (c *ChaturbateChecker) createUpdater() Updater { return c.createFullUpdater(c) }
