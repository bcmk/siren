package lib

import (
	"errors"

	"github.com/nicklaw5/helix"
)

// CheckChannelTwitch checks Twitch channel status
func CheckChannelTwitch(httpClient *Client, modelID string, headers [][2]string, dbg bool, params map[string]string) StatusKind {
	client, err := helix.NewClient(&helix.Options{
		ClientID:     params["client_id"],
		ClientSecret: params["client_secret"],
		HTTPClient:   httpClient.Client,
	})
	if err != nil {
		return StatusUnknown
	}
	accessResponse, err := client.RequestAppAccessToken(nil)
	if err != nil {
		return StatusUnknown
	}
	if accessResponse.ErrorMessage != "" {
		return StatusUnknown
	}

	client.SetAppAccessToken(accessResponse.Data.AccessToken)

	streamsResponse, err := client.GetStreams(&helix.StreamsParams{UserLogins: []string{modelID}})
	if err != nil {
		return StatusUnknown
	}
	if streamsResponse.ErrorMessage != "" {
		return StatusUnknown
	}
	if len(streamsResponse.Data.Streams) == 1 {
		return StatusOnline
	}

	chanResponse, err := client.GetUsers(&helix.UsersParams{
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

// TwitchOnlineAPI returns Twitch online models
func TwitchOnlineAPI(
	endpoint string,
	httpClient *Client,
	headers [][2]string,
	dbg bool,
	params map[string]string,
) (
	onlineModels map[string]OnlineModel,
	err error,
) {
	onlineModels = map[string]OnlineModel{}
	client, err := helix.NewClient(&helix.Options{
		ClientID:     params["client_id"],
		ClientSecret: params["client_secret"],
		HTTPClient:   httpClient.Client,
	})
	if err != nil {
		return nil, err
	}
	accessResponse, err := client.RequestAppAccessToken(nil)
	if err != nil {
		return nil, err
	}
	if accessResponse.ErrorMessage != "" {
		return nil, errors.New(accessResponse.ErrorMessage)
	}

	client.SetAppAccessToken(accessResponse.Data.AccessToken)

	after := ""
	for {
		streamsResponse, err := client.GetStreams(&helix.StreamsParams{
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
			onlineModels[s.UserLogin] = OnlineModel{ModelID: s.UserLogin, Image: s.ThumbnailURL}
		}
		if len(streamsResponse.Data.Streams) == 0 {
			break
		}
		after = streamsResponse.Data.Pagination.Cursor
	}
	return onlineModels, nil
}
