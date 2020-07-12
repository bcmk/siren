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

type bongacamsModel struct {
	Username      string `json:"username"`
	ProfileImages struct {
		ThumbnailImageMediumLive string `json:"thumbnail_image_medium_live"`
	} `json:"profile_images"`
}

// CheckModelBongaCams checks BongaCams model status
func CheckModelBongaCams(client *Client, modelID string, headers [][2]string, dbg bool) StatusKind {
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

var _ = StartBongaCamsAPIChecker

// StartBongaCamsAPIChecker starts a checker for BongaCams
func StartBongaCamsAPIChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
) (
	statusRequests chan StatusRequest,
	statusUpdates chan []StatusUpdate,
	elapsedCh chan time.Duration) {

	statusRequests = make(chan StatusRequest)
	statusUpdates = make(chan []StatusUpdate)
	elapsedCh = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
		for request := range statusRequests {
			hash := map[string]StatusUpdate{}
			updates := []StatusUpdate{}
			for _, endpoint := range usersOnlineEndpoint {
				client := clients[clientIdx]
				clientIdx++
				if clientIdx == clientsNum {
					clientIdx = 0
				}

				resp, buf, elapsed, err := onlineQuery(endpoint, client, headers)
				elapsedCh <- elapsed
				if err != nil {
					statusUpdates <- nil
					Lerr("[%v] cannot send a query, %v", client.Addr, err)
					continue
				}
				if resp.StatusCode != 200 {
					Lerr("[%v] query status, %d", client.Addr, resp.StatusCode)
					statusUpdates <- nil
					continue
				}
				decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
				var parsed []bongacamsModel
				err = decoder.Decode(&parsed)
				if err != nil {
					Lerr("[%v] cannot parse response, %v", client.Addr, err)
					if dbg {
						Ldbg("response: %s", buf.String())
					}
					statusUpdates <- nil
					continue
				}

				for _, m := range parsed {
					modelID := strings.ToLower(m.Username)
					hash[modelID] = StatusUpdate{ModelID: modelID, Status: StatusOnline, Image: m.ProfileImages.ThumbnailImageMediumLive}
				}
			}
			for _, statusUpdate := range hash {
				updates = append(updates, statusUpdate)
			}
			for _, modelID := range request.KnownModels {
				if _, ok := hash[modelID]; !ok {
					updates = append(updates, StatusUpdate{ModelID: modelID, Status: StatusOffline})
				}
			}
			statusUpdates <- updates
		}
	}()
	return
}

// StartBongaCamsPollingChecker starts a checker for BongaCams using redirection heuristic
func StartBongaCamsPollingChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	debug bool,
) (
	statusRequests chan StatusRequest,
	output chan []StatusUpdate,
	elapsed chan time.Duration) {

	statusRequests = make(chan StatusRequest)
	output = make(chan []StatusUpdate)
	elapsed = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
		for request := range statusRequests {
			start := time.Now()
			updates := []StatusUpdate{}
			for _, modelID := range request.ModelsToPoll {
				queryStart := time.Now()
				newStatus := CheckModelBongaCams(clients[clientIdx], modelID, headers, debug)
				updates = append(updates, StatusUpdate{ModelID: modelID, Status: newStatus})
				queryElapsed := time.Since(queryStart) / time.Millisecond
				if intervalMs != 0 {
					sleep := intervalMs/len(clients) - int(queryElapsed)
					if sleep > 0 {
						time.Sleep(time.Duration(sleep) * time.Millisecond)
					}
				}
				clientIdx++
				if clientIdx == clientsNum {
					clientIdx = 0
				}
			}
			elapsed <- time.Since(start)
			output <- updates
		}
	}()
	return
}
