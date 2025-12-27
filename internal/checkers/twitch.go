package checkers

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bcmk/siren/lib/cmdlib"
	"github.com/nicklaw5/helix"
)

// TwitchChecker implements a checker for Twitch
type TwitchChecker struct {
	cmdlib.CheckerCommon
}

var _ cmdlib.Checker = &TwitchChecker{}

// TwitchModelIDRegexp is a regular expression to check model IDs
var TwitchModelIDRegexp = regexp.MustCompile(`^@?[a-z0-9][a-z0-9\-_]*$`)

// TwitchCanonicalModelID preprocesses channel string to canonical form
func TwitchCanonicalModelID(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "@"))
}

// CheckStatusSingle checks Twitch channel status
func (c *TwitchChecker) CheckStatusSingle(modelID string) cmdlib.StatusKind {
	client := c.ClientsLoop.NextClient()
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
		HTTPClient:   client.Client,
	})
	if err != nil {
		cmdlib.Lerr("cannot create new twitch client, %v", err)
		return cmdlib.StatusUnknown
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		cmdlib.Lerr("%v", err)
		return cmdlib.StatusUnknown
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	streamsResponse, err := helixClient.GetStreams(&helix.StreamsParams{UserLogins: []string{modelID}})
	if err != nil {
		cmdlib.Lerr("negotiation error on getting streams, %v", err)
		return cmdlib.StatusUnknown
	}
	if streamsResponse.ErrorMessage != "" {
		cmdlib.Lerr("Twitch returns the error on getting streams, %s", streamsResponse.ErrorMessage)
		return cmdlib.StatusUnknown
	}
	if len(streamsResponse.Data.Streams) == 1 {
		return cmdlib.StatusOnline
	}

	chanResponse, err := helixClient.GetUsers(&helix.UsersParams{
		Logins: []string{modelID},
	})
	if err != nil {
		cmdlib.Lerr("negotiation error on getting users, %v", err)
		return cmdlib.StatusUnknown
	}
	if chanResponse.ErrorMessage != "" {
		cmdlib.Lerr("Twitch returns the error on getting users, %s", streamsResponse.ErrorMessage)
		return cmdlib.StatusUnknown
	}
	if len(chanResponse.Data.Users) == 1 {
		return cmdlib.StatusOffline
	}
	return cmdlib.StatusNotFound
}

// CheckStatusesMany checks Twitch channel status
func (c *TwitchChecker) CheckStatusesMany(channels cmdlib.QueryModelList, checkMode cmdlib.CheckMode) (results map[string]cmdlib.StatusKind, images map[string]string, err error) {
	client := c.ClientsLoop.NextClient()
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
		HTTPClient:   client.Client,
	})
	if err != nil {
		return nil, nil, err
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		return nil, nil, err
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	if checkMode == cmdlib.CheckOnline && channels.All {
		return cmdlib.CheckEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
	} else if checkMode == cmdlib.CheckOnline {
		return c.checkOnlineMany(helixClient, channels.List)
	}

	return c.checkExistingMany(helixClient, channels.List)
}

func thumbnail(s string) string {
	s = strings.Replace(s, "{width}", "1280", 1)
	return strings.Replace(s, "{height}", "720", 1)
}

func (c *TwitchChecker) checkOnlineMany(client *helix.Client, channels []string) (online map[string]cmdlib.StatusKind, images map[string]string, err error) {
	online = map[string]cmdlib.StatusKind{}
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
			online[name] = cmdlib.StatusOnline
			images[name] = thumbnail(s.ThumbnailURL)
		}
	}
	return online, images, nil
}

func (c *TwitchChecker) checkExistingMany(helixClient *helix.Client, channels []string) (results map[string]cmdlib.StatusKind, images map[string]string, err error) {
	results = map[string]cmdlib.StatusKind{}
	for _, c := range channels {
		results[c] = cmdlib.StatusNotFound
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
			results[u.Login] = cmdlib.StatusOnline | cmdlib.StatusOffline
		}
	}
	return results, images, nil
}

// CheckEndpoint returns all Twitch online channels
func (c *TwitchChecker) CheckEndpoint(string) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
	httpClient := c.ClientsLoop.NextClient()
	onlineModels = map[string]cmdlib.StatusKind{}
	images = map[string]string{}
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
		HTTPClient:   httpClient.Client,
	})
	if err != nil {
		return nil, nil, err
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		return nil, nil, err
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	after := ""
	for {
		streamsResponse, err := helixClient.GetStreams(&helix.StreamsParams{
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
			onlineModels[name] = cmdlib.StatusOnline
			images[name] = thumbnail(s.ThumbnailURL)
		}
		if len(streamsResponse.Data.Streams) == 0 {
			break
		}
		after = streamsResponse.Data.Pagination.Cursor
	}
	return onlineModels, images, nil
}

func requestAppAccessToken(helixClient *helix.Client) (*helix.AppAccessTokenResponse, error) {
	accessResponse, err := helixClient.RequestAppAccessToken(nil)
	if err != nil {
		return nil, fmt.Errorf("negotiation error on requesting an access token, %w", err)
	}
	if accessResponse.ErrorMessage != "" {
		return nil, fmt.Errorf("Twitch returns an error on requesting an access token, %s", accessResponse.ErrorMessage) //nolint:staticcheck
	}
	return accessResponse, nil
}

// Start starts a daemon
func (c *TwitchChecker) Start() { c.StartSelectiveCheckerDaemon(c) }

// CreateUpdater creates an updater
func (c *TwitchChecker) CreateUpdater() cmdlib.Updater { return c.CreateSelectiveUpdater(c) }
