package lib

import (
	"errors"
	"strings"
	"time"

	"github.com/nicklaw5/helix"
)

// TwitchChecker implements a checker for Twitch
type TwitchChecker struct {
	CheckerCommon
}

var _ SelectiveChecker = &TwitchChecker{}
var _ FullChecker = &TwitchChecker{}

// CheckSingle checks Twitch channel status
func (c *TwitchChecker) CheckSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.specificConfig["client_id"],
		ClientSecret: c.specificConfig["client_secret"],
		HTTPClient:   client.Client,
	})
	if err != nil {
		return StatusUnknown
	}
	accessResponse, err := helixClient.RequestAppAccessToken(nil)
	if err != nil {
		return StatusUnknown
	}
	if accessResponse.ErrorMessage != "" {
		return StatusUnknown
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	streamsResponse, err := helixClient.GetStreams(&helix.StreamsParams{UserLogins: []string{modelID}})
	if err != nil {
		return StatusUnknown
	}
	if streamsResponse.ErrorMessage != "" {
		return StatusUnknown
	}
	if len(streamsResponse.Data.Streams) == 1 {
		return StatusOnline
	}

	chanResponse, err := helixClient.GetUsers(&helix.UsersParams{
		Logins: []string{modelID},
	})
	if err != nil {
		return StatusUnknown
	}
	if chanResponse.ErrorMessage != "" {
		return StatusUnknown
	}
	if len(chanResponse.Data.Users) == 1 {
		return StatusOffline
	}
	return StatusNotFound
}

func thumbnail(s string) string {
	s = strings.Replace(s, "{width}", "1280", 1)
	return strings.Replace(s, "{height}", "720", 1)
}

// CheckMany returns Twitch online models
func (c *TwitchChecker) CheckMany(channels []string) (online map[string]bool, images map[string]string, err error) {
	httpClient := c.clientsLoop.nextClient()
	online = map[string]bool{}
	images = map[string]string{}
	client, err := helix.NewClient(&helix.Options{
		ClientID:     c.specificConfig["client_id"],
		ClientSecret: c.specificConfig["client_secret"],
		HTTPClient:   httpClient.Client,
	})
	if err != nil {
		return nil, nil, err
	}
	accessResponse, err := client.RequestAppAccessToken(nil)
	if err != nil {
		return nil, nil, err
	}
	if accessResponse.ErrorMessage != "" {
		return nil, nil, errors.New(accessResponse.ErrorMessage)
	}
	client.SetAppAccessToken(accessResponse.Data.AccessToken)
	for _, chunk := range chunks(channels, 100) {
		streamsResponse, err := client.GetStreams(&helix.StreamsParams{
			First:      100,
			UserLogins: chunk,
		})
		if err != nil {
			return nil, nil, err
		}
		if streamsResponse.ErrorMessage != "" {
			return nil, nil, errors.New(streamsResponse.ErrorMessage)
		}
		for _, s := range streamsResponse.Data.Streams {
			name := strings.ToLower(s.UserLogin)
			online[name] = true
			images[name] = thumbnail(s.ThumbnailURL)
		}
	}
	return online, images, nil
}

// checkEndpoint returns all Twitch online channels on the endpoint
func (c *TwitchChecker) checkEndpoint(endpoint string) (onlineModels map[string]bool, images map[string]string, err error) {
	httpClient := c.clientsLoop.nextClient()
	onlineModels = map[string]bool{}
	images = map[string]string{}
	client, err := helix.NewClient(&helix.Options{
		ClientID:     c.specificConfig["client_id"],
		ClientSecret: c.specificConfig["client_secret"],
		HTTPClient:   httpClient.Client,
	})
	if err != nil {
		return nil, nil, err
	}
	accessResponse, err := client.RequestAppAccessToken(nil)
	if err != nil {
		return nil, nil, err
	}
	if accessResponse.ErrorMessage != "" {
		return nil, nil, errors.New(accessResponse.ErrorMessage)
	}

	client.SetAppAccessToken(accessResponse.Data.AccessToken)

	after := ""
	for {
		streamsResponse, err := client.GetStreams(&helix.StreamsParams{
			First: 100,
			After: after,
		})
		if err != nil {
			return nil, nil, err
		}
		if streamsResponse.ErrorMessage != "" {
			return nil, nil, errors.New(streamsResponse.ErrorMessage)
		}
		for _, s := range streamsResponse.Data.Streams {
			name := strings.ToLower(s.UserLogin)
			onlineModels[name] = true
			images[name] = thumbnail(s.ThumbnailURL)
		}
		if len(streamsResponse.Data.Streams) == 0 {
			break
		}
		after = streamsResponse.Data.Pagination.Cursor
	}
	return onlineModels, images, nil
}

// CheckFull returns Twitch online models
func (c *TwitchChecker) CheckFull() (onlineModels map[string]bool, images map[string]string, err error) {
	return checkEndpoints(c, c.usersOnlineEndpoint, c.dbg)
}

// Start starts a daemon
func (c *TwitchChecker) Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return selectiveDaemonStart(c, siteOnlineModels, subscriptions, dbg)
}
