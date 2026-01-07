package checkers

import (
	"math/rand"
	"time"

	"github.com/bcmk/siren/lib/cmdlib"
)

// RandomChecker implements test checker
type RandomChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &RandomChecker{}

// CheckStatusSingle mimics checker
func (c *RandomChecker) CheckStatusSingle(_ string) cmdlib.StatusKind {
	return cmdlib.StatusOnline
}

// CheckEndpoint returns random online models
func (c *RandomChecker) CheckEndpoint(_ string) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
	now := time.Now()
	seconds := now.Sub(now.Truncate(time.Minute))
	onlineModels = map[string]cmdlib.StatusKind{}
	images = map[string]string{}
	if seconds < time.Second*30 {
		toggle := "toggle"
		onlineModels[toggle] = cmdlib.StatusOnline
		images[toggle] = ""
	}
	for i := 0; i < 300; i++ {
		modelID := randString(4)
		onlineModels[modelID] = cmdlib.StatusOnline
		images[modelID] = ""
	}
	return
}

//goland:noinspection SpellCheckingInspection
var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

// CheckStatusesMany returns Random online models
func (c *RandomChecker) CheckStatusesMany(cmdlib.QueryModelList, cmdlib.CheckMode) (onlineModels map[string]cmdlib.StatusKind, images map[string]string, err error) {
	return cmdlib.CheckEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *RandomChecker) Start() { c.StartOnlineListCheckerDaemon(c) }

// UsesFixedList returns false for online list checkers
func (c *RandomChecker) UsesFixedList() bool { return false }
