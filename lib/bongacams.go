package lib

import (
	"fmt"
	"net/http"
)

// CheckModelBongaCams checks BongaCams model status
func CheckModelBongaCams(client *http.Client, modelID string, userAgent string, dbg bool) StatusKind {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://bongacams.com/%s", modelID), nil)
	CheckErr(err)
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		Lerr("cannot send a query, %v", err)
		return StatusUnknown
	}
	CheckErr(resp.Body.Close())
	if dbg {
		Ldbg("query status for %s: %d", modelID, resp.StatusCode)
	}
	switch resp.StatusCode {
	case 200:
		return StatusOnline
	case 302:
		return StatusOffline
	case 404:
		return StatusNotFound
	}
	return StatusUnknown
}
