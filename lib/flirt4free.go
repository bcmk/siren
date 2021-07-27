package lib

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

// Flirt4FreeChecker implements a checker for Flirt4Free
type Flirt4FreeChecker struct{ CheckerCommon }

var _ Checker = &Flirt4FreeChecker{}

type flirt4FreeCheckResponse struct {
	Status string `json:"status"`
}

type flirt4FreeOnlineModel struct {
	Name           string `json:"name"`
	ScreencapImage string `json:"screencap_image"`
}

type flirt4FreeOnlineResponse struct {
	Error *struct {
		Method      string
		Code        string
		Description string
	}
	Girls map[int]flirt4FreeOnlineModel
	Guys  map[int]flirt4FreeOnlineModel
	Trans map[int]flirt4FreeOnlineModel
}

// CheckStatusSingle checks Flirt4Free model status
func (c *Flirt4FreeChecker) CheckStatusSingle(modelID string) StatusKind {
	client := c.clientsLoop.nextClient()
	req, err := http.NewRequest("GET", fmt.Sprintf("https://ws.vs3.com/rooms/check-model-status.php?model_name=%s", modelID), nil)
	CheckErr(err)
	for _, h := range c.Headers {
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
	if c.Dbg {
		Ldbg("[%v] query status for %s: %d", client.Addr, modelID, resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return StatusUnknown
	}
	buf := bytes.Buffer{}
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		Lerr("[%v] cannot read response for model %s, %v", client.Addr, modelID, err)
		return StatusUnknown
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	parsed := &flirt4FreeCheckResponse{}
	err = decoder.Decode(parsed)
	if err != nil {
		Lerr("[%v] cannot parse response for model %s, %v", client.Addr, modelID, err)
		if c.Dbg {
			Ldbg("response: %s", buf.String())
		}
		return StatusUnknown
	}
	return flirt4FreeStatus(parsed.Status)
}

func flirt4FreeStatus(roomStatus string) StatusKind {
	switch roomStatus {
	case "failed":
		return StatusNotFound
	case "online":
		return StatusOnline
	}
	Lerr("cannot parse room status \"%s\"", roomStatus)
	return StatusUnknown
}

func flirt4FreeCanonicalAPIModelID(name string) string {
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, "&amp;", "and")
	name = strings.ReplaceAll(name, "&", "")
	name = strings.ReplaceAll(name, ";", "")
	return strings.ToLower(name)
}

// Flirt4FreeCanonicalModelID preprocesses model ID string to canonical for Flirt4Free form
func Flirt4FreeCanonicalModelID(name string) string {
	name = strings.ReplaceAll(name, "-", "_")
	return strings.ToLower(name)
}

// checkEndpoint returns Flirt4Free online models
func (c *Flirt4FreeChecker) checkEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	client := c.clientsLoop.nextClient()
	onlineModels = map[string]StatusKind{}
	images = map[string]string{}
	resp, buf, err := onlineQuery(endpoint, client, c.Headers)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot send a query, %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("query status, %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
	var parsed flirt4FreeOnlineResponse
	err = decoder.Decode(&parsed)
	if err != nil {
		if c.Dbg {
			Ldbg("response: %s", buf.String())
		}
		return nil, nil, fmt.Errorf("cannot parse response, %v", err)
	}
	if parsed.Error != nil {
		return nil, nil, fmt.Errorf("API error, code: %s, description: %s", parsed.Error.Code, parsed.Error.Description)
	}
	if len(parsed.Girls) == 0 || len(parsed.Guys) == 0 || len(parsed.Trans) == 0 {
		return nil, nil, errors.New("zero online models reported")
	}
	for _, m := range parsed.Girls {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		onlineModels[modelID] = StatusOnline
		images[modelID] = m.ScreencapImage
	}
	for _, m := range parsed.Guys {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		onlineModels[modelID] = StatusOnline
		images[modelID] = m.ScreencapImage
	}
	for _, m := range parsed.Trans {
		modelID := flirt4FreeCanonicalAPIModelID(m.Name)
		onlineModels[modelID] = StatusOnline
		images[modelID] = m.ScreencapImage
	}
	return
}

// CheckStatusesMany returns Flirt4Free online models
func (c *Flirt4FreeChecker) CheckStatusesMany(QueryModelList, CheckMode) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return checkEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *Flirt4FreeChecker) Start()                 { c.startFullCheckerDaemon(c) }
func (c *Flirt4FreeChecker) createUpdater() Updater { return c.createFullUpdater(c) }
