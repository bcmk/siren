package lib

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

type bongacamsModel struct {
	Username      string `json:"username"`
	ProfileImages struct {
		ThumbnailImageMediumLive string `json:"thumbnail_image_medium_live"`
	} `json:"profile_images"`
}

// CheckModelBongaCams checks BongaCams model status
func CheckModelBongaCams(client *Client, modelID string, headers [][2]string, dbg bool, _ map[string]string) StatusKind {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://en.bongacams.com/%s", modelID), nil)
	CheckErr(err)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := client.Client.Do(req)
	if err != nil {
		Lerr("[%v] cannot send a query, %v", client.Addr, err)
		return StatusUnknown
	}
	CheckErr(resp.Body.Close())
	if dbg {
		Ldbg("query status for %s: %d", modelID, resp.StatusCode)
	}
	switch resp.StatusCode {
	case 200:
		return StatusOnline
	case 302:
		return StatusOffline
	case 404:
		return StatusNotFound
	}
	return StatusUnknown
}

// BongaCamsOnlineAPI returns BongaCams online models
func BongaCamsOnlineAPI(
	endpoint string,
	client *Client,
	headers [][2]string,
	dbg bool,
	_ map[string]string,
) (
	onlineModels map[string]OnlineModel,
	err error,
) {
	onlineModels = map[string]OnlineModel{}
	resp, buf, err := onlineQuery(endpoint, client, headers)
	if err != nil {
		return nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	var parsed []bongacamsModel
	err = decoder.Decode(&parsed)
	if err != nil {
		if dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}

	if len(parsed) == 0 {
		return nil, errors.New("zero online models reported")
	}

	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = OnlineModel{ModelID: modelID, Image: "https:" + m.ProfileImages.ThumbnailImageMediumLive}
	}
	return
}

// StartBongaCamsChecker starts a checker for Chaturbate
func StartBongaCamsChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
	specificConfig map[string]string,
) (
	statusRequests chan StatusRequest,
	output chan []OnlineModel,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return StartChecker(CheckModelBongaCams, BongaCamsOnlineAPI, usersOnlineEndpoint, clients, headers, intervalMs, dbg, specificConfig)
}
