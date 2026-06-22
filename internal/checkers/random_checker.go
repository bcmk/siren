package checkers

import (
	"math/rand"
	"time"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// TestCheckerConfig is the config for RandomChecker. Skips
// BaseCheckerConfig.validateBase (no HTTP requests).
type TestCheckerConfig struct {
	BaseCheckerConfig `mapstructure:",squash"`
}

func (c *TestCheckerConfig) validate() error { return nil }

// RandomChecker implements test checker
type RandomChecker struct {
	BaseChecker[*TestCheckerConfig]
}

// Site returns the site name.
func (*RandomChecker) Site() string { return "test" }

// Init loads test-checker.json.
func (c *RandomChecker) Init(checkerCfgPath string) error {
	if err := c.ensureUninitialised(); err != nil {
		return err
	}
	cfg := &TestCheckerConfig{}
	if err := readCheckerConfig(cfg, c.Site(), checkerCfgPath); err != nil {
		return err
	}
	c.BaseChecker = NewBaseChecker(cfg)
	return nil
}

var _ Checker = &RandomChecker{}

// QueryStatus mimics checker
func (c *RandomChecker) QueryStatus(_ string) (cmdlib.StreamerInfoWithStatus, error) {
	return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOnline}, nil
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
	return nil, ErrNotImplemented
}

// Capabilities lists the surfaces RandomChecker exposes for dispatch.
func (*RandomChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: false,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
	}
}
