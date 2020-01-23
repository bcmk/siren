package lib

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

var viewCamPageMainTag = cascadia.MustCompile("div.view-cam-page-wrapper")
var statusDiv = cascadia.MustCompile("div.vc-status")
var offlineDiv = cascadia.MustCompile("div.status-off")
var privateDiv = cascadia.MustCompile("div.status-private")
var groupDiv = cascadia.MustCompile("div.status-groupShow")
var p2pStatusDiv = cascadia.MustCompile("div.status-p2p")
var idleDiv = cascadia.MustCompile("div.status-idle")
var disabledDiv = cascadia.MustCompile("div.account-disabled-page")

// CheckModelStripchat checks Stripchat model status
func CheckModelStripchat(client *Client, modelID string, headers [][2]string, dbg bool) StatusKind {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://stripchat.com/%s", modelID), nil)
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
	if resp.StatusCode == 404 {
		return StatusNotFound
	}
	buf := bytes.Buffer{}
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		Lerr("[%v] cannot read body for model %s, %v", client.Addr, modelID, err)
		return StatusUnknown
	}
	copy := ioutil.NopCloser(bytes.NewReader(buf.Bytes()))
	doc, err := html.Parse(copy)
	if err != nil {
		Lerr("[%v] cannot parse body for model %s, %v", client.Addr, modelID, err)
		return StatusUnknown
	}

	if viewCamPageMainTag.MatchFirst(doc) == nil {
		Lerr("[%v] cannot parse a page for model %s", client.Addr, modelID)
		return StatusUnknown
	}

	if offlineDiv.MatchFirst(doc) != nil {
		return StatusOffline
	}

	if disabledDiv.MatchFirst(doc) != nil {
		return StatusDenied
	}

	if false ||
		statusDiv.MatchFirst(doc) == nil ||
		privateDiv.MatchFirst(doc) != nil ||
		p2pStatusDiv.MatchFirst(doc) != nil ||
		idleDiv.MatchFirst(doc) != nil ||
		groupDiv.MatchFirst(doc) != nil {

		return StatusOnline
	}

	Lerr("[%v] cannot determine status, response: %s", client.Addr, buf.String())
	return StatusUnknown
}
