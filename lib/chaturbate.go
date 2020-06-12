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

type chaturbateModel struct {
	Username string `json:"username"`
}

type chaturbateResponse struct {
	RoomStatus string `json:"room_status"`
}

// CheckModelChaturbate checks Chaturbate model status
func CheckModelChaturbate(client *Client, modelID string, headers [][2]string, dbg bool) StatusKind {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://en.chaturbate.com/api/chatvideocontext/%s/", modelID), nil)
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
	parsed := &chaturbateResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		Lerr("[%v] cannot parse response for model %s, %v", client.Addr, modelID, err)
		if dbg {
			Ldbg("response: %s", buf.String())
		}
		return StatusUnknown
	}
	return chaturbateStatus(parsed.RoomStatus)
}

func chaturbateStatus(roomStatus string) StatusKind {
	switch roomStatus {
	case "public":
		return StatusOnline
	case "private":
		return StatusOnline
	case "group":
		return StatusOnline
	case "hidden":
		return StatusOnline
	case "connecting":
		return StatusOnline
	case "password protected":
		return StatusOnline
	case "away":
		return StatusOffline
	case "offline":
		return StatusOffline
	}
	Lerr("cannot parse room status \"%s\"", roomStatus)
	return StatusUnknown
}

// StartChaturbateAPIChecker starts a checker for Chaturbate
func StartChaturbateAPIChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
) (
	statusRequests chan StatusRequest,
	output chan []StatusUpdate,
	elapsedCh chan time.Duration) {

	statusRequests = make(chan StatusRequest)
	output = make(chan []StatusUpdate)
	elapsedCh = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
		for request := range statusRequests {
			hash := map[string]bool{}
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
					Lerr("[%v] cannot send a query, %v", client.Addr, err)
					output <- nil
					continue
				}
				if resp.StatusCode != 200 {
					Lerr("[%v] query status, %d", client.Addr, resp.StatusCode)
					output <- nil
					continue
				}
				decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
				var parsed []chaturbateModel
				err = decoder.Decode(&parsed)
				if err != nil {
					Lerr("[%v] cannot parse response, %v", client.Addr, err)
					if dbg {
						Ldbg("response: %s", buf.String())
					}
					output <- nil
					continue
				}

				for _, m := range parsed {
					modelID := strings.ToLower(m.Username)
					hash[modelID] = true
				}
			}
			for modelID := range hash {
				updates = append(updates, StatusUpdate{ModelID: modelID, Status: StatusOnline})
			}
			for _, modelID := range request.KnownModels {
				if !hash[modelID] {
					updates = append(updates, StatusUpdate{ModelID: modelID, Status: StatusOffline})
				}
			}
			output <- updates
		}
	}()
	return
}
