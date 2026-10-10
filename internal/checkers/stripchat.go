package checkers

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// StripchatCheckerConfig holds the user_id and online URL.
type StripchatCheckerConfig struct {
	BaseCheckerConfig   `mapstructure:",squash"`
	UsersOnlineEndpoint string        `mapstructure:"users_online_endpoint"`
	UserID              cmdlib.Secret `mapstructure:"user_id"`
	Headers             [][2]string   `mapstructure:"headers"`
}

func (c *StripchatCheckerConfig) validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	if c.UserID == "" {
		return errors.New("configure user_id")
	}
	if c.UsersOnlineEndpoint == "" {
		return errors.New("configure users_online_endpoint")
	}
	return nil
}

// StripchatChecker implements a checker for Stripchat
type StripchatChecker struct {
	BaseChecker[*StripchatCheckerConfig]
}

var _ Checker = &StripchatChecker{}

// Site returns the site name.
func (*StripchatChecker) Site() string { return "stripchat" }

// Init loads stripchat-checker.json.
func (c *StripchatChecker) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &StripchatCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	return nil
}

type stripchatModel struct {
	Username     string `json:"username"`
	SnapshotURL  string `json:"snapshotUrl"`
	Status       string `json:"status"`
	ViewersCount int    `json:"viewersCount"`
}

type stripchatResponse struct {
	Total  int              `json:"total"`
	Models []stripchatModel `json:"models"`
}

type onlineModel struct {
	Username string `json:"username"`
}

type onlineResponse struct {
	Models map[string]onlineModel `json:"models"`
}

type stripchatUserIDResponse struct {
	ID          int64  `json:"id"`
	NewUsername string `json:"newUsername"`
}

type stripchatUserResponse struct {
	Item struct {
		IsModel   bool `json:"isModel"`
		IsDeleted bool `json:"isDeleted"`
		IsBlocked bool `json:"isBlocked"`
	} `json:"item"`
}

type stripchatStatusesResponse struct {
	Statuses []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	} `json:"statuses"`
}

func stripchatShowKind(status string) cmdlib.ShowKind {
	switch status {
	case "public":
		return cmdlib.ShowPublic
	case "groupShow":
		return cmdlib.ShowGroup
	case "p2p", "p2pVoice", "private", "virtualPrivate":
		return cmdlib.ShowPrivate
	}
	return cmdlib.ShowUnknown
}

// QueryStatus checks Stripchat model status
func (c *StripchatChecker) QueryStatus(modelID string) (cmdlib.StreamerInfoWithStatus, error) {
	// Look up the model's ID, then its status among live models
	unknown := cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}
	notFound := cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}

	endpoint := "https://stripchat.com/api/front/users/user-ids/" + url.PathEscape(modelID)
	resp, buf, err := cmdlib.OnlineQuery(endpoint, c.Client, c.Cfg.Headers)
	if err != nil {
		cmdlib.Lerr("cannot query status: model = %s, url = %s, %v", modelID, endpoint, err)
		return unknown, nil
	}
	cmdlib.Ldbg("query status: url = %s, status = %d", endpoint, resp.StatusCode)
	switch resp.StatusCode {
	case 404:
		return notFound, nil
	case 200:
	default:
		cmdlib.Lerr("unexpected query status: model = %s, url = %s, status = %d", modelID, endpoint, resp.StatusCode)
		return unknown, nil
	}
	user := &stripchatUserIDResponse{}
	if err := json.Unmarshal(buf.Bytes(), user); err != nil {
		cmdlib.Lerr("cannot parse response: model = %s, url = %s, %v", modelID, endpoint, err)
		cmdlib.Ldbg("response: %s", buf.String())
		return unknown, nil
	}
	// An old nickname of a renamed model resolves to the renamed account
	if user.NewUsername != "" {
		return notFound, nil
	}

	// min_request_interval_ms paces each request, not just each status query
	time.Sleep(c.Cfg.MinRequestInterval())
	endpoint = "https://stripchat.com/api/front/models/get-statuses"
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(fmt.Sprintf(`{"ids":[%d]}`, user.ID)))
	cmdlib.CheckErr(err)
	for _, h := range c.Cfg.Headers {
		req.Header.Set(h[0], h[1])
	}
	req.Header.Set("Content-Type", "application/json")
	resp, buf, err = cmdlib.OnlineRequest(req, c.Client)
	if err != nil {
		cmdlib.Lerr("cannot query status: model = %s, url = %s, %v", modelID, endpoint, err)
		return unknown, nil
	}
	cmdlib.Ldbg("query status: url = %s, status = %d", endpoint, resp.StatusCode)
	if resp.StatusCode != 200 {
		cmdlib.Lerr("unexpected query status: model = %s, url = %s, status = %d", modelID, endpoint, resp.StatusCode)
		return unknown, nil
	}
	statuses := &stripchatStatusesResponse{}
	if err := json.Unmarshal(buf.Bytes(), statuses); err != nil {
		cmdlib.Lerr("cannot parse response: model = %s, url = %s, %v", modelID, endpoint, err)
		cmdlib.Ldbg("response: %s", buf.String())
		return unknown, nil
	}
	for _, s := range statuses.Statuses {
		if s.ID == user.ID {
			return cmdlib.StreamerInfoWithStatus{
				StreamerInfo: cmdlib.StreamerInfo{
					// The CDN serves the latest snapshot for any recent timestamp
					ImageURL: fmt.Sprintf("https://img.doppiocdn.com/thumbs/%d/%d", time.Now().Unix(), user.ID),
					ShowKind: stripchatShowKind(s.Status),
				},
				Status: cmdlib.StatusOnline,
			}, nil
		}
	}
	// Offline and idle models are missing from the statuses, as are deleted ones
	time.Sleep(c.Cfg.MinRequestInterval())
	endpoint = fmt.Sprintf("https://stripchat.com/api/front/v2/users/%d", user.ID)
	resp, buf, err = cmdlib.OnlineQuery(endpoint, c.Client, c.Cfg.Headers)
	if err != nil {
		cmdlib.Lerr("cannot query status: model = %s, url = %s, %v", modelID, endpoint, err)
		return unknown, nil
	}
	cmdlib.Ldbg("query status: url = %s, status = %d", endpoint, resp.StatusCode)
	switch resp.StatusCode {
	case 404:
		return notFound, nil
	case 200:
	default:
		cmdlib.Lerr("unexpected query status: model = %s, url = %s, status = %d", modelID, endpoint, resp.StatusCode)
		return unknown, nil
	}
	profile := &stripchatUserResponse{}
	if err := json.Unmarshal(buf.Bytes(), profile); err != nil {
		cmdlib.Lerr("cannot parse response: model = %s, url = %s, %v", modelID, endpoint, err)
		cmdlib.Ldbg("response: %s", buf.String())
		return unknown, nil
	}
	if !profile.Item.IsModel || profile.Item.IsDeleted || profile.Item.IsBlocked {
		return notFound, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOffline}, nil
}

func (c *StripchatChecker) checkOnlyOnline() (map[string]cmdlib.StreamerInfo, error) {
	endpoint := c.Cfg.UsersOnlineEndpoint
	userID := string(c.Cfg.UserID)
	streamers := map[string]cmdlib.StreamerInfo{}

	request, err := url.Parse(endpoint + "/online")
	if err != nil {
		return nil, fmt.Errorf("cannot parse endpoint %q", endpoint)
	}

	q := request.Query()
	q.Set("userId", userID)

	request.RawQuery = q.Encode()

	resp, buf, err := cmdlib.OnlineQuery(request.String(), c.Client, c.Cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status %d", resp.StatusCode)
	}
	parsed := &onlineResponse{}
	err = json.Unmarshal(buf.Bytes(), parsed)
	if err != nil {
		cmdlib.Ldbg("response: %s", buf.String())
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	cmdlib.Ldbg("models count in the response: %d", len(parsed.Models))
	for _, m := range parsed.Models {
		if m.Username != "" {
			modelID := strings.ToLower(m.Username)
			if _, ok := streamers[modelID]; !ok {
				streamers[modelID] = cmdlib.StreamerInfo{}
			}
		}
	}
	return streamers, nil
}

// QueryOnlineStreamers returns Stripchat online models
func (c *StripchatChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	endpoint := c.Cfg.UsersOnlineEndpoint
	streamers, err := c.checkOnlyOnline()
	if err != nil {
		return nil, fmt.Errorf("cannot check online models, %v", err)
	}
	// This is the actual limit, although the documentation states 1000
	limitK := 400
	chunkIter := slices.Chunk(slices.Collect(maps.Keys(streamers)), limitK)
	userID := string(c.Cfg.UserID)
	for chunk := range chunkIter {
		modelIDs := strings.Join(chunk, ",")

		request, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("cannot parse endpoint %q", endpoint)
		}

		q := request.Query()
		q.Set("userId", userID)
		q.Set("modelsList", modelIDs)
		q.Set("strict", "1")
		q.Set("limit", strconv.Itoa(len(chunk)))

		request.RawQuery = q.Encode()

		resp, buf, err := cmdlib.OnlineQuery(request.String(), c.Client, c.Cfg.Headers)
		if err != nil {
			return nil, fmt.Errorf("cannot send a query, %v", err)
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("query status %d", resp.StatusCode)
		}
		parsed := &stripchatResponse{}
		err = json.Unmarshal(buf.Bytes(), parsed)
		if err != nil {
			cmdlib.Ldbg("response: %s", buf.String())
			return nil, fmt.Errorf("cannot parse response, %v", err)
		}
		cmdlib.Ldbg("models count in the response: %d", len(parsed.Models))
		for _, m := range parsed.Models {
			if m.Username != "" {
				modelID := strings.ToLower(m.Username)
				viewers := m.ViewersCount
				streamers[modelID] = cmdlib.StreamerInfo{
					ImageURL: m.SnapshotURL,
					Viewers:  &viewers,
					ShowKind: stripchatShowKind(m.Status),
				}
			}
		}
	}
	if len(streamers) == 0 {
		return nil, errors.New("zero online models reported")
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *StripchatChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

// Capabilities lists the surfaces Stripchat exposes for dispatch.
func (*StripchatChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           true,
		SupportsCustomAffiliateLink:           true,
	}
}

// stripchatReferrerParam names who an affiliate link credits when it carries no affiliate ID.
// stripchatStreamerReferrer is both the keyword a chat sends and the value the link carries:
// the redirect behind the affiliate base turns it into the site's referral form.
const (
	stripchatReferrerParam    = "referrer"
	stripchatStreamerReferrer = "streamer"
)

// stripchatUserIDParam is the affiliate identity a StripCash link must carry.
const stripchatUserIDParam = "userId"

// The fields kept beside the identity; anything else the pasted link carries is dropped.
// An affiliate names these, so they hold arbitrary text and only their length is bounded:
// the query encodes whatever they say, and the link escapes it.
var stripchatAffiliateParams = []string{"campaignId", "creativeId", "sourceId", "memberId"}

const stripchatAffiliateParamValueMaxLen = 256

var stripchatUserIDRegexp = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// ParseAffiliateParams reads either identity a chat can claim: the streamer keyword,
// which credits the model an alert names through the follow-me link Stripchat pays them for,
// or the Final url the StripCash links builder writes.
func (*StripchatChecker) ParseAffiliateParams(input string) (map[string]string, bool) {
	input = strings.TrimSpace(input)
	if strings.EqualFold(input, stripchatStreamerReferrer) {
		return map[string]string{stripchatReferrerParam: stripchatStreamerReferrer}, true
	}
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, false
	}
	query := u.Query()
	userID := query.Get(stripchatUserIDParam)
	if !stripchatUserIDRegexp.MatchString(userID) {
		return nil, false
	}
	out := map[string]string{stripchatUserIDParam: userID}
	for _, name := range stripchatAffiliateParams {
		value := query.Get(name)
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > stripchatAffiliateParamValueMaxLen {
			return nil, false
		}
		out[name] = value
	}
	return out, true
}

// AffiliateID returns the userId, the StripCash ID to show back.
// A chat crediting the streamer has none.
func (*StripchatChecker) AffiliateID(params map[string]string) string {
	return params[stripchatUserIDParam]
}
