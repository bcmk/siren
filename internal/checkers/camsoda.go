package checkers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bcmk/siren/lib/cmdlib"
)

// CamSodaChecker implements a checker for CamSoda
type CamSodaChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &CamSodaChecker{}

type camSodaOnlineResponse struct {
	Status  bool
	Error   string
	Results []struct {
		Username string
		Status   string
		Thumb    string
		Viewers  int
		Subject  string
	}
}

func camSodaShowKind(status string) cmdlib.ShowKind {
	switch status {
	case "online":
		return cmdlib.ShowPublic
	case "limited":
		return cmdlib.ShowTicket
	case "private":
		return cmdlib.ShowPrivate
	}
	return cmdlib.ShowUnknown
}

// CheckStatusSingle checks CamSoda model status
func (c *CamSodaChecker) CheckStatusSingle(modelID string) cmdlib.StatusKind {
	code := c.QueryStatusCode(fmt.Sprintf("https://www.camsoda.com/%s", modelID))
	switch code {
	case 200:
		return cmdlib.StatusOnline | cmdlib.StatusOffline
	case 404:
		return cmdlib.StatusNotFound
	}
	return cmdlib.StatusUnknown
}

// QueryOnlineChannels returns CamSoda online models
func (c *CamSodaChecker) QueryOnlineChannels() (map[string]cmdlib.ChannelInfo, error) {
	client := c.ClientsLoop.NextClient()
	channels := map[string]cmdlib.ChannelInfo{}
	resp, buf, err := cmdlib.OnlineQuery(c.UsersOnlineEndpoints[0], client, c.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.NopCloser(bytes.NewReader(buf.Bytes())))
	var parsed camSodaOnlineResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if !parsed.Status {
		return nil, fmt.Errorf("API error, %s", parsed.Error)
	}
	for _, m := range parsed.Results {
		modelID := strings.ToLower(m.Username)
		viewers := m.Viewers
		channels[modelID] = cmdlib.ChannelInfo{
			ImageURL: m.Thumb,
			Viewers:  &viewers,
			ShowKind: camSodaShowKind(m.Status),
			Subject:  m.Subject,
		}
	}
	return channels, nil
}

// QueryFixedListOnlineChannels is not implemented for online list checkers
func (c *CamSodaChecker) QueryFixedListOnlineChannels([]string, cmdlib.CheckMode) (map[string]cmdlib.ChannelInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// UsesFixedList returns false for online list checkers
func (c *CamSodaChecker) UsesFixedList() bool { return false }

// SubjectSupported returns true for CamSoda
func (c *CamSodaChecker) SubjectSupported() bool { return true }
