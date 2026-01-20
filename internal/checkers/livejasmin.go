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
	RoomTopic         string `json:"roomTopic"`
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

func liveJasminShowKind(status string) cmdlib.ShowKind {
	switch status {
	case "free_chat":
		return cmdlib.ShowPublic
	case "member_chat", "private_chat", "members_only":
		return cmdlib.ShowPrivate
	}
	return cmdlib.ShowUnknown
}

// CheckEndpoint returns LiveJasmin online models
func (c *LiveJasminChecker) CheckEndpoint(endpoint string) (map[string]cmdlib.ChannelInfo, error) {
	client := c.ClientsLoop.NextClient()
	channels := map[string]cmdlib.ChannelInfo{}
	resp, buf, err := cmdlib.OnlineQuery(endpoint, client, c.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	var parsed liveJasminResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if parsed.Status != "OK" {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot query a list of models, %d", parsed.ErrorCode)
	}
	for _, m := range parsed.Data.Models {
		modelID := strings.ToLower(m.PerformerID)
		channels[modelID] = cmdlib.ChannelInfo{
			ImageURL: m.ProfilePictureURL.Size896x504,
			ShowKind: liveJasminShowKind(m.Status),
			Subject:  m.RoomTopic,
		}
	}
	return channels, nil
}

// QueryOnlineChannels returns LiveJasmin online models
func (c *LiveJasminChecker) QueryOnlineChannels() (map[string]cmdlib.ChannelInfo, error) {
	channels := map[string]cmdlib.ChannelInfo{}
	for _, endpoint := range c.UsersOnlineEndpoints {
		endpointChannels, err := c.CheckEndpoint(endpoint)
		if err != nil {
			return nil, err
		}
		if c.Dbg {
			cmdlib.Ldbg("got channels for endpoint: %d", len(endpointChannels))
		}
		for channelID, info := range endpointChannels {
			channels[channelID] = info
		}
	}
	return channels, nil
}

// QueryFixedListOnlineChannels is not implemented for online list checkers
func (c *LiveJasminChecker) QueryFixedListOnlineChannels([]string, cmdlib.CheckMode) (map[string]cmdlib.ChannelInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// UsesFixedList returns false for online list checkers
func (c *LiveJasminChecker) UsesFixedList() bool { return false }

// SubjectSupported returns true for LiveJasmin
func (c *LiveJasminChecker) SubjectSupported() bool { return true }
