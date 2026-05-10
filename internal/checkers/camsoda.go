package checkers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// CamSodaChecker implements a checker for CamSoda
type CamSodaChecker struct {
	BaseChecker[*SimpleCheckerConfig]
}

var _ Checker = &CamSodaChecker{}

// Site returns the site name.
func (*CamSodaChecker) Site() string { return "camsoda" }

// Init loads camsoda-checker.json.
func (c *CamSodaChecker) Init(checkerCfgPath string, dbg bool) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &SimpleCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg, dbg)
	return nil
}

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

// QueryStatus checks CamSoda model status
func (c *CamSodaChecker) QueryStatus(modelID string) (cmdlib.StreamerInfoWithStatus, error) {
	code := c.QueryStatusCode(fmt.Sprintf("https://www.camsoda.com/%s", modelID), c.Cfg.Headers)
	switch code {
	case 200:
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOnline | cmdlib.StatusOffline}, nil
	case 404:
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
}

// QueryOnlineStreamers returns CamSoda online models
func (c *CamSodaChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	streamers := map[string]cmdlib.StreamerInfo{}
	resp, buf, err := cmdlib.OnlineQuery(c.Cfg.UsersOnlineEndpoint, c.Client, c.Cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	var parsed camSodaOnlineResponse
	err = json.Unmarshal(buf.Bytes(), &parsed)
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
		streamers[modelID] = cmdlib.StreamerInfo{
			ImageURL: m.Thumb,
			Viewers:  &viewers,
			ShowKind: camSodaShowKind(m.Status),
			Subject:  m.Subject,
		}
	}
	if len(streamers) == 0 {
		return nil, errors.New("zero online models reported")
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *CamSodaChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

// Capabilities lists the surfaces CamSoda exposes for dispatch.
func (*CamSodaChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           true,
		SupportsSubject:                       true,
	}
}
