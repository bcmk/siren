package checkers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// BongaCamsChecker implements a checker for BongaCams
type BongaCamsChecker struct {
	BaseChecker[*SimpleCheckerConfig]
}

var _ Checker = &BongaCamsChecker{}

// Site returns the site name.
func (*BongaCamsChecker) Site() string { return "bongacams" }

// Init loads bongacams-checker.json.
func (c *BongaCamsChecker) Init(checkerCfgPath string) error {
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

type bongacamsModel struct {
	Username     string `json:"username"`
	MembersCount int    `json:"members_count"`
	ChatTopic    string `json:"chat_topic"`
	// TODO: support chat_topic_ru for Russian users
	ProfileImages struct {
		ThumbnailImageMediumLive string `json:"thumbnail_image_medium_live"`
	} `json:"profile_images"`
}

// QueryStatus checks BongaCams model status
func (c *BongaCamsChecker) QueryStatus(modelID string) (cmdlib.StreamerInfoWithStatus, error) {
	code := c.QueryStatusCode(fmt.Sprintf("https://en.bongacams.com/%s", modelID), c.Cfg.Headers)
	switch code {
	case 200:
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOnline}, nil
	case 302:
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOffline}, nil
	case 404:
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
}

// QueryOnlineStreamers returns BongaCams online models
func (c *BongaCamsChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	streamers := map[string]cmdlib.StreamerInfo{}

	resp, buf, err := cmdlib.OnlineQuery(c.Cfg.UsersOnlineEndpoint, c.Client, c.Cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	var parsed []bongacamsModel
	err = json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		cmdlib.Ldbg("response: %s", buf.String())
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}

	if len(parsed) == 0 {
		return nil, errors.New("zero online models reported")
	}

	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		viewers := m.MembersCount
		streamers[modelID] = cmdlib.StreamerInfo{
			ImageURL: "https:" + m.ProfileImages.ThumbnailImageMediumLive,
			Viewers:  &viewers,
			Subject:  m.ChatTopic,
		}
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *BongaCamsChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

// Capabilities lists the surfaces BongaCams exposes for dispatch.
func (*BongaCamsChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           true,
		SupportsSubject:                       true,
	}
}
