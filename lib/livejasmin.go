package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
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

// LiveJasminOnlineAPI returns LiveJasmin online models
func LiveJasminOnlineAPI(
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
	var parsed liveJasminResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if parsed.Status != "OK" {
		if dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, fmt.Errorf("cannot query a list of models, %d", parsed.ErrorCode)
	}
	for _, m := range parsed.Data.Models {
		modelID := strings.ToLower(m.PerformerID)
		onlineModels[modelID] = OnlineModel{ModelID: modelID, Image: "https:" + m.ProfilePictureURL.Size896x503}
	}
	return
}
