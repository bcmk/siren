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

// Cam4Checker implements a checker for CAM4
type Cam4Checker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &Cam4Checker{}

// Cam4ModelIDRegexp is a regular expression to check model IDs
var Cam4ModelIDRegexp = regexp.MustCompile(`^[a-z0-9_]+$`)

var cam4ModelRegex = regexp.MustCompile(`^(?:https?://)?cam4\.com/([A-Za-z0-9_]+)(?:[/?].*)?$`)

// Cam4CanonicalModelID preprocesses model ID string to canonical for CAM4 form
func Cam4CanonicalModelID(name string) string {
	m := cam4ModelRegex.FindStringSubmatch(name)
	if len(m) == 2 {
		name = m[1]
	}
	return strings.ToLower(name)
}

type cam4Model struct {
	Nickname string `json:"nickname"`
	ThumbBig string `json:"thumb_big"`
}

type cam4Response struct {
	Username string `json:"username"`
	Status   string `json:"status"`
}

// CheckStatusSingle checks CAM4 model status
func (c *Cam4Checker) CheckStatusSingle(modelID string) cmdlib.StatusKind {
	url := fmt.Sprintf("https://api.pinklabel.com/api/v1/cams/profile/%s.json", modelID)
	addr, resp := c.DoGetRequest(url)
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
	parsed := &cam4Response{}
	err = decoder.Decode(parsed)
	if err != nil {
		cmdlib.Lerr("[%v] cannot parse response for model %s, %v", addr, modelID, err)
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return cmdlib.StatusUnknown
	}
	return cam4RoomStatus(parsed.Status)
}

func cam4RoomStatus(roomStatus string) cmdlib.StatusKind {
	switch roomStatus {
	case "online":
		return cmdlib.StatusOnline
	case "offline":
		return cmdlib.StatusOffline
	}
	cmdlib.Lerr("cannot parse room status \"%s\"", roomStatus)
	return cmdlib.StatusUnknown
}

// CheckEndpoint returns CAM4 online models on the endpoint
func (c *Cam4Checker) CheckEndpoint(endpoint string) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
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
	var parsed []cam4Model
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed {
		modelID := strings.ToLower(m.Nickname)
		onlineModels[modelID] = cmdlib.StatusOnline
		images[modelID] = m.ThumbBig
	}
	return
}

// CheckStatusesMany returns CAM4 online models
func (c *Cam4Checker) CheckStatusesMany(cmdlib.QueryModelList, cmdlib.CheckMode) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
	return cmdlib.CheckEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *Cam4Checker) Start() { c.StartFullCheckerDaemon(c) }

// CreateUpdater creates an updater
func (c *Cam4Checker) CreateUpdater() cmdlib.Updater { return c.CreateFullUpdater(c) }
