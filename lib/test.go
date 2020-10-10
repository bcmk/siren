package lib

import (
	"math/rand"
	"time"
)

// CheckModelTest mimics checker
func CheckModelTest(client *Client, modelID string, headers [][2]string, dbg bool, _ map[string]string) StatusKind {
	return StatusOnline
}

// StartTestChecker mimics checker
func StartTestChecker(
	usersOnlineEndpoint []string,
	clients []*Client,
	headers [][2]string,
	intervalMs int,
	dbg bool,
	_ map[string]string,
) (
	statusRequests chan StatusRequest,
	statusUpdates chan []OnlineModel,
	errorsCh chan struct{},
	elapsedCh chan time.Duration) {

	statusRequests = make(chan StatusRequest)
	statusUpdates = make(chan []OnlineModel)
	errorsCh = make(chan struct{})
	elapsedCh = make(chan time.Duration)
	go func() {
		for range statusRequests {
			updates := []OnlineModel{}
			for i := 0; i < 300; i++ {
				updates = append(updates, OnlineModel{ModelID: RandString(4), Image: ""})
			}
			statusUpdates <- updates
		}
	}()
	return
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func RandString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
