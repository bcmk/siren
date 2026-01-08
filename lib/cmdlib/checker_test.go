package cmdlib

import (
	"errors"
	"os"
	"testing"
)

type TestChecker struct {
	CheckerCommon
	status StatusKind
	online map[string]bool   //nolint:structcheck
	images map[string]string //nolint:structcheck
	err    error             //nolint:structcheck
}

type testOnlineListChecker struct {
	TestChecker
}

var queueSize = 1000

func (c *TestChecker) CheckStatusSingle(string) StatusKind {
	return c.status
}

func (c *testOnlineListChecker) CheckEndpoint(
	_ string,
) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	return onlineStatuses(c.online), c.images, nil
}

func (c *testOnlineListChecker) CheckStatusesMany(
	QueryChannelList,
	CheckMode,
) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return CheckEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *testOnlineListChecker) Start() { c.StartOnlineListCheckerDaemon(c) }

// UsesFixedList returns false for online list checkers
func (c *testOnlineListChecker) UsesFixedList() bool { return false }

func TestOnlineListCheckerHandlesFixedList(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan StatusResults)
	callback := func(res StatusResults) { resultsCh <- res }
	checker.Start()

	checker.online = toSet("a", "b")
	if err := checker.PushStatusRequest(StatusRequest{
		Callback: callback,
		Channels: toSet("a", "c"),
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	result := <-resultsCh
	if result.Error {
		t.Error("unexpected error")
	}
	if result.Statuses["a"] != StatusOnline {
		t.Errorf("expected a to be online, got %v", result.Statuses["a"])
	}
	// c was queried but not returned by checker, should be reported as unknown
	if result.Statuses["c"] != StatusUnknown {
		t.Errorf("expected c to be unknown, got %v", result.Statuses["c"])
	}
	// b was online but not queried, should not be in the results
	if _, ok := result.Statuses["b"]; ok {
		t.Errorf("expected b to not be in statuses, got %v", result.Statuses["b"])
	}
}

func TestOnlineListCheckerError(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan StatusResults)
	callback := func(res StatusResults) { resultsCh <- res }
	checker.Start()

	checker.err = errors.New("error")
	if err := checker.PushStatusRequest(StatusRequest{Callback: callback}); err != nil {
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
