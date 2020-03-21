package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

type bongacamsModel struct {
	Username string `json:"username"`
}

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

var _ = StartBongaCamsAPIChecker
// StartBongaCamsAPIChecker starts a checker for BongaCams
func StartBongaCamsAPIChecker(
	usersOnlineEndpoint string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
) (input chan []string, output chan StatusUpdate, elapsedCh chan time.Duration) {

	input = make(chan []string)
	output = make(chan StatusUpdate)
	elapsedCh = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
		for models := range input {
			client := clients[clientIdx]
			clientIdx++
			if clientIdx == clientsNum {
				clientIdx = 0
			}

			resp, buf, elapsed, err := onlineQuery(usersOnlineEndpoint, client, headers)
			elapsedCh <- elapsed
			if err != nil {
				sendUnknowns(output, models)
				Lerr("[%v] cannot send a query, %v", client.Addr, err)
				continue
			}
			if resp.StatusCode != 200 {
				Lerr("[%v] query status, %d", client.Addr, resp.StatusCode)
				sendUnknowns(output, models)
				continue
			}
			decoder := json.NewDecoder(ioutil.NopCloser(bytes.NewReader(buf.Bytes())))
			var parsed []bongacamsModel
			err = decoder.Decode(&parsed)
			if err != nil {
				Lerr("[%v] cannot parse response, %v", client.Addr, err)
				if dbg {
					Ldbg("response: %s", buf.String())
				}
				sendUnknowns(output, models)
				continue
			}

			hash := map[string]bool{}
			for _, m := range parsed {
				hash[strings.ToLower(m.Username)] = true
			}

			for _, modelID := range models {
				newStatus := StatusOffline
				if hash[modelID] {
					newStatus = StatusOnline
				}
				output <- StatusUpdate{ModelID: modelID, Status: newStatus}
			}
		}
	}()
	return
}

// StartBongaCamsRedirChecker starts a checker for BongaCams using redirection heuristic
func StartBongaCamsRedirChecker(usersOnlineEndpoint string, clients []*Client, headers [][2]string, intervalMs int, debug bool) (input chan []string, output chan StatusUpdate, elapsed chan time.Duration) {
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
