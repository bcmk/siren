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

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// KickCheckerConfig holds Kick OAuth credentials.
type KickCheckerConfig struct {
	BaseCheckerConfig `mapstructure:",squash"`
	ClientID          cmdlib.Secret `mapstructure:"client_id"`
	ClientSecret      cmdlib.Secret `mapstructure:"client_secret"`
}

func (c *KickCheckerConfig) validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	if c.ClientID == "" {
		return errors.New("configure client_id")
	}
	if c.ClientSecret == "" {
		return errors.New("configure client_secret")
	}
	return nil
}

// kickProfileImageTTL is how long the checker caches a profile image before looking it up again
const kickProfileImageTTL = time.Hour

// kickLookupPause is how long the checker stops looking up profile images after a lookup fails
const kickLookupPause = 5 * time.Minute

// kickUsersPerRequest is how many ids a users lookup takes.
// Kick refuses 51 with a 400, and documents the limit only for channels.
const kickUsersPerRequest = 50

// KickChecker implements a checker for Kick
type KickChecker struct {
	BaseChecker[*KickCheckerConfig]
	// profileImages caches the profile images looked up, by broadcaster user id
	profileImages map[int64]kickProfileImage
	// lookUpAfter is when profile image lookups resume after one failed
	lookUpAfter time.Time
}

type kickProfileImage struct {
	url     string
	expires time.Time
}

var _ Checker = &KickChecker{}

// Site returns the site name.
func (*KickChecker) Site() string { return "kick" }

// Init loads kick-checker.json.
func (c *KickChecker) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &KickCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	c.profileImages = map[int64]kickProfileImage{}
	return nil
}

// KickChannelIDRegexp is a regular expression to check channel IDs
var KickChannelIDRegexp = regexp.MustCompile(`^@?[a-z0-9][a-z0-9\-_]*$`)

type kickTokenResponse struct {
	AccessToken string `json:"access_token"`
}

type kickStream struct {
	IsLive      bool   `json:"is_live"`
	ViewerCount int    `json:"viewer_count"`
	Thumbnail   string `json:"thumbnail"`
}

type kickChannel struct {
	Slug              string      `json:"slug"`
	BroadcasterUserID int64       `json:"broadcaster_user_id"`
	StreamTitle       string      `json:"stream_title"`
	Stream            *kickStream `json:"stream"`
}

type kickChannelsResponse struct {
	Data []kickChannel `json:"data"`
}

type kickUser struct {
	UserID         int64  `json:"user_id"`
	ProfilePicture string `json:"profile_picture"`
}

type kickUsersResponse struct {
	Data []kickUser `json:"data"`
}

// NicknamePreprocessing preprocesses nickname to canonical form
func (c *KickChecker) NicknamePreprocessing(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "@"))
}

// NicknameRegexp returns the regular expression to validate channel IDs
func (c *KickChecker) NicknameRegexp() *regexp.Regexp {
	return KickChannelIDRegexp
}

func (c *KickChecker) requestAccessToken(httpClient *http.Client) (string, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {string(c.Cfg.ClientID)},
		"client_secret": {string(c.Cfg.ClientSecret)},
	}
	resp, err := httpClient.Post(
		"https://id.kick.com/oauth/token",
		"application/x-www-form-urlencoded",
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("requesting kick access token, %w", err)
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kick token request status %d", resp.StatusCode)
	}
	var tokenResp kickTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding kick token response, %w", err)
	}
	return tokenResp.AccessToken, nil
}

func (c *KickChecker) queryChannels(
	httpClient *http.Client,
	token string,
	slugs []string,
) ([]kickChannel, error) {
	params := url.Values{}
	for _, s := range slugs {
		params.Add("slug", s)
	}
	var channelsResp kickChannelsResponse
	if err := queryKickAPI(httpClient, token, "channels", params, &channelsResp); err != nil {
		return nil, err
	}
	return channelsResp.Data, nil
}

func (c *KickChecker) queryUsers(httpClient *http.Client, token string, ids []int64) ([]kickUser, error) {
	params := url.Values{}
	for _, id := range ids {
		params.Add("id", strconv.FormatInt(id, 10))
	}
	var usersResp kickUsersResponse
	if err := queryKickAPI(httpClient, token, "users", params, &usersResp); err != nil {
		return nil, err
	}
	return usersResp.Data, nil
}

// queryKickAPI decodes the answer of a public API endpoint, such as channels, into out
func queryKickAPI(httpClient *http.Client, token, endpoint string, params url.Values, out any) error {
	reqURL := "https://api.kick.com/public/v1/" + endpoint + "?" + params.Encode()
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("creating kick request, %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("querying kick %s, %w", endpoint, err)
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kick %s API status %d", endpoint, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding kick %s response, %w", endpoint, err)
	}
	return nil
}

// lookUpProfileImages caches the channels' profile images, looking up those not cached,
// and refreshing a request's worth of the expired ones.
// Expired ones serve for up to another TTL. A failed lookup pauses lookups.
func (c *KickChecker) lookUpProfileImages(token string, channels []kickChannel) {
	now := time.Now()
	maps.DeleteFunc(c.profileImages, func(_ int64, p kickProfileImage) bool {
		return !now.Before(p.expires.Add(kickProfileImageTTL))
	})
	if now.Before(c.lookUpAfter) {
		return
	}
	var missing, expired []int64
	for _, ch := range channels {
		switch p, ok := c.profileImages[ch.BroadcasterUserID]; {
		case !ok:
			missing = append(missing, ch.BroadcasterUserID)
		case !now.Before(p.expires):
			expired = append(expired, ch.BroadcasterUserID)
		}
	}
	due := append(missing, expired[:min(len(expired), kickUsersPerRequest)]...)
	for chunk := range slices.Chunk(due, kickUsersPerRequest) {
		users, err := c.queryUsers(c.Client, token, chunk)
		if err != nil {
			cmdlib.Lerr("cannot look up profile images, %v", err)
			c.lookUpAfter = now.Add(kickLookupPause)
			return
		}
		pictures := map[int64]string{}
		for _, u := range users {
			pictures[u.UserID] = u.ProfilePicture
		}
		// An id the answer leaves out is cached too, so it is not asked again on every check
		for _, id := range chunk {
			c.profileImages[id] = kickProfileImage{
				url:     kickProfileImageURL(pictures[id]),
				expires: now.Add(kickProfileImageTTL),
			}
		}
	}
}

// kickProfileImageURL returns a profile picture, empty for Kick's shared default avatars
func kickProfileImageURL(picture string) string {
	if strings.Contains(picture, "/img/default-profile-pictures/") {
		return ""
	}
	return picture
}

// QueryStreamer checks Kick channel status
func (c *KickChecker) QueryStreamer(channelID string, _ Extras) (cmdlib.StreamerInfoWithStatus, error) {
	token, err := c.requestAccessToken(c.Client)
	if err != nil {
		cmdlib.Lerr("%v", err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	channels, err := c.queryChannels(c.Client, token, []string{channelID})
	if err != nil {
		cmdlib.Lerr("%v", err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if len(channels) == 0 {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}, nil
	}
	ch := channels[0]
	if ch.Stream != nil && ch.Stream.IsLive {
		viewers := ch.Stream.ViewerCount
		return cmdlib.StreamerInfoWithStatus{
			StreamerInfo: cmdlib.StreamerInfo{
				ImageURL: ch.Stream.Thumbnail,
				Viewers:  &viewers,
				Subject:  ch.StreamTitle,
			},
			Status: cmdlib.StatusOnline,
		}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOffline}, nil
}

// QueryOnlineStreamers returns all online Kick channels
func (c *KickChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

// QueryFixedListOnlineStreamers returns statuses for specific Kick channels
func (c *KickChecker) QueryFixedListOnlineStreamers(
	channelIDs []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.StreamerInfo, error) {
	token, err := c.requestAccessToken(c.Client)
	if err != nil {
		return nil, err
	}
	var live []kickChannel
	// Kick API allows up to 50 slugs per request
	for _, chunk := range chunks(channelIDs, 50) {
		channels, err := c.queryChannels(c.Client, token, chunk)
		if err != nil {
			return nil, err
		}
		for _, ch := range channels {
			if ch.Stream != nil && ch.Stream.IsLive {
				live = append(live, ch)
			}
		}
	}
	c.lookUpProfileImages(token, live)
	result := map[string]cmdlib.StreamerInfo{}
	for _, ch := range live {
		viewers := ch.Stream.ViewerCount
		result[strings.ToLower(ch.Slug)] = cmdlib.StreamerInfo{
			ImageURL:        ch.Stream.Thumbnail,
			ProfileImageURL: c.profileImages[ch.BroadcasterUserID].url,
			Viewers:         &viewers,
			Subject:         ch.StreamTitle,
		}
	}
	return result, nil
}

// QueryFixedListStatuses checks if specific Kick channels exist
func (c *KickChecker) QueryFixedListStatuses(
	channelIDs []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.StreamerInfoWithStatus, error) {
	token, err := c.requestAccessToken(c.Client)
	if err != nil {
		return nil, err
	}
	result := map[string]cmdlib.StreamerInfoWithStatus{}
	for _, ch := range channelIDs {
		result[ch] = cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}
	}
	// Kick API allows up to 50 slugs per request
	for _, chunk := range chunks(channelIDs, 50) {
		channels, err := c.queryChannels(c.Client, token, chunk)
		if err != nil {
			return nil, err
		}
		for _, ch := range channels {
			slug := strings.ToLower(ch.Slug)
			result[slug] = cmdlib.StreamerInfoWithStatus{
				Status: cmdlib.StatusOnline | cmdlib.StatusOffline,
			}
		}
	}
	return result, nil
}

// Capabilities lists the surfaces Kick exposes for dispatch.
func (*KickChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          false,
		SupportsQueryFixedListOnlineStreamers: true,
		SupportsQueryFixedListStatuses:        true,
		SupportsQueryStreamer:                 true,
		SupportsQueryStreamerTimes:            false,
		SupportsCLI:                           true,
		SupportsSubject:                       true,
	}
}
