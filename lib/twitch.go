package lib

import (
	"errors"
	"regexp"
	"strings"

	"github.com/nicklaw5/helix"
)

// TwitchChecker implements a checker for Twitch
type TwitchChecker struct {
	CheckerCommon
}

var _ Checker = &TwitchChecker{}

// TwitchModelIDRegexp is a regular expression to check model IDs
var TwitchModelIDRegexp = regexp.MustCompile(`^@?[a-z0-9][a-z0-9\-_]*$`)

// TwitchCanonicalModelID preprocesses channel string to canonical form
func TwitchCanonicalModelID(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "@"))
}

// CheckStatusSingle checks Twitch channel status
func (c *TwitchChecker) CheckStatusSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
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

// CheckStatusesMany checks Twitch channel status
func (c *TwitchChecker) CheckStatusesMany(channels QueryModelList, checkMode CheckMode) (results map[string]StatusKind, images map[string]string, err error) {
	client := c.clientsLoop.nextClient()
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
		HTTPClient:   client.Client,
	})
	if err != nil {
		return nil, nil, err
	}
	accessResponse, err := helixClient.RequestAppAccessToken(nil)
	if err != nil {
		return nil, nil, err
	}
	if accessResponse.ErrorMessage != "" {
		return nil, nil, errors.New(accessResponse.ErrorMessage)
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	if checkMode == CheckOnline && channels.all {
		return checkEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
	} else if checkMode == CheckOnline {
		return c.checkOnlineMany(helixClient, channels.list)
	} else {
		return c.checkExistingMany(helixClient, channels.list)
	}
}

func thumbnail(s string) string {
	s = strings.Replace(s, "{width}", "1280", 1)
	return strings.Replace(s, "{height}", "720", 1)
}

func (c *TwitchChecker) checkOnlineMany(client *helix.Client, channels []string) (online map[string]StatusKind, images map[string]string, err error) {
	online = map[string]StatusKind{}
	images = map[string]string{}
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
			online[name] = StatusOnline
			images[name] = thumbnail(s.ThumbnailURL)
		}
	}
	return online, images, nil
}

func (c *TwitchChecker) checkExistingMany(helixClient *helix.Client, channels []string) (results map[string]StatusKind, images map[string]string, err error) {
	results = map[string]StatusKind{}
	for _, c := range channels {
		results[c] = StatusNotFound
	}
	for _, chunk := range chunks(channels, 100) {
		chanResponse, err := helixClient.GetUsers(&helix.UsersParams{
			Logins: chunk,
		})
		if err != nil {
			return nil, nil, err
		}
		if chanResponse.ErrorMessage != "" {
			return nil, nil, errors.New(chanResponse.ErrorMessage)
		}
		for _, u := range chanResponse.Data.Users {
			results[u.Login] = StatusOnline | StatusOffline
		}
	}
	return results, images, nil
}

// checkEndpoint returns all Twitch online channels
func (c *TwitchChecker) checkEndpoint(string) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	httpClient := c.clientsLoop.nextClient()
	onlineModels = map[string]StatusKind{}
	images = map[string]string{}
	client, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
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
			onlineModels[name] = StatusOnline
			images[name] = thumbnail(s.ThumbnailURL)
		}
		if len(streamsResponse.Data.Streams) == 0 {
			break
		}
		after = streamsResponse.Data.Pagination.Cursor
	}
	return onlineModels, images, nil
}

// Start starts a daemon
func (c *TwitchChecker) Start()                 { c.startSelectiveCheckerDaemon(c) }
func (c *TwitchChecker) createUpdater() Updater { return c.createSelectiveUpdater(c) }
