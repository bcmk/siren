package checkers

import (
	"math/rand"
	"time"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// RandomChecker implements test checker
type RandomChecker struct{ cmdlib.CheckerCommon }

var _ cmdlib.Checker = &RandomChecker{}

// QueryStatus mimics checker
func (c *RandomChecker) QueryStatus(_ string) (cmdlib.StatusKind, error) {
	return cmdlib.StatusOnline, nil
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

// QueryOnlineStreamers returns Random online streamers
func (c *RandomChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	now := time.Now()
	seconds := now.Sub(now.Truncate(time.Minute))
	streamers := map[string]cmdlib.StreamerInfo{}
	if seconds < time.Second*30 {
		streamers["toggle"] = cmdlib.StreamerInfo{}
	}
	for i := 0; i < 300; i++ {
		nickname := randString(4)
		streamers[nickname] = cmdlib.StreamerInfo{}
	}
	return streamers, nil
}

// QueryFixedListOnlineStreamers is not implemented for online list checkers
func (c *RandomChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, cmdlib.ErrNotImplemented
}

// Capabilities reports the status surfaces RandomChecker implements.
func (*RandomChecker) Capabilities() cmdlib.Capabilities {
	return cmdlib.Capabilities{
		QueryOnlineStreamers:          true,
		QueryFixedListOnlineStreamers: false,
		QueryFixedListStatuses:        false,
		QueryStatus:                   true,
	}
}
