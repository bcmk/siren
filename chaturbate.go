package main

import (
	"encoding/json"
	"fmt"
)

type chaturbateResponse struct {
	RoomStatus string `json:"room_status"`
}

func (w *worker) checkModelChaturbate(modelID string) statusKind {
	resp, err := w.client.Get(fmt.Sprintf("https://en.chaturbate.com/api/chatvideocontext/%s/", modelID))
	if err != nil {
		lerr("cannot send a query, %v", err)
		return statusUnknown
	}
	defer func() {
		checkErr(resp.Body.Close())
	}()
	if w.cfg.Debug {
		ldbg("query status for %s: %d", modelID, resp.StatusCode)
	}
	if resp.StatusCode == 401 {
		return statusNotFound
	}
	decoder := json.NewDecoder(resp.Body)
	parsed := &chaturbateResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		linf("cannot parse response for model %s, %v", modelID, err)
		return statusUnknown
	}
	switch parsed.RoomStatus {
	case "public":
		return statusOnline
	case "private":
		return statusOnline
	case "group":
		return statusOnline
	case "hidden":
		return statusOnline
	case "connecting":
		return statusOnline
	case "password protected":
		return statusOnline
	case "away":
		return statusOffline
	case "offline":
		return statusOffline
	}
	linf("cannot parse room status %s for model %s", parsed.RoomStatus, modelID)
	return statusUnknown
}
