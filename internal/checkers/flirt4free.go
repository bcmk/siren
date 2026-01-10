package checkers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bcmk/siren/lib/cmdlib"
)

// Flirt4FreeChecker implements a checker for Flirt4Free
type Flirt4FreeChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &Flirt4FreeChecker{}

type flirt4FreeCheckResponse struct {
	Status string `json:"status"`
}

type flirt4FreeOnlineModel struct {
	Name           string `json:"name"`
	ScreencapImage string `json:"screencap_image"`
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

// CheckStatusSingle checks Flirt4Free model status
func (c *Flirt4FreeChecker) CheckStatusSingle(modelID string) cmdlib.StatusKind {
	addr, resp := c.DoGetRequest(fmt.Sprintf("https://ws.vs3.com/rooms/check-model-status.php?model_name=%s", modelID))
	if resp == nil {
		return cmdlib.StatusUnknown
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode != 200 {
		return cmdlib.StatusUnknown
	}
	buf := bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	if err != nil {
		cmdlib.Lerr("[%v] cannot read response for model %s, %v", addr, modelID, err)
		return cmdlib.StatusUnknown
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &flirt4FreeCheckResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		cmdlib.Lerr("[%v] cannot parse response for model %s, %v", addr, modelID, err)
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return cmdlib.StatusUnknown
	}
	return flirt4FreeStatus(parsed.Status)
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

func flirt4FreeCanonicalAPIModelID(name string) string {
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, "&amp;", "and")
	name = strings.ReplaceAll(name, "&", "")
	name = strings.ReplaceAll(name, ";", "")
	return strings.ToLower(name)
}

// Flirt4FreeCanonicalModelID preprocesses model ID string to canonical for Flirt4Free form
func Flirt4FreeCanonicalModelID(name string) string {
	name = strings.ReplaceAll(name, "-", "_")
	return strings.ToLower(name)
}

// QueryOnlineChannels returns Flirt4Free online models
func (c *Flirt4FreeChecker) QueryOnlineChannels(cmdlib.CheckMode) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
	client := c.ClientsLoop.NextClient()
	onlineModels = map[string]cmdlib.StatusKind{}
	images = map[string]string{}
	resp, buf, err := cmdlib.OnlineQuery(c.UsersOnlineEndpoints[0], client, c.Headers)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	var parsed flirt4FreeOnlineResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if parsed.Error != nil {
		return nil, nil, fmt.Errorf("API error, code: %s, description: %s", parsed.Error.Code, parsed.Error.Description)
	}
	if len(parsed.Girls) == 0 || len(parsed.Guys) == 0 || len(parsed.Trans) == 0 {
		return nil, nil, errors.New("zero online models reported")
	}
	for _, m := range parsed.Girls {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		onlineModels[modelID] = cmdlib.StatusOnline
		images[modelID] = m.ScreencapImage
	}
	for _, m := range parsed.Guys {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		onlineModels[modelID] = cmdlib.StatusOnline
		images[modelID] = m.ScreencapImage
	}
	for _, m := range parsed.Trans {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		onlineModels[modelID] = cmdlib.StatusOnline
		images[modelID] = m.ScreencapImage
	}
	return
}

// QueryChannelListStatuses is not implemented for online list checkers
func (c *Flirt4FreeChecker) QueryChannelListStatuses([]string, cmdlib.CheckMode) (map[string]cmdlib.StatusKind, map[string]string, error) {
	return nil, nil, cmdlib.ErrNotImplemented
}

// UsesFixedList returns false for online list checkers
func (c *Flirt4FreeChecker) UsesFixedList() bool { return false }
