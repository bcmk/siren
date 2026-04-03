package checkers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// Cam4Checker implements a checker for CAM4
type Cam4Checker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &Cam4Checker{}

// Cam4ModelIDRegexp is a regular expression to check model IDs
var Cam4ModelIDRegexp = regexp.MustCompile(`^[a-z0-9_]+$`)

var cam4ModelOrLinkRegexp = regexp.MustCompile(`^(?:https?://)?cam4\.com/([A-Za-z0-9_]+)(?:[/?].*)?$`)

// NicknamePreprocessing preprocesses nickname to canonical form
func (c *Cam4Checker) NicknamePreprocessing(name string) string {
	m := cam4ModelOrLinkRegexp.FindStringSubmatch(name)
	if len(m) == 2 {
		name = m[1]
	}
	return strings.ToLower(name)
}

// NicknameRegexp returns the regular expression to validate model IDs
func (c *Cam4Checker) NicknameRegexp() *regexp.Regexp {
	return Cam4ModelIDRegexp
}

type cam4Model struct {
	Nickname string `json:"nickname"`
	ThumbBig string `json:"thumb_big"`
	Viewers  int    `json:"viewers"`
	ShowType string `json:"show_type"`
}

type cam4Response struct {
	Username string `json:"username"`
	Status   string `json:"status"`
}

// CheckStatusSingle checks CAM4 model status
func (c *Cam4Checker) CheckStatusSingle(modelID string) (cmdlib.StatusKind, error) {
	url := fmt.Sprintf("https://api.pinklabel.com/api/v1/cams/profile/%s.json", modelID)
	addr, resp := c.DoGetRequest(url)
	if resp == nil {
		return cmdlib.StatusUnknown, nil
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode == 404 {
		return cmdlib.StatusNotFound, nil
	}
	buf := bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	if err != nil {
		cmdlib.Lerr("[%v] cannot read response for model %s, %v", addr, modelID, err)
		return cmdlib.StatusUnknown, nil
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &cam4Response{}
	err = decoder.Decode(parsed)
	if err != nil {
		cmdlib.Lerr("[%v] cannot parse response for model %s, %v", addr, modelID, err)
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return cmdlib.StatusUnknown, nil
	}
	return cam4RoomStatus(parsed.Status), nil
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

func cam4ShowKind(showType string) cmdlib.ShowKind {
	switch showType {
	case "NORMAL":
		return cmdlib.ShowPublic
	case "GROUP_SHOW_SELLING_TICKETS":
		return cmdlib.ShowGroup
	case "PRIVATE_SHOW":
		return cmdlib.ShowPrivate
	}
	return cmdlib.ShowUnknown
}

// QueryOnlineStreamers returns CAM4 online models
func (c *Cam4Checker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	client := c.ClientsLoop.NextClient()
	streamers := map[string]cmdlib.StreamerInfo{}
	resp, buf, err := cmdlib.OnlineQuery(c.UsersOnlineEndpoints[0], client, c.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	var parsed []cam4Model
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed {
		modelID := strings.ToLower(m.Nickname)
		viewers := m.Viewers
		streamers[modelID] = cmdlib.StreamerInfo{
			ImageURL: m.ThumbBig,
			Viewers:  &viewers,
			ShowKind: cam4ShowKind(m.ShowType),
		}
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *Cam4Checker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// UsesFixedList returns false for online list checkers
func (c *Cam4Checker) UsesFixedList() bool { return false }
