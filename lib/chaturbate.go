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

// StartChaturbateChecker starts a checker for Chaturbate
func StartChaturbateAPIChecker(
	usersOnlineEndpoint string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
) (input chan []string, output chan StatusUpdate, elapsedCh chan time.Duration) {

	input = make(chan []string)
	output = make(chan StatusUpdate)
	elapsedCh = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
		for models := range input {
			client := clients[clientIdx]
			clientIdx++
			if clientIdx == clientsNum {
				clientIdx = 0
			}

			resp, buf, elapsed, err := onlineQuery(usersOnlineEndpoint, client, headers)
			elapsedCh <- elapsed
			if err != nil {
				sendUnknowns(output, models)
				Lerr("[%v] cannot send a query, %v", client.Addr, err)
				continue
			}
			if resp.StatusCode != 200 {
				Lerr("[%v] query status, %d", client.Addr, resp.StatusCode)
				sendUnknowns(output, models)
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
				sendUnknowns(output, models)
				continue
			}

			hash := map[string]bool{}
			for _, m := range parsed {
				hash[strings.ToLower(m.Username)] = true
			}

			for _, modelID := range models {
				newStatus := StatusOffline
				if hash[modelID] {
					newStatus = StatusOnline
				}
				output <- StatusUpdate{ModelID: modelID, Status: newStatus}
			}
		}
	}()
	return
}
