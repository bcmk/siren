package checkers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// Flirt4FreeChecker implements a checker for Flirt4Free
type Flirt4FreeChecker struct {
	BaseChecker[*SimpleCheckerConfig]
}

var _ Checker = &Flirt4FreeChecker{}

// Site returns the site name.
func (*Flirt4FreeChecker) Site() string { return "flirt4free" }

// Init loads flirt4free-checker.json.
func (c *Flirt4FreeChecker) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &SimpleCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	return nil
}

type flirt4FreeCheckResponse struct {
	Status string `json:"status"`
}

type flirt4FreeOnlineModel struct {
	Name           string `json:"name"`
	ScreencapImage string `json:"screencap_image"`
	RoomStatus     string `json:"room_status"`
}

type flirt4FreeOnlineResponse struct {
	Error *struct {
		Method      string
		Code        string
		Description string
	}
	Girls map[int]flirt4FreeOnlineModel
	Guys  map[int]flirt4FreeOnlineModel
	Trans map[int]flirt4FreeOnlineModel
}

// QueryStatus checks Flirt4Free model status
func (c *Flirt4FreeChecker) QueryStatus(modelID string) (cmdlib.StreamerInfoWithStatus, error) {
	resp := c.DoGetRequest(fmt.Sprintf("https://ws.vs3.com/rooms/check-model-status.php?model_name=%s", modelID), c.Cfg.Headers)
	if resp == nil {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode != 200 {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	buf := bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	if err != nil {
		cmdlib.Lerr("cannot read response for model %s, %v", modelID, err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	parsed := &flirt4FreeCheckResponse{}
	err = json.Unmarshal(buf.Bytes(), parsed)
	if err != nil {
		cmdlib.Lerr("cannot parse response for model %s, %v", modelID, err)
		cmdlib.Ldbg("response: %s", buf.String())
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: flirt4FreeStatus(parsed.Status)}, nil
}

func flirt4FreeStatus(roomStatus string) cmdlib.StatusKind {
	switch roomStatus {
	case "failed":
		return cmdlib.StatusNotFound
	case "offline":
		return cmdlib.StatusOffline
	case "online":
		return cmdlib.StatusOnline
	}
	cmdlib.Lerr("cannot parse room status \"%s\"", roomStatus)
	return cmdlib.StatusUnknown
}

func flirt4FreeShowKind(roomStatus string) cmdlib.ShowKind {
	switch roomStatus {
	case "In Open":
		return cmdlib.ShowPublic
	case "In Private":
		return cmdlib.ShowPrivate
	case "On Break":
		return cmdlib.ShowAway
	}
	return cmdlib.ShowUnknown
}

func flirt4FreeCanonicalAPIModelID(name string) string {
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, "&amp;", "and")
	name = strings.ReplaceAll(name, "&", "")
	name = strings.ReplaceAll(name, ";", "")
	return strings.ToLower(name)
}

// NicknamePreprocessing preprocesses nickname to canonical form
func (c *Flirt4FreeChecker) NicknamePreprocessing(name string) string {
	name = strings.ReplaceAll(name, "-", "_")
	return strings.ToLower(name)
}

// QueryOnlineStreamers returns Flirt4Free online models
func (c *Flirt4FreeChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	streamers := map[string]cmdlib.StreamerInfo{}
	resp, buf, err := cmdlib.OnlineQuery(c.Cfg.UsersOnlineEndpoint, c.Client, c.Cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	var parsed flirt4FreeOnlineResponse
	err = json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		cmdlib.Ldbg("response: %s", buf.String())
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("API error, code: %s, description: %s", parsed.Error.Code, parsed.Error.Description)
	}
	if len(parsed.Girls) == 0 || len(parsed.Guys) == 0 || len(parsed.Trans) == 0 {
		return nil, errors.New("zero online models reported")
	}
	for _, m := range parsed.Girls {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		streamers[modelID] = cmdlib.StreamerInfo{
			ImageURL: m.ScreencapImage,
			ShowKind: flirt4FreeShowKind(m.RoomStatus),
		}
	}
	for _, m := range parsed.Guys {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		streamers[modelID] = cmdlib.StreamerInfo{
			ImageURL: m.ScreencapImage,
			ShowKind: flirt4FreeShowKind(m.RoomStatus),
		}
	}
	for _, m := range parsed.Trans {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		streamers[modelID] = cmdlib.StreamerInfo{
			ImageURL: m.ScreencapImage,
			ShowKind: flirt4FreeShowKind(m.RoomStatus),
		}
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *Flirt4FreeChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

// Capabilities lists the surfaces Flirt4Free exposes for dispatch.
func (*Flirt4FreeChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           true,
	}
}
