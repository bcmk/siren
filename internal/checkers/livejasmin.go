package checkers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bcmk/siren/lib/cmdlib"
)

// LiveJasminChecker implements a checker for LiveJasmin
type LiveJasminChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &LiveJasminChecker{}

type liveJasminModel struct {
	PerformerID       string `json:"performerId"`
	Status            string `json:"status"`
	ProfilePictureURL struct {
		Size896x504 string `json:"size896x504"`
	} `json:"profilePictureUrl"`
}

type liveJasminResponse struct {
	Status    string `json:"status"`
	ErrorCode int    `json:"errorCode"`
	Data      struct {
		Models []liveJasminModel `json:"models"`
	} `json:"data"`
}

// CheckStatusSingle checks LiveJasmin model status
func (c *LiveJasminChecker) CheckStatusSingle(modelID string) cmdlib.StatusKind {
	psID := c.SpecificConfig["ps_id"]
	accessKey := c.SpecificConfig["access_key"]
	url := fmt.Sprintf("https://pt.potawe.com/api/model/status?performerId=%s&psId=%s&accessKey=%s&legacyRedirect=1", modelID, psID, accessKey)
	addr, resp := c.DoGetRequest(url)
	if resp == nil {
		return cmdlib.StatusUnknown
	}
	defer cmdlib.CloseBody(resp.Body)
	switch resp.StatusCode {
	case 401:
		return cmdlib.StatusDenied
	case 404:
		return cmdlib.StatusNotFound
	}
	buf := bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	if err != nil {
		cmdlib.Lerr("[%v] cannot read response for model %s, %v", addr, modelID, err)
		return cmdlib.StatusUnknown
	}
	return liveJasminStatus(buf.String())
}

func liveJasminStatus(roomStatus string) cmdlib.StatusKind {
	switch roomStatus {
	case "free_chat":
		return cmdlib.StatusOnline
	case "member_chat":
		return cmdlib.StatusOnline
	case "members_only":
		return cmdlib.StatusOnline
	case "offline":
		return cmdlib.StatusOffline
	case "invalid":
		return cmdlib.StatusNotFound
	}
	cmdlib.Lerr("cannot parse room status \"%s\"", roomStatus)
	return cmdlib.StatusUnknown
}

// CheckEndpoint returns LiveJasmin online models
func (c *LiveJasminChecker) CheckEndpoint(endpoint string) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
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
	var parsed liveJasminResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if parsed.Status != "OK" {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot query a list of models, %d", parsed.ErrorCode)
	}
	for _, m := range parsed.Data.Models {
		modelID := strings.ToLower(m.PerformerID)
		onlineModels[modelID] = cmdlib.StatusOnline
		images[modelID] = m.ProfilePictureURL.Size896x504
	}
	return
}

// CheckStatusesMany returns LiveJasmin online models
func (c *LiveJasminChecker) CheckStatusesMany(cmdlib.QueryChannelList, cmdlib.CheckMode) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
	onlineModels = map[string]cmdlib.StatusKind{}
	images = map[string]string{}
	for _, endpoint := range c.UsersOnlineEndpoints {
		statuses, imgs, err := c.CheckEndpoint(endpoint)
		if err != nil {
			return nil, nil, err
		}
		if c.Dbg {
			cmdlib.Ldbg("got statuses for endpoint: %d", len(statuses))
			cmdlib.Ldbg("got images for endpoint: %d", len(imgs))
		}
		for channel, status := range statuses {
			onlineModels[channel] = status
		}
		for channel, image := range imgs {
			images[channel] = image
		}
	}
	return
}

// Start starts a daemon
func (c *LiveJasminChecker) Start() { c.StartOnlineListCheckerDaemon(c) }

// UsesFixedList returns false for online list checkers
func (c *LiveJasminChecker) UsesFixedList() bool { return false }
