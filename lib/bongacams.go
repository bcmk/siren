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

var _ = StartBongaCamsAPIChecker

// StartBongaCamsAPIChecker starts a checker for BongaCams
func StartBongaCamsAPIChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
	_ map[string]string,
) (
	statusRequests chan StatusRequest,
	statusUpdates chan []OnlineModel,
	elapsedCh chan time.Duration) {

	statusRequests = make(chan StatusRequest)
	statusUpdates = make(chan []OnlineModel)
	elapsedCh = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
		for _ = range statusRequests {
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
					hash[modelID] = OnlineModel{ModelID: modelID, Image: "https:" + m.ProfileImages.ThumbnailImageMediumLive}
				}
			}
			for _, statusUpdate := range hash {
				updates = append(updates, statusUpdate)
			}
			statusUpdates <- updates
		}
	}()
	return
}
