package checkers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// LiveJasminCheckerConfig holds API credentials and per-category
// online endpoints (LiveJasmin's API exposes multiple online URLs).
type LiveJasminCheckerConfig struct {
	BaseCheckerConfig    `mapstructure:",squash"`
	UsersOnlineEndpoints []string      `mapstructure:"users_online_endpoints"`
	PsID                 cmdlib.Secret `mapstructure:"ps_id"`
	AccessKey            cmdlib.Secret `mapstructure:"access_key"`
	Headers              [][2]string   `mapstructure:"headers"`
}

func (c *LiveJasminCheckerConfig) validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	if c.PsID == "" {
		return errors.New("configure ps_id")
	}
	if c.AccessKey == "" {
		return errors.New("configure access_key")
	}
	if len(c.UsersOnlineEndpoints) == 0 {
		return errors.New("configure users_online_endpoints")
	}
	for _, ep := range c.UsersOnlineEndpoints {
		if ep == "" {
			return errors.New("users_online_endpoints contains an empty entry")
		}
	}
	return nil
}

// LiveJasminChecker implements a checker for LiveJasmin
type LiveJasminChecker struct {
	BaseChecker[*LiveJasminCheckerConfig]
}

var _ Checker = &LiveJasminChecker{}

// Site returns the site name.
func (*LiveJasminChecker) Site() string { return "livejasmin" }

// Init loads livejasmin-checker.json.
func (c *LiveJasminChecker) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &LiveJasminCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	return nil
}

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

// QueryStatus checks LiveJasmin model status
func (c *LiveJasminChecker) QueryStatus(modelID string) (cmdlib.StreamerInfoWithStatus, error) {
	psID := string(c.Cfg.PsID)
	accessKey := string(c.Cfg.AccessKey)
	url := fmt.Sprintf("https://pt.potawe.com/api/model/status?performerId=%s&psId=%s&accessKey=%s&legacyRedirect=1", modelID, psID, accessKey)
	resp := c.DoGetRequest(url, c.Cfg.Headers)
	if resp == nil {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	defer cmdlib.CloseBody(resp.Body)
	switch resp.StatusCode {
	case 401:
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusDenied}, nil
	case 404:
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}, nil
	}
	buf := bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	if err != nil {
		cmdlib.Lerr("cannot read response for model %s, %v", modelID, err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: liveJasminStatus(buf.String())}, nil
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

// checkEndpoint returns LiveJasmin online models
func (c *LiveJasminChecker) checkEndpoint(endpoint string) (map[string]cmdlib.StreamerInfo, error) {
	streamers := map[string]cmdlib.StreamerInfo{}
	resp, buf, err := cmdlib.OnlineQuery(endpoint, c.Client, c.Cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	var parsed liveJasminResponse
	err = json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		cmdlib.Ldbg("response: %s", buf.String())
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if parsed.Status != "OK" {
		cmdlib.Ldbg("response: %s", buf.String())
		return nil, fmt.Errorf("cannot query a list of models, %d", parsed.ErrorCode)
	}
	for _, m := range parsed.Data.Models {
		modelID := strings.ToLower(m.PerformerID)
		streamers[modelID] = cmdlib.StreamerInfo{
			ImageURL: m.ProfilePictureURL.Size896x504,
			ShowKind: liveJasminShowKind(m.Status),
			Subject:  m.RoomTopic,
		}
	}
	return streamers, nil
}

// QueryOnlineStreamers returns LiveJasmin online models
func (c *LiveJasminChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	streamers := map[string]cmdlib.StreamerInfo{}
	for _, endpoint := range c.Cfg.UsersOnlineEndpoints {
		endpointStreamers, err := c.checkEndpoint(endpoint)
		if err != nil {
			return nil, err
		}
		cmdlib.Ldbg("got streamers for endpoint: %d", len(endpointStreamers))
		for nickname, info := range endpointStreamers {
			streamers[nickname] = info
		}
	}
	if len(streamers) == 0 {
		return nil, errors.New("zero online models reported")
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *LiveJasminChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

// Capabilities lists the surfaces LiveJasmin exposes for dispatch.
func (*LiveJasminChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           true,
		SupportsSubject:                       true,
	}
}
