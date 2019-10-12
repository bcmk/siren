package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
)

type chaturbateResponse struct {
	RoomStatus string `json:"room_status"`
}

// CheckModelChaturbate checks Chaturbate model status
func CheckModelChaturbate(client *http.Client, modelID string, dbg bool) StatusKind {
	resp, err := client.Get(fmt.Sprintf("https://en.chaturbate.com/api/chatvideocontext/%s/", modelID))
	if err != nil {
		Lerr("cannot send a query, %v", err)
		return StatusUnknown
	}
	defer func() {
		CheckErr(resp.Body.Close())
	}()
	if dbg {
		Ldbg("query status for %s: %d", modelID, resp.StatusCode)
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
		Lerr("cannot read response for model %s, %v", modelID, err)
		return StatusUnknown
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &chaturbateResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		Lerr("cannot parse response for model %s, %v", modelID, err)
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
