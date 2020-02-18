package lib

import (
	"fmt"
	"net/http"
	"time"
)

// CheckModelBongaCams checks BongaCams model status
func CheckModelBongaCams(client *Client, modelID string, headers [][2]string, dbg bool) StatusKind {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://en.bongacams.com/%s", modelID), nil)
	CheckErr(err)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := client.Client.Do(req)
	if err != nil {
		Lerr("[%v] cannot send a query, %v", client.Addr, err)
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

// StartBongaCamsChecker starts a checker for BongaCams
func StartBongaCamsChecker(usersOnlineEndpoint string, clients []*Client, headers [][2]string, intervalMs int, debug bool) (input chan []string, output chan StatusUpdate, elapsed chan time.Duration) {
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
				newStatus := CheckModelBongaCams(clients[clientIdx], modelID, headers, debug)
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
