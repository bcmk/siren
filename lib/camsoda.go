package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

type camSodaUserResponse struct {
	Status bool
	Error  string
	User   struct {
		Chat struct {
			Status string
		}
	}
}

type camSodaOnlineResponse struct {
	Status  bool
	Error   string
	Results []struct {
		Username string
		Status   string
		Thumb    string
	}
}

// CheckModelCamSoda checks CamSoda model status
func CheckModelCamSoda(client *Client, modelID string, headers [][2]string, dbg bool, _ map[string]string) StatusKind {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://feed.camsoda.com/api/v1/user/%s", modelID), nil)
	CheckErr(err)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := client.Client.Do(req)
	if err != nil {
		Lerr("[%v] cannot send a query, %v", client.Addr, err)
		return StatusUnknown
	}
	defer func() {
		CheckErr(resp.Body.Close())
	}()
	if dbg {
		Ldbg("[%v] query status for %s: %d", client.Addr, modelID, resp.StatusCode)
	}
	if resp.StatusCode == 401 {
		return StatusDenied
	}
	if resp.StatusCode == 404 {
		return StatusNotFound
	}
	buf := bytes.Buffer{}
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		Lerr("[%v] cannot read response for model %s, %v", client.Addr, modelID, err)
		return StatusUnknown
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &camSodaUserResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		Lerr("[%v] cannot parse response for model %s, %v", client.Addr, modelID, err)
		if dbg {
			Ldbg("response: %s", buf.String())
		}
		return StatusUnknown
	}
	if !parsed.Status {
		Lerr("[%v] API error for model %s, %s", client.Addr, modelID, parsed.Error)
		return StatusUnknown
	}
	return camSodaStatus(parsed.User.Chat.Status)
}

func camSodaStatus(roomStatus string) StatusKind {
	switch roomStatus {
	case "online":
		return StatusOnline
	case "limited":
		return StatusOnline
	case "private":
		return StatusOnline
	case "offline":
		return StatusOffline
	}
	Lerr("cannot parse room status \"%s\"", roomStatus)
	return StatusUnknown
}

// CamSodaOnlineAPI returns CamSoda online models
func CamSodaOnlineAPI(
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
	var parsed camSodaOnlineResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if !parsed.Status {
		return nil, fmt.Errorf("API error, %s", parsed.Error)
	}
	for _, m := range parsed.Results {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = OnlineModel{ModelID: modelID, Image: m.Thumb}
	}
	return
}

// StartCamSodaChecker starts a checker for Chaturbate
func StartCamSodaChecker(
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
	return StartChecker(CheckModelCamSoda, CamSodaOnlineAPI, usersOnlineEndpoint, clients, headers, intervalMs, dbg, specificConfig)
}
