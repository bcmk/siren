package lib

import (
	"strings"
	"time"
)

type clientsLoop struct {
	clients   []*Client
	clientIdx int
}

func (c *clientsLoop) nextClient() *Client {
	client := c.clients[c.clientIdx]
	c.clientIdx++
	if c.clientIdx == len(c.clients) {
		c.clientIdx = 0
	}
	return client
}

// StartChecker starts a checker for Chaturbate
func StartChecker(
	singleChecker func(
		client *Client,
		modelID string,
		headers [][2]string,
		dbg bool,
		specificConfig map[string]string) StatusKind,
	apiChecker func(
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
	clientsLoop := clientsLoop{clients: clients}
	go func() {
	requests:
		for request := range statusRequests {
			hash := map[string]OnlineModel{}
			updates := []OnlineModel{}
			start := time.Now()
			for _, endpoint := range usersOnlineEndpoint {
				client := clientsLoop.nextClient()
				onlineModels, err := apiChecker(endpoint, client, headers, dbg, specificConfig)
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
			for modelID := range request.SpecialModels {
				time.Sleep(time.Duration(intervalMs) * time.Millisecond)
				client := clientsLoop.nextClient()
				status := singleChecker(client, modelID, headers, dbg, specificConfig)
				if status == StatusOnline {
					hash[modelID] = OnlineModel{ModelID: modelID}
				} else if status != StatusOffline {
					Lerr("status for model %s reported: %v", modelID, status)
					errorsCh <- struct{}{}
				}
			}
			for _, statusUpdate := range hash {
				updates = append(updates, statusUpdate)
			}
			elapsedCh <- time.Since(start)
			if dbg {
				Ldbg("online models: %d", len(updates))
			}
			output <- updates
		}
	}()
	return
}
