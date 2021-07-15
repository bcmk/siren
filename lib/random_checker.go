package lib

import (
	"math/rand"
	"time"
)

// RandomChecker implements test checker
type RandomChecker struct{ CheckerCommon }

var _ Checker = &RandomChecker{}

// CheckStatusSingle mimics checker
func (c *RandomChecker) CheckStatusSingle(modelID string) StatusKind {
	return StatusOnline
}

// checkEndpoint returns random online models
func (c *RandomChecker) checkEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	now := time.Now()
	seconds := now.Sub(now.Truncate(time.Minute))
	onlineModels = map[string]StatusKind{}
	images = map[string]string{}
	if seconds < time.Second*30 {
		toggle := "toggle"
		onlineModels[toggle] = StatusOnline
		images[toggle] = ""
	}
	for i := 0; i < 300; i++ {
		modelID := randString(4)
		onlineModels[modelID] = StatusOnline
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

// CheckStatusesMany returns Random online models
func (c *RandomChecker) CheckStatusesMany([]string, CheckMode) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return checkEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *RandomChecker) Start()                 { c.startFullCheckerDaemon(c) }
func (c *RandomChecker) createUpdater() Updater { return c.createFullUpdater(c) }
