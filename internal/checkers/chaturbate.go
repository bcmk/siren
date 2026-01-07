package checkers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/bcmk/siren/lib/cmdlib"
)

// ChaturbateChecker implements a checker for Chaturbate
type ChaturbateChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &ChaturbateChecker{}

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
func (c *ChaturbateChecker) CheckStatusSingle(modelID string) cmdlib.StatusKind {
	addr, resp := c.DoGetRequest(fmt.Sprintf("https://chaturbate.com/api/biocontext/%s/?", modelID))
	if resp == nil {
		return cmdlib.StatusUnknown
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode == 404 {
		return cmdlib.StatusNotFound
	}
	buf := bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	if err != nil {
		cmdlib.Lerr("[%v] cannot read response for model %s, %v", addr, modelID, err)
		return cmdlib.StatusUnknown
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &chaturbateResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		cmdlib.Lerr("[%v] cannot parse response for model %s, %v", addr, modelID, err)
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return cmdlib.StatusUnknown
	}
	if parsed.Status != nil {
		return chaturbateStatus(parsed.Code)
	}
	return chaturbateRoomStatus(parsed.RoomStatus)
}

func chaturbateStatus(status string) cmdlib.StatusKind {
	switch status {
	case "access-denied":
		return cmdlib.StatusDenied
	case "unauthorized":
		return cmdlib.StatusDenied
	}
	return cmdlib.StatusUnknown
}

func chaturbateRoomStatus(roomStatus string) cmdlib.StatusKind {
	switch roomStatus {
	case "public":
		return cmdlib.StatusOnline
	case "private":
		return cmdlib.StatusOnline
	case "group":
		return cmdlib.StatusOnline
	case "hidden":
		return cmdlib.StatusOnline
	case "connecting":
		return cmdlib.StatusOnline
	case "password protected":
		return cmdlib.StatusOnline
	case "away":
		return cmdlib.StatusOffline
	case "offline":
		return cmdlib.StatusOffline
	}
	cmdlib.Lerr("cannot parse room status \"%s\"", roomStatus)
	return cmdlib.StatusUnknown
}

// CheckEndpoint returns Chaturbate online models on the endpoint
func (c *ChaturbateChecker) CheckEndpoint(endpoint string) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
	client := c.ClientsLoop.NextClient()
	onlineModels = map[string]cmdlib.StatusKind{}
	images = map[string]string{}
	resp, buf, err := cmdlib.OnlineQuery(endpoint, client, c.Headers)
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
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = cmdlib.StatusOnline
		images[modelID] = m.ImageURL
	}
	return
}

// CheckStatusesMany returns Chaturbate online models
func (c *ChaturbateChecker) CheckStatusesMany(cmdlib.QueryModelList, cmdlib.CheckMode) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
	return cmdlib.CheckEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *ChaturbateChecker) Start() { c.StartFullCheckerDaemon(c) }
