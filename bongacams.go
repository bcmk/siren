package siren

import (
	"fmt"
	"net/http"
)

func CheckModelBongacams(client *http.Client, modelID string, dbg bool) StatusKind {
	resp, err := client.Get(fmt.Sprintf("https://bongacams.com/%s", modelID))
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
