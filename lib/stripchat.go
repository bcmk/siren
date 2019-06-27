package lib

import (
	"fmt"
	"net/http"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

var offlineDiv = cascadia.MustCompile("div.status-off")
var bannedDiv = cascadia.MustCompile("div.banned")

// CheckModelStripchat checks Stripchat model status
func CheckModelStripchat(client *http.Client, modelID string, dbg bool) StatusKind {
	resp, err := client.Get(fmt.Sprintf("https://stripchat.com/%s", modelID))
	if err != nil {
		Lerr("cannot send a query, %v", err)
		return StatusUnknown
	}
	defer func() {
		CheckErr(resp.Body.Close())
	}()
	if resp.StatusCode == 404 {
		return StatusNotFound
	}
	doc, err := html.Parse(resp.Body)
	if err != nil {
		Lerr("cannot parse body for model %s, %v", modelID, err)
		return StatusUnknown
	}

	if offlineDiv.MatchFirst(doc) != nil {
		return StatusOffline
	}

	if bannedDiv.MatchFirst(doc) != nil {
		return StatusDenied
	}

	return StatusOnline
}
