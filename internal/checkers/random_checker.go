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

//goland:noinspection SpellCheckingInspection
var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

// QueryOnlineChannels returns Random online channels
func (c *RandomChecker) QueryOnlineChannels(cmdlib.CheckMode) (map[string]cmdlib.ChannelInfo, error) {
	now := time.Now()
	seconds := now.Sub(now.Truncate(time.Minute))
	channels := map[string]cmdlib.ChannelInfo{}
	if seconds < time.Second*30 {
		channels["toggle"] = cmdlib.ChannelInfo{Status: cmdlib.StatusOnline}
	}
	for i := 0; i < 300; i++ {
		channelID := randString(4)
		channels[channelID] = cmdlib.ChannelInfo{Status: cmdlib.StatusOnline}
	}
	return channels, nil
}

// QueryChannelListStatuses is not implemented for online list checkers
func (c *RandomChecker) QueryChannelListStatuses([]string, cmdlib.CheckMode) (map[string]cmdlib.ChannelInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// UsesFixedList returns false for online list checkers
func (c *RandomChecker) UsesFixedList() bool { return false }
