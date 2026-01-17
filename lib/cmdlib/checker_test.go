package cmdlib

import (
	"errors"
	"os"
	"testing"
)

type TestChecker struct {
	CheckerCommon
	status StatusKind
	online map[string]bool //nolint:structcheck
	err    error           //nolint:structcheck
}

type testOnlineListChecker struct {
	TestChecker
}

var queueSize = 1000

func (c *TestChecker) CheckStatusSingle(string) StatusKind {
	return c.status
}

func (c *testOnlineListChecker) QueryOnlineChannels() (map[string]ChannelInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	channels := map[string]ChannelInfo{}
	for k := range c.online {
		channels[k] = ChannelInfo{}
	}
	return channels, nil
}

func (c *testOnlineListChecker) QueryFixedListOnlineChannels(channels []string, _ CheckMode) (map[string]ChannelInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	result := map[string]ChannelInfo{}
	for _, ch := range channels {
		if c.online[ch] {
			result[ch] = ChannelInfo{}
		}
	}
	return result, nil
}

// UsesFixedList returns false for online list checkers
func (c *testOnlineListChecker) UsesFixedList() bool { return false }

func TestOnlineListCheckerHandlesFixedList(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan CheckerResults)
	StartCheckerDaemon(checker)

	checker.online = toSet("a", "b")
	if err := checker.PushStatusRequest(&FixedListOnlineRequest{
		ResultsCh: resultsCh,
		Channels:  toSet("a", "c"),
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	result := (<-resultsCh).(*FixedListOnlineResults)
	if result.Failed() {
		t.Error("unexpected error")
	}
	if _, ok := result.Channels["a"]; !ok {
		t.Error("expected a to be in channels (online)")
	}
	// c was queried but not online, should not be in the results
	if _, ok := result.Channels["c"]; ok {
		t.Error("expected c to not be in channels (not online)")
	}
	// b was online but not queried, should not be in the results
	if _, ok := result.Channels["b"]; ok {
		t.Error("expected b to not be in channels (not queried)")
	}
}

func TestOnlineListCheckerError(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan CheckerResults)
	StartCheckerDaemon(checker)

	checker.err = errors.New("error")
	if err := checker.PushStatusRequest(&OnlineListRequest{ResultsCh: resultsCh}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	result := <-resultsCh
	if !result.Failed() {
		t.Error("expected error")
	}
}

func toSet(xs ...string) map[string]bool {
	result := map[string]bool{}
	for _, x := range xs {
		result[x] = true
	}
	return result
}

func TestMain(m *testing.M) {
	Verbosity = SilentVerbosity
	os.Exit(m.Run())
}
