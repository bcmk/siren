package cmdlib

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

type TestChecker struct {
	CheckerCommon
	status StatusKind
	online map[string]bool   //nolint:structcheck
	images map[string]string //nolint:structcheck
	err    error             //nolint:structcheck
}

type testFullChecker struct {
	TestChecker
}

type testSelectiveChecker struct {
	TestChecker
}

var queueSize = 1000

func (c *TestChecker) CheckStatusSingle(string) StatusKind {
	return c.status
}

func (c *testSelectiveChecker) CheckStatusesMany(
	QueryModelList,
	CheckMode,
) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	return onlineStatuses(c.online), c.images, nil
}

func (c *testFullChecker) CheckEndpoint(
	_ string,
) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	return onlineStatuses(c.online), c.images, nil
}

func (c *testFullChecker) CheckStatusesMany(
	QueryModelList,
	CheckMode,
) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return CheckEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *testFullChecker) Start()                 { c.StartFullCheckerDaemon(c) }
func (c *testFullChecker) CreateUpdater() Updater { return FullUpdater() }

// Start starts a daemon
func (c *testSelectiveChecker) Start()                 { c.StartSelectiveCheckerDaemon(c) }
func (c *testSelectiveChecker) CreateUpdater() Updater { return SelectiveUpdater() }

func TestFullUpdater(t *testing.T) {
	checker := &testFullChecker{}
	up := checker.CreateUpdater()
	up.Init(UpdaterConfig{SiteOnlineModels: toSet("a", "b")})
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan StatusResults)
	callback := func(res StatusResults) { resultsCh <- res }
	checker.Start()

	// Test: b stays online, a goes offline, c goes online
	checker.online = toSet("b", "c")
	if err := checker.PushStatusRequest(StatusRequest{Callback: callback}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	rawResult := <-resultsCh
	updateResult := up.ProcessStreams(rawResult)
	uSet := updatesSet(updateResult.Updates)
	expected := map[string]StatusKind{"a": StatusOffline, "c": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}

	// Test: error case
	checker.err = errors.New("error")
	if err := checker.PushStatusRequest(StatusRequest{Callback: callback}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	upd := up.ProcessStreams(<-resultsCh)
	if !upd.Error {
		t.Error("expected error")
	}
}

func TestFullCheckerHandlesFixedStreams(t *testing.T) {
	checker := &testFullChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan StatusResults)
	callback := func(res StatusResults) { resultsCh <- res }
	checker.Start()

	checker.online = toSet("a", "b")
	if err := checker.PushStatusRequest(StatusRequest{
		Callback: callback,
		Models:   toSet("a", "c"),
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	rawResult := <-resultsCh
	if rawResult.Error {
		t.Error("unexpected error")
	}
	if rawResult.Statuses["a"] != StatusOnline {
		t.Errorf("expected a to be online, got %v", rawResult.Statuses["a"])
	}
	// c was queried but not returned by checker, should be reported as unknown
	if rawResult.Statuses["c"] != StatusUnknown {
		t.Errorf("expected c to be unknown, got %v", rawResult.Statuses["c"])
	}
	// b was online but not queried, should not be in the results
	if _, ok := rawResult.Statuses["b"]; ok {
		t.Errorf("expected b to not be in statuses, got %v", rawResult.Statuses["b"])
	}
}

func TestSelectiveUpdater(t *testing.T) {
	checker := &testSelectiveChecker{}
	up := checker.CreateUpdater()
	up.Init(UpdaterConfig{
		SiteOnlineModels:     toSet("a", "b"),
		SubscriptionStatuses: map[string]StatusKind{"a": StatusOnline, "b": StatusOnline},
	})
	checker.Init(CheckerConfig{QueueSize: queueSize})
	resultsCh := make(chan StatusResults)
	callback := func(res StatusResults) { resultsCh <- res }
	checker.Start()

	// Test: a goes offline, b becomes unknown, c goes online
	checker.online = toSet("c")
	queriedModels := toSet("a", "c")
	if err := checker.PushStatusRequest(StatusRequest{
		Callback: callback,
		Models:   queriedModels,
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	rawResult := <-resultsCh
	updateResult := up.ProcessStreams(rawResult)
	updates := updateResult.Updates
	uSet := updatesSet(updates)
	if len(updates) != len(uSet) {
		t.Errorf("duplicates found: %v", updates)
	}
	expected := map[string]StatusKind{"a": StatusOffline, "b": StatusUnknown, "c": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}

	// Test: b goes online, a becomes unknown
	checker.online = toSet("b", "c")
	queriedModels = toSet("b", "c")
	if err := checker.PushStatusRequest(StatusRequest{
		Callback: callback,
		Models:   queriedModels,
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	rawResult = <-resultsCh
	updateResult = up.ProcessStreams(rawResult)
	uSet = updatesSet(updateResult.Updates)
	expected = map[string]StatusKind{"a": StatusUnknown, "b": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}

	// Test: error case
	checker.err = errors.New("error")
	queriedModels = toSet()
	if err := checker.PushStatusRequest(StatusRequest{
		Callback: callback,
		Models:   queriedModels,
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	rawResult = <-resultsCh
	updateResult = up.ProcessStreams(rawResult)
	if !updateResult.Error {
		t.Error("expected error")
	}

	// Test: c goes offline
	checker.err = nil
	checker.online = toSet("b")
	queriedModels = toSet("b", "c")
	if err := checker.PushStatusRequest(StatusRequest{
		Callback: callback,
		Models:   queriedModels,
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	rawResult = <-resultsCh
	updateResult = up.ProcessStreams(rawResult)
	uSet = updatesSet(updateResult.Updates)
	expected = map[string]StatusKind{"c": StatusOffline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}
}

func toSet(xs ...string) map[string]bool {
	result := map[string]bool{}
	for _, x := range xs {
		result[x] = true
	}
	return result
}

func updatesSet(updates []StatusUpdate) map[string]StatusKind {
	result := map[string]StatusKind{}
	for _, x := range updates {
		result[x.ModelID] = x.Status
	}
	return result
}

// TestSelectiveUpdaterQueriedModels tests that we correctly report updates
// for all queried models, regardless of subscription changes during the request.
func TestSelectiveUpdaterQueriedModels(t *testing.T) {
	up := SelectiveUpdater()
	up.Init(UpdaterConfig{
		SiteOnlineModels:     toSet("a", "b", "c"),
		SubscriptionStatuses: map[string]StatusKind{"a": StatusOnline, "b": StatusOnline, "c": StatusOnline},
	})

	// We queried a, b, c and c went offline
	result := StatusResults{
		Request:  &StatusRequest{Models: toSet("a", "b", "c")},
		Statuses: map[string]StatusKind{"a": StatusOnline, "b": StatusOnline, "c": StatusOffline},
	}

	// c's update should be included since it was in the queried models
	updateResult := up.ProcessStreams(result)

	uSet := updatesSet(updateResult.Updates)
	expected := map[string]StatusKind{"c": StatusOffline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}
}

func TestMain(m *testing.M) {
	Verbosity = SilentVerbosity
	os.Exit(m.Run())
}
