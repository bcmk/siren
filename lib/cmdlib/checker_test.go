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

func (c *testOnlineListChecker) QueryOnlineChannels(CheckMode) (map[string]ChannelInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	channels := map[string]ChannelInfo{}
	for k := range c.online {
		channels[k] = ChannelInfo{Status: StatusOnline}
	}
	return channels, nil
}

func (c *testOnlineListChecker) QueryChannelListStatuses([]string, CheckMode) (map[string]ChannelInfo, error) {
	return nil, ErrNotImplemented
}

// UsesFixedList returns false for online list checkers
func (c *testOnlineListChecker) UsesFixedList() bool { return false }

func TestOnlineListCheckerHandlesFixedList(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan StatusResults)
	StartCheckerDaemon(checker)

	checker.online = toSet("a", "b")
	if err := checker.PushStatusRequest(StatusRequest{
		ResultsCh: resultsCh,
		Channels:  toSet("a", "c"),
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	result := <-resultsCh
	if result.Error {
		t.Error("unexpected error")
	}
	if result.Channels["a"].Status != StatusOnline {
		t.Errorf("expected a to be online, got %v", result.Channels["a"].Status)
	}
	// c was queried but not returned by checker, should be reported as unknown
	if result.Channels["c"].Status != StatusUnknown {
		t.Errorf("expected c to be unknown, got %v", result.Channels["c"].Status)
	}
	// b was online but not queried, should not be in the results
	if _, ok := result.Channels["b"]; ok {
		t.Errorf("expected b to not be in channels, got %v", result.Channels["b"])
	}
}

func TestOnlineListCheckerError(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan StatusResults)
	StartCheckerDaemon(checker)

	checker.err = errors.New("error")
	if err := checker.PushStatusRequest(StatusRequest{ResultsCh: resultsCh}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	result := <-resultsCh
	if !result.Error {
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
