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

type liveJasminModel struct {
	PerformerID       string `json:"performerId"`
	Status            string `json:"status"`
	ProfilePictureURL struct {
		Size896x503 string `json:"size896x504"`
	} `json:"profilePictureUrl"`
}

type liveJasminResponse struct {
	Status    string `json:"status"`
	ErrorCode int    `json:"errorCode"`
	Data      struct {
		Models []liveJasminModel `json:"models"`
	} `json:"data"`
}

// CheckModelLiveJasmin checks LiveJasmin model status
func CheckModelLiveJasmin(client *Client, modelID string, headers [][2]string, dbg bool, config map[string]string) StatusKind {
	psID := config["ps_id"]
	accessKey := config["access_key"]
	url := fmt.Sprintf("https://pt.potawe.com/api/model/status?performerId=%s&psId=%s&accessKey=%s&legacyRedirect=1", modelID, psID, accessKey)
	req, err := http.NewRequest("GET", url, nil)
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
	return liveJasminStatus(buf.String())
}

func liveJasminStatus(roomStatus string) StatusKind {
	switch roomStatus {
	case "free_chat":
		return StatusOnline
	case "member_chat":
		return StatusOnline
	case "members_only":
		return StatusOnline
	case "offline":
		return StatusOffline
	case "invalid":
		return StatusNotFound
	}
	Lerr("cannot parse room status \"%s\"", roomStatus)
	return StatusUnknown
}

// StartLiveJasminAPIChecker starts a checker for LiveJasmin
func StartLiveJasminAPIChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
	config map[string]string,
) (
	statusRequests chan StatusRequest,
	output chan []OnlineModel,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	statusRequests = make(chan StatusRequest)
	output = make(chan []OnlineModel)
	errorsCh = make(chan struct{})
	elapsedCh = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
	requests:
		for range statusRequests {
			hash := map[string]OnlineModel{}
			updates := []OnlineModel{}
			for _, endpoint := range usersOnlineEndpoint {
				client := clients[clientIdx]
				clientIdx++
				if clientIdx == clientsNum {
					clientIdx = 0
				}

				resp, buf, elapsed, err := onlineQuery(endpoint, client, headers)
				elapsedCh <- elapsed
				if err != nil {
					Lerr("[%v] cannot send a query, %v", client.Addr, err)
					errorsCh <- struct{}{}
					continue requests
				}
				if resp.StatusCode != 200 {
					Lerr("[%v] query status, %d", client.Addr, resp.StatusCode)
					errorsCh <- struct{}{}
					continue requests
				}
				decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
				var parsed liveJasminResponse
				err = decoder.Decode(&parsed)
				if err != nil {
					Lerr("[%v] cannot parse response, %v", client.Addr, err)
					if dbg {
						Ldbg("response: %s", buf.String())
					}
					errorsCh <- struct{}{}
					continue requests
				}
				if parsed.Status != "OK" {
					Lerr("[%v] cannot query a list of models, %d", client.Addr, parsed.ErrorCode)
					if dbg {
						Ldbg("response: %s", buf.String())
					}
					errorsCh <- struct{}{}
					continue requests
				}
				for _, m := range parsed.Data.Models {
					modelID := strings.ToLower(m.PerformerID)
					hash[modelID] = OnlineModel{ModelID: modelID, Image: "https:" + m.ProfilePictureURL.Size896x503}
				}
			}
			for _, statusUpdate := range hash {
				updates = append(updates, statusUpdate)
			}
			output <- updates
		}
	}()
	return
}
