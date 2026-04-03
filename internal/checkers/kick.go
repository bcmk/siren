package checkers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// KickChecker implements a checker for Kick
type KickChecker struct {
	cmdlib.CheckerCommon
}

var _ cmdlib.Checker = &KickChecker{}

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
	Slug        string      `json:"slug"`
	StreamTitle string      `json:"stream_title"`
	Stream      *kickStream `json:"stream"`
}

type kickChannelsResponse struct {
	Data []kickChannel `json:"data"`
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
		"client_id":     {string(c.SpecificConfig["client_id"])},
		"client_secret": {string(c.SpecificConfig["client_secret"])},
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
	reqURL := "https://api.kick.com/public/v1/channels?" + params.Encode()
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating kick request, %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying kick channels, %w", err)
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kick channels API status %d", resp.StatusCode)
	}
	var channelsResp kickChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&channelsResp); err != nil {
		return nil, fmt.Errorf("decoding kick channels response, %w", err)
	}
	return channelsResp.Data, nil
}

// CheckStatusSingle checks Kick channel status
func (c *KickChecker) CheckStatusSingle(channelID string) (cmdlib.StatusKind, error) {
	client := c.ClientsLoop.NextClient()
	token, err := c.requestAccessToken(client.Client)
	if err != nil {
		cmdlib.Lerr("%v", err)
		return cmdlib.StatusUnknown, nil
	}
	channels, err := c.queryChannels(client.Client, token, []string{channelID})
	if err != nil {
		cmdlib.Lerr("%v", err)
		return cmdlib.StatusUnknown, nil
	}
	if len(channels) == 0 {
		return cmdlib.StatusNotFound, nil
	}
	if channels[0].Stream != nil && channels[0].Stream.IsLive {
		return cmdlib.StatusOnline, nil
	}
	return cmdlib.StatusOffline, nil
}

// QueryOnlineStreamers returns all online Kick channels
func (c *KickChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// QueryFixedListOnlineStreamers returns statuses for specific Kick channels
func (c *KickChecker) QueryFixedListOnlineStreamers(
	channelIDs []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.StreamerInfo, error) {
	client := c.ClientsLoop.NextClient()
	token, err := c.requestAccessToken(client.Client)
	if err != nil {
		return nil, err
	}
	result := map[string]cmdlib.StreamerInfo{}
	// Kick API allows up to 50 slugs per request
	for _, chunk := range chunks(channelIDs, 50) {
		channels, err := c.queryChannels(client.Client, token, chunk)
		if err != nil {
			return nil, err
		}
		for _, ch := range channels {
			if ch.Stream == nil || !ch.Stream.IsLive {
				continue
			}
			slug := strings.ToLower(ch.Slug)
			viewers := ch.Stream.ViewerCount
			result[slug] = cmdlib.StreamerInfo{
				ImageURL: ch.Stream.Thumbnail,
				Viewers:  &viewers,
				Subject:  ch.StreamTitle,
			}
		}
	}
	return result, nil
}

// QueryFixedListStatuses checks if specific Kick channels exist
func (c *KickChecker) QueryFixedListStatuses(
	channelIDs []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.StreamerInfoWithStatus, error) {
	client := c.ClientsLoop.NextClient()
	token, err := c.requestAccessToken(client.Client)
	if err != nil {
		return nil, err
	}
	result := map[string]cmdlib.StreamerInfoWithStatus{}
	for _, ch := range channelIDs {
		result[ch] = cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}
	}
	// Kick API allows up to 50 slugs per request
	for _, chunk := range chunks(channelIDs, 50) {
		channels, err := c.queryChannels(client.Client, token, chunk)
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

// UsesFixedList returns true for fixed list checkers
func (c *KickChecker) UsesFixedList() bool { return true }

// SubjectSupported returns true for Kick
func (c *KickChecker) SubjectSupported() bool { return true }
