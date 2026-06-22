package checkers

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bcmk/siren/v3/lib/cmdlib"
	"github.com/nicklaw5/helix/v2"
)

// TwitchCheckerConfig holds Twitch OAuth credentials.
type TwitchCheckerConfig struct {
	BaseCheckerConfig `mapstructure:",squash"`
	ClientID          cmdlib.Secret `mapstructure:"client_id"`
	ClientSecret      cmdlib.Secret `mapstructure:"client_secret"`
}

func (c *TwitchCheckerConfig) validate() error {
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

// TwitchChecker implements a checker for Twitch
type TwitchChecker struct {
	BaseChecker[*TwitchCheckerConfig]
}

var _ Checker = &TwitchChecker{}

// Site returns the site name.
func (*TwitchChecker) Site() string { return "twitch" }

// Init loads twitch-checker.json.
func (c *TwitchChecker) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &TwitchCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	return nil
}

// TwitchChannelIDRegexp is a regular expression to check channel IDs
var TwitchChannelIDRegexp = regexp.MustCompile(`^@?[a-z0-9][a-z0-9\-_]*$`)

// NicknamePreprocessing preprocesses nickname to canonical form
func (c *TwitchChecker) NicknamePreprocessing(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "@"))
}

// NicknameRegexp returns the regular expression to validate channel IDs
func (c *TwitchChecker) NicknameRegexp() *regexp.Regexp {
	return TwitchChannelIDRegexp
}

// QueryStatus checks Twitch channel status
func (c *TwitchChecker) QueryStatus(channelID string) (cmdlib.StreamerInfoWithStatus, error) {
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     string(c.Cfg.ClientID),
		ClientSecret: string(c.Cfg.ClientSecret),
		HTTPClient:   c.Client,
	})
	if err != nil {
		cmdlib.Lerr("cannot create new twitch client, %v", err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		cmdlib.Lerr("%v", err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	streamsResponse, err := helixClient.GetStreams(&helix.StreamsParams{UserLogins: []string{channelID}})
	if err != nil {
		cmdlib.Lerr("negotiation error on getting streams, %v", err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if streamsResponse.ErrorMessage != "" {
		cmdlib.Lerr("Twitch returns the error on getting streams, %s", streamsResponse.ErrorMessage)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if len(streamsResponse.Data.Streams) == 1 {
		s := streamsResponse.Data.Streams[0]
		viewers := s.ViewerCount
		return cmdlib.StreamerInfoWithStatus{
			StreamerInfo: cmdlib.StreamerInfo{
				ImageURL: thumbnail(s.ThumbnailURL),
				Viewers:  &viewers,
				Subject:  s.Title,
			},
			Status: cmdlib.StatusOnline,
		}, nil
	}

	chanResponse, err := helixClient.GetUsers(&helix.UsersParams{
		Logins: []string{channelID},
	})
	if err != nil {
		cmdlib.Lerr("negotiation error on getting users, %v", err)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if chanResponse.ErrorMessage != "" {
		cmdlib.Lerr("Twitch returns the error on getting users, %s", chanResponse.ErrorMessage)
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusUnknown}, nil
	}
	if len(chanResponse.Data.Users) == 1 {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOffline}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}, nil
}

// QueryOnlineStreamers returns all online Twitch channels
func (c *TwitchChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     string(c.Cfg.ClientID),
		ClientSecret: string(c.Cfg.ClientSecret),
		HTTPClient:   c.Client,
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

// QueryFixedListOnlineStreamers returns statuses for specific Twitch channels
func (c *TwitchChecker) QueryFixedListOnlineStreamers(
	channelIDs []string,
	_ cmdlib.CheckMode,
) (map[string]cmdlib.StreamerInfo, error) {
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     string(c.Cfg.ClientID),
		ClientSecret: string(c.Cfg.ClientSecret),
		HTTPClient:   c.Client,
	})
	if err != nil {
		return nil, err
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		return nil, err
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)

	return c.checkOnlineMany(helixClient, channelIDs)
}

// QueryFixedListStatuses checks if specific Twitch channels exist
func (c *TwitchChecker) QueryFixedListStatuses(channelIDs []string, _ cmdlib.CheckMode) (map[string]cmdlib.StreamerInfoWithStatus, error) {
	helixClient, err := helix.NewClient(&helix.Options{
		ClientID:     string(c.Cfg.ClientID),
		ClientSecret: string(c.Cfg.ClientSecret),
		HTTPClient:   c.Client,
	})
	if err != nil {
		return nil, err
	}
	accessResponse, err := requestAppAccessToken(helixClient)
	if err != nil {
		return nil, err
	}

	helixClient.SetAppAccessToken(accessResponse.Data.AccessToken)
	return c.checkExistingMany(helixClient, channelIDs)
}

func thumbnail(s string) string {
	s = strings.Replace(s, "{width}", "1280", 1)
	return strings.Replace(s, "{height}", "720", 1)
}

func (c *TwitchChecker) checkOnlineMany(client *helix.Client, channelIDs []string) (map[string]cmdlib.StreamerInfo, error) {
	result := map[string]cmdlib.StreamerInfo{}
	for _, chunk := range chunks(channelIDs, 100) {
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
			result[name] = cmdlib.StreamerInfo{
				ImageURL: thumbnail(s.ThumbnailURL),
				Viewers:  &viewers,
				Subject:  s.Title,
			}
		}
	}
	return result, nil
}

func (c *TwitchChecker) checkExistingMany(helixClient *helix.Client, channelIDs []string) (map[string]cmdlib.StreamerInfoWithStatus, error) {
	result := map[string]cmdlib.StreamerInfoWithStatus{}
	for _, ch := range channelIDs {
		result[ch] = cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusNotFound}
	}
	for _, chunk := range chunks(channelIDs, 100) {
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
			result[u.Login] = cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOnline | cmdlib.StatusOffline}
		}
	}
	return result, nil
}

func (c *TwitchChecker) checkAllOnline(helixClient *helix.Client) (map[string]cmdlib.StreamerInfo, error) {
	result := map[string]cmdlib.StreamerInfo{}
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
			result[name] = cmdlib.StreamerInfo{
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

// Capabilities lists the surfaces Twitch exposes for dispatch.
func (*TwitchChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          false,
		SupportsQueryFixedListOnlineStreamers: true,
		SupportsQueryFixedListStatuses:        true,
		SupportsQueryStatus:                   true,
		SupportsCLI:                           true,
		SupportsSubject:                       true,
	}
}
