package main

import "fmt"

func (w *worker) checkModelBongacams(modelID string) statusKind {
	resp, err := w.client.Get(fmt.Sprintf("https://bongacams.com/%s", modelID))
	if err != nil {
		lerr("cannot send a query, %v", err)
		return statusUnknown
	}
	checkErr(resp.Body.Close())
	if w.cfg.Debug {
		ldbg("query status for %s: %d", modelID, resp.StatusCode)
	}
	switch resp.StatusCode {
	case 200:
		return statusOnline
	case 302:
		return statusOffline
	case 404:
		return statusNotFound
	}
	return statusUnknown
}
