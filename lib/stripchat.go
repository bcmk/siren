package lib

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

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
		if dbg {
			Ldbg("[%v] response:\n%s", client.Addr, buf.String())
		}
		return StatusUnknown
	}

	if viewCamPageMainTag.MatchFirst(doc) == nil {
		Lerr("[%v] cannot parse a page for model %s", client.Addr, modelID)
		if dbg {
			Ldbg("[%v] response:\n%s", client.Addr, buf.String())
		}
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

// StartStripchatChecker starts a checker for Stripchat
func StartStripchatChecker(clients []*Client, headers [][2]string, intervalMs int, debug bool) (input chan []string, output chan StatusUpdate, elapsed chan time.Duration) {
	input = make(chan []string)
	output = make(chan StatusUpdate)
	elapsed = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
		for models := range input {
			start := time.Now()
			for _, modelID := range models {
				queryStart := time.Now()
				newStatus := CheckModelStripchat(clients[clientIdx], modelID, headers, debug)
				output <- StatusUpdate{ModelID: modelID, Status: newStatus}
				queryElapsed := time.Since(queryStart) / time.Millisecond
				if intervalMs != 0 {
					sleep := intervalMs/len(clients) - int(queryElapsed)
					if sleep > 0 {
						time.Sleep(time.Duration(sleep) * time.Millisecond)
					}
				}
				clientIdx++
				if clientIdx == clientsNum {
					clientIdx = 0
				}
			}
			elapsed <- time.Since(start)
		}
	}()
	return
}
