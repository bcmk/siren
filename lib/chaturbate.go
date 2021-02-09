package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

type chaturbateModel struct {
	Username string `json:"username"`
	ImageURL string `json:"image_url"`
}

type chaturbateResponse struct {
	RoomStatus string `json:"room_status"`
	Status     *int   `json:"status"`
	Code       string `json:"code"`
}

// CheckModelChaturbate checks Chaturbate model status
func CheckModelChaturbate(client *Client, modelID string, headers [][2]string, dbg bool, _ map[string]string) StatusKind {
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
	if parsed.Status != nil {
		return chaturbateStatus(parsed.Code)
	}
	return chaturbateRoomStatus(parsed.RoomStatus)
}

func chaturbateStatus(status string) StatusKind {
	switch status {
	case "access-denied":
		return StatusDenied
	case "unauthorized":
		return StatusDenied
	}
	return StatusUnknown
}

func chaturbateRoomStatus(roomStatus string) StatusKind {
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

// ChaturbateOnlineAPI returns Chaturbate online models
func ChaturbateOnlineAPI(
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
	var parsed []chaturbateModel
	err = decoder.Decode(&parsed)
	if err != nil {
		if dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	for _, m := range parsed {
		modelID := strings.ToLower(m.Username)
		onlineModels[modelID] = OnlineModel{ModelID: modelID, Image: m.ImageURL}
	}
	return
}
