package lib

import (
	"strings"
	"time"
)

// StartAPIChecker starts a checker for Chaturbate
func StartAPIChecker(
	checker func(
		usersOnlineEndpoint string,
		client *Client,
		headers [][2]string,
		dbg bool,
		specificConfig map[string]string,
	) (
		map[string]OnlineModel,
		error,
	),
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
	specificConfig map[string]string,
) (
	statusRequests chan StatusRequest,
	output chan []OnlineModel,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	statusRequests = make(chan StatusRequest)
	output = make(chan []OnlineModel)
	errorsCh = make(chan struct{})
	elapsedCh = make(chan time.Duration)
	clientIdx := 0
	clientsNum := len(clients)
	go func() {
	requests:
		for range statusRequests {
			hash := map[string]OnlineModel{}
			updates := []OnlineModel{}
			for _, endpoint := range usersOnlineEndpoint {
				client := clients[clientIdx]
				clientIdx++
				if clientIdx == clientsNum {
					clientIdx = 0
				}

				start := time.Now()
				onlineModels, err := checker(endpoint, client, headers, dbg, specificConfig)
				elapsedCh <- time.Since(start)
				if err != nil {
					Lerr("[%v] %v", client.Addr, err)
					errorsCh <- struct{}{}
					continue requests
				}
				if dbg {
					Ldbg("online models for endpoint: %d", len(onlineModels))
				}
				for _, m := range onlineModels {
					m.ModelID = strings.ToLower(m.ModelID)
					hash[m.ModelID] = m
				}
			}
			for _, statusUpdate := range hash {
				updates = append(updates, statusUpdate)
			}
			if dbg {
				Ldbg("online models: %d", len(updates))
			}
			output <- updates
		}
	}()
	return
}
