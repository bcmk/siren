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

// TwitchChannelIDRegexp is a regular expression to check channel IDs
var TwitchChannelIDRegexp = regexp.MustCompile(`^@?[a-z0-9][a-z0-9\-_]*$`)

// TwitchCanonicalChannelID preprocesses channel string to canonical form
func TwitchCanonicalChannelID(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "@"))
}

// CheckStatusSingle checks Twitch channel status
func (c *TwitchChecker) CheckStatusSingle(channelID string) cmdlib.StatusKind {
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

	streamsResponse, err := helixClient.GetStreams(&helix.StreamsParams{UserLogins: []string{channelID}})
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
		Logins: []string{channelID},
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

// QueryOnlineChannels returns all online Twitch channels
func (c *TwitchChecker) QueryOnlineChannels() (map[string]cmdlib.ChannelInfo, error) {
	client := c.ClientsLoop.NextClient()
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
		HTTPClient:   client.Client,
	})
	if err != nil {
		return nil, err
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		return nil, err
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	return c.checkAllOnline(helixClient)
}

// QueryFixedListOnlineChannels returns statuses for specific Twitch channels
func (c *TwitchChecker) QueryFixedListOnlineChannels(
	channels []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.ChannelInfo, error) {
	client := c.ClientsLoop.NextClient()
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
		HTTPClient:   client.Client,
	})
	if err != nil {
		return nil, err
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		return nil, err
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	return c.checkOnlineMany(helixClient, channels)
}

// QueryFixedListStatuses checks if specific Twitch channels exist
func (c *TwitchChecker) QueryFixedListStatuses(channels []string, _ cmdlib.CheckMode) (map[string]cmdlib.ChannelInfoWithStatus, error) {
	client := c.ClientsLoop.NextClient()
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     c.SpecificConfig["client_id"],
		ClientSecret: c.SpecificConfig["client_secret"],
		HTTPClient:   client.Client,
	})
	if err != nil {
		return nil, err
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		return nil, err
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)
	return c.checkExistingMany(helixClient, channels)
}

func thumbnail(s string) string {
	s = strings.Replace(s, "{width}", "1280", 1)
	return strings.Replace(s, "{height}", "720", 1)
}

func (c *TwitchChecker) checkOnlineMany(client *helix.Client, channels []string) (map[string]cmdlib.ChannelInfo, error) {
	result := map[string]cmdlib.ChannelInfo{}
	for _, chunk := range chunks(channels, 100) {
		streamsResponse, err := client.GetStreams(&helix.StreamsParams{
			First:      100,
			UserLogins: chunk,
		})
		if err != nil {
			return nil, err
		}
		if streamsResponse.ErrorMessage != "" {
			return nil, errors.New(streamsResponse.ErrorMessage)
		}
		for _, s := range streamsResponse.Data.Streams {
			name := strings.ToLower(s.UserLogin)
			viewers := s.ViewerCount
			result[name] = cmdlib.ChannelInfo{
				ImageURL: thumbnail(s.ThumbnailURL),
				Viewers:  &viewers,
				Subject:  s.Title,
			}
		}
	}
	return result, nil
}

func (c *TwitchChecker) checkExistingMany(helixClient *helix.Client, channels []string) (map[string]cmdlib.ChannelInfoWithStatus, error) {
	result := map[string]cmdlib.ChannelInfoWithStatus{}
	for _, ch := range channels {
		result[ch] = cmdlib.ChannelInfoWithStatus{Status: cmdlib.StatusNotFound}
	}
	for _, chunk := range chunks(channels, 100) {
		chanResponse, err := helixClient.GetUsers(&helix.UsersParams{
			Logins: chunk,
		})
		if err != nil {
			return nil, err
		}
		if chanResponse.ErrorMessage != "" {
			return nil, errors.New(chanResponse.ErrorMessage)
		}
		for _, u := range chanResponse.Data.Users {
			result[u.Login] = cmdlib.ChannelInfoWithStatus{Status: cmdlib.StatusOnline | cmdlib.StatusOffline}
		}
	}
	return result, nil
}

func (c *TwitchChecker) checkAllOnline(helixClient *helix.Client) (map[string]cmdlib.ChannelInfo, error) {
	result := map[string]cmdlib.ChannelInfo{}
	after := ""
	for {
		streamsResponse, err := helixClient.GetStreams(&helix.StreamsParams{
			First: 100,
			After: after,
		})
		if err != nil {
			return nil, err
		}
		if streamsResponse.ErrorMessage != "" {
			return nil, errors.New(streamsResponse.ErrorMessage)
		}
		for _, s := range streamsResponse.Data.Streams {
			name := strings.ToLower(s.UserLogin)
			viewers := s.ViewerCount
			result[name] = cmdlib.ChannelInfo{
				ImageURL: thumbnail(s.ThumbnailURL),
				Viewers:  &viewers,
				Subject:  s.Title,
			}
		}
		if len(streamsResponse.Data.Streams) == 0 {
			break
		}
		after = streamsResponse.Data.Pagination.Cursor
	}
	return result, nil
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

// UsesFixedList returns true for fixed list checkers
func (c *TwitchChecker) UsesFixedList() bool { return true }

// SubjectSupported returns true for Twitch
func (c *TwitchChecker) SubjectSupported() bool { return true }
