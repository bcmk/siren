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

// BongaCamsChecker implements a checker for BongaCams
type BongaCamsChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &BongaCamsChecker{}

type bongacamsModel struct {
	Username     string `json:"username"`
	MembersCount int    `json:"members_count"`
	ChatTopic    string `json:"chat_topic"`
	// TODO: support chat_topic_ru for Russian users
	ProfileImages struct {
		ThumbnailImageMediumLive string `json:"thumbnail_image_medium_live"`
	} `json:"profile_images"`
}

// CheckStatusSingle checks BongaCams model status
func (c *BongaCamsChecker) CheckStatusSingle(modelID string) cmdlib.StatusKind {
	code := c.QueryStatusCode(fmt.Sprintf("https://en.bongacams.com/%s", modelID))
	switch code {
	case 200:
		return cmdlib.StatusOnline
	case 302:
		return cmdlib.StatusOffline
	case 404:
		return cmdlib.StatusNotFound
	}
	return cmdlib.StatusUnknown
}

// QueryOnlineChannels returns BongaCams online models
func (c *BongaCamsChecker) QueryOnlineChannels() (map[string]cmdlib.ChannelInfo, error) {
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
	var parsed []bongacamsModel
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			cmdlib.Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}

	if len(parsed) == 0 {
		return nil, errors.New("zero online models reported")
	}

	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		viewers := m.MembersCount
		channels[modelID] = cmdlib.ChannelInfo{
			ImageURL: "https:" + m.ProfileImages.ThumbnailImageMediumLive,
			Viewers:  &viewers,
			Subject:  m.ChatTopic,
		}
	}
	return channels, nil
}

// QueryFixedListOnlineChannels is not implemented for online list checkers
func (c *BongaCamsChecker) QueryFixedListOnlineChannels([]string, cmdlib.CheckMode) (map[string]cmdlib.ChannelInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// UsesFixedList returns false for online list checkers
func (c *BongaCamsChecker) UsesFixedList() bool { return false }

// SubjectSupported returns true for BongaCams
func (c *BongaCamsChecker) SubjectSupported() bool { return true }
