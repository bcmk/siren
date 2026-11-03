package checkers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bcmk/siren/v6/lib/cmdlib"
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
		ThumbnailImageBig        string `json:"thumbnail_image_big"`
	} `json:"profile_images"`
}

// bongacamsImageURL completes a scheme-relative image URL, leaving an empty one empty
func bongacamsImageURL(u string) string {
	if u == "" {
		return ""
	}
	return "https:" + u
}

// bongacamsRoomData is the part of a room's data that names its performer's avatar
type bongacamsRoomData struct {
	PerformerData struct {
		AvatarURL string `json:"avatarUrl"`
	} `json:"performerData"`
}

// bongacamsProfileImage is the profile image the online list names after an avatar's path,
// such as /0f5/0b2/2fc/<hash>_avatars.jpg, empty for none
func bongacamsProfileImage(avatarPath string) string {
	base, ok := strings.CutSuffix(avatarPath, "_avatars.jpg")
	if !ok || base == "" {
		return ""
	}
	return "https://i.bgicdn.com" + base + "_thumb_big.jpg"
}

// queryProfileImage looks up a performer's profile image in their room's data, empty for none
func (c *BongaCamsChecker) queryProfileImage(modelID string) string {
	// min_request_interval_ms paces each request, not just each status query
	time.Sleep(c.Cfg.MinRequestInterval())
	form := url.Values{"method": {"getRoomData"}, "args[]": {modelID, "false"}}
	req, err := http.NewRequest("POST", "https://bongacams.com/tools/amf.php", strings.NewReader(form.Encode()))
	cmdlib.CheckErr(err)
	for _, h := range c.Cfg.Headers {
		req.Header.Set(h[0], h[1])
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := c.Client.Do(req)
	if err != nil {
		cmdlib.Lerr("cannot query the room data: model = %s, %v", modelID, err)
		return ""
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		cmdlib.Lerr("cannot query the room data: model = %s, status = %d", modelID, resp.StatusCode)
		return ""
	}
	var parsed bongacamsRoomData
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		cmdlib.Lerr("cannot parse the room data: model = %s, %v", modelID, err)
		return ""
	}
	return bongacamsProfileImage(parsed.PerformerData.AvatarURL)
}

// QueryStreamer checks BongaCams model status
func (c *BongaCamsChecker) QueryStreamer(modelID string, extras Extras) (cmdlib.StreamerInfoWithStatus, error) {
	code := c.QueryStatusCode(fmt.Sprintf("https://en.bongacams.com/%s", modelID), c.Cfg.Headers)
	switch code {
	case 200:
		info := cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOnline}
		if extras&ExtraImages != 0 {
			info.ProfileImageURL = c.queryProfileImage(modelID)
		}
		return info, nil
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
			ImageURL:        bongacamsImageURL(m.ProfileImages.ThumbnailImageMediumLive),
			ProfileImageURL: bongacamsImageURL(m.ProfileImages.ThumbnailImageBig),
			Viewers:         &viewers,
			Subject:         m.ChatTopic,
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
		SupportsQueryStreamer:                 true,
		SupportsQueryStreamerTimes:            false,
		SupportsCLI:                           true,
		SupportsSubject:                       true,
		SupportsCustomAffiliateLink:           true,
	}
}

// bongacamsAffiliateHostRegexp matches bongacams.com and its numbered mirrors, e.g. bongacams4.com.
var bongacamsAffiliateHostRegexp = regexp.MustCompile(`^(?:[a-z0-9-]+\.)*bongacams\d*\.com$`)

// The two identities a pasted link can carry:
// fuid from a BongaModels member-referral link, c from a BongaCash direct link.
// The direct link's other fields do nothing for us, so only c is kept.
const (
	bongacamsMemberReferralParam = "fuid"
	bongacamsCampaignParam       = "c"
)

var bongacamsAffiliateParamValueRegexp = regexp.MustCompile(`^[0-9]{1,32}$`)

// ParseAffiliateParams reads either link a chat can paste:
// the "To refer members" link from the BongaModels promo tools,
// or the direct link from the BongaCash promo panel.
func (*BongaCamsChecker) ParseAffiliateParams(input string) (map[string]string, bool) {
	u, err := url.Parse(strings.TrimSpace(input))
	if err != nil || u.Scheme != "https" {
		return nil, false
	}
	if !bongacamsAffiliateHostRegexp.MatchString(strings.ToLower(u.Hostname())) {
		return nil, false
	}
	query := u.Query()
	for _, name := range []string{bongacamsMemberReferralParam, bongacamsCampaignParam} {
		value := query.Get(name)
		if value == "" {
			continue
		}
		if !bongacamsAffiliateParamValueRegexp.MatchString(value) {
			return nil, false
		}
		return map[string]string{name: value}, true
	}
	return nil, false
}

// AffiliateID returns the fuid or the campaign, whichever identity the link carries.
func (*BongaCamsChecker) AffiliateID(params map[string]string) string {
	if fuid := params[bongacamsMemberReferralParam]; fuid != "" {
		return fuid
	}
	return params[bongacamsCampaignParam]
}
