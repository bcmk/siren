package lib

import (
	"math/rand"
	"time"
)

// RandomChecker implements test checker
type RandomChecker struct{ CheckerCommon }

var _ Checker = &RandomChecker{}

// CheckSingle mimics checker
func (c *RandomChecker) CheckSingle(modelID string) StatusKind {
	return StatusOnline
}

// checkEndpoint returns random online models
func (c *RandomChecker) checkEndpoint(endpoint string) (onlineModels map[string]bool, images map[string]string, err error) {
	now := time.Now()
	seconds := now.Sub(now.Truncate(time.Minute))
	onlineModels = map[string]bool{}
	images = map[string]string{}
	if seconds < time.Second*30 {
		toggle := "toggle"
		onlineModels[toggle] = true
		images[toggle] = ""
	}
	for i := 0; i < 300; i++ {
		modelID := randString(4)
		onlineModels[modelID] = true
		images[modelID] = ""
	}
	return
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

// CheckFull returns Test online models
func (c *RandomChecker) CheckFull() (onlineModels map[string]bool, images map[string]string, err error) {
	return checkEndpoints(c, c.usersOnlineEndpoint, c.dbg)
}

// Start starts a daemon
func (c *RandomChecker) Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return fullDaemonStart(c, siteOnlineModels, intervalMs, dbg)
}
