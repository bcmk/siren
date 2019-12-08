package lib

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

var bodyTag = cascadia.MustCompile("#body")
var offlineDiv = cascadia.MustCompile("div.status-off")
var bannedDiv = cascadia.MustCompile("div.banned")
var statusDiv = cascadia.MustCompile("div.vc-status")
var privateDiv = cascadia.MustCompile("div.status-private")
var p2pStatusDiv = cascadia.MustCompile("div.status-private")
var idleDiv = cascadia.MustCompile("div.status-idle")

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
	copy := ioutil.NopCloser(bytes.NewReader(buf.Bytes()))
	doc, err := html.Parse(copy)
	if err != nil {
		Lerr("[%v] cannot parse body for model %s, %v", client.Addr, modelID, err)
		return StatusUnknown
	}

	if bodyTag.MatchFirst(doc) == nil {
		Lerr("[%v] cannot parse body for model %s", client.Addr, modelID)
		return StatusUnknown
	}

	if offlineDiv.MatchFirst(doc) != nil {
		return StatusOffline
	}

	if bannedDiv.MatchFirst(doc) != nil {
		return StatusDenied
	}

	if statusDiv.MatchFirst(doc) == nil || privateDiv.MatchFirst(doc) != nil || p2pStatusDiv.MatchFirst(doc) != nil || idleDiv.MatchFirst(doc) != nil {
		return StatusOnline
	}

	Lerr("[%v] cannot determine status", client.Addr)
	if dbg {
		Ldbg("response: %s", buf.String())
	}
	return StatusUnknown
}
