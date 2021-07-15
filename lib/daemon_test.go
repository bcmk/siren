package lib

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

func (c *testSelectiveChecker) CheckStatusesMany([]string, CheckMode) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	return onlineMapToStatus(c.online), c.images, nil
}

func (c *testFullChecker) checkEndpoint(endpoint string) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	return onlineMapToStatus(c.online), c.images, nil
}

func (c *testFullChecker) CheckStatusesMany([]string, CheckMode) (onlineModels map[string]StatusKind, images map[string]string, err error) {
	return checkEndpoints(c, c.UsersOnlineEndpoints, c.Dbg)
}

// Start starts a daemon
func (c *testFullChecker) Start()                 { c.startFullCheckerDaemon(c) }
func (c *testFullChecker) createUpdater() Updater { return c.createFullUpdater(c) }

// Start starts a daemon
func (c *testSelectiveChecker) Start()                 { c.startSelectiveCheckerDaemon(c) }
func (c *testSelectiveChecker) createUpdater() Updater { return c.createSelectiveUpdater(c) }

func TestFullChecker(t *testing.T) {
	checker := &testFullChecker{}
	checker.Init(checker, CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize, SiteOnlineModels: toSet("a", "b")})
	resultsCh := make(chan StatusUpdateResults)
	callback := func(res StatusUpdateResults) { resultsCh <- res }
	checker.Start()
	up := checker.Updater()
	checker.online = toSet("b", "c")
	if err := up.QueryUpdates(StatusUpdateRequest{Callback: callback, SpecialModels: toSet(), Subscriptions: map[string]StatusKind{}}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	uSet := updatesSet((<-resultsCh).Data.Updates)
	expected := map[string]StatusKind{"a": StatusOffline, "c": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}
	checker.err = errors.New("error")
	if err := up.QueryUpdates(StatusUpdateRequest{Callback: callback, SpecialModels: toSet(), Subscriptions: map[string]StatusKind{}}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	upd := <-resultsCh
	if upd.Data != nil {
		t.Error("unexpected updates")
	}
	checker.err = nil
	checker.status = StatusOnline
	checker.online = toSet("a", "b")
	if err := up.QueryUpdates(StatusUpdateRequest{Callback: callback, SpecialModels: toSet("d"), Subscriptions: map[string]StatusKind{}}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	checker.err = nil
	uSet = updatesSet((<-resultsCh).Data.Updates)
	expected = map[string]StatusKind{"a": StatusOnline, "c": StatusOffline, "d": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}
}

func TestSelectiveChecker(t *testing.T) {
	checker := &testSelectiveChecker{}
	checker.Init(checker, CheckerConfig{
		QueueSize:        queueSize,
		SiteOnlineModels: toSet("a", "b"),
		Subscriptions:    map[string]StatusKind{"a": StatusOnline, "b": StatusOnline}})
	resultsCh := make(chan StatusUpdateResults)
	callback := func(res StatusUpdateResults) { resultsCh <- res }
	checker.Start()
	up := checker.Updater()
	checker.online = toSet("c")
	if err := up.QueryUpdates(StatusUpdateRequest{
		Callback:      callback,
		SpecialModels: toSet(),
		Subscriptions: map[string]StatusKind{"a": StatusOnline, "c": StatusUnknown},
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	uSet := updatesSet((<-resultsCh).Data.Updates)
	expected := map[string]StatusKind{"a": StatusOffline, "b": StatusUnknown, "c": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}

	checker.online = toSet("b", "c")
	if err := up.QueryUpdates(StatusUpdateRequest{
		Callback:      callback,
		SpecialModels: toSet(),
		Subscriptions: map[string]StatusKind{"b": StatusUnknown, "c": StatusOnline},
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	uSet = updatesSet((<-resultsCh).Data.Updates)
	expected = map[string]StatusKind{"a": StatusUnknown, "b": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}

	checker.err = errors.New("error")
	if err := up.QueryUpdates(StatusUpdateRequest{Callback: callback, SpecialModels: toSet(), Subscriptions: map[string]StatusKind{}}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	upd := <-resultsCh
	if upd.Data != nil {
		t.Error("unexpected updates")
	}

	checker.err = nil
	checker.online = toSet("b")
	if err := up.QueryUpdates(StatusUpdateRequest{
		Callback:      callback,
		SpecialModels: toSet(),
		Subscriptions: map[string]StatusKind{"b": StatusOnline, "c": StatusOnline},
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	uSet = updatesSet((<-resultsCh).Data.Updates)
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

func TestMain(m *testing.M) {
	Verbosity = SilentVerbosity
	os.Exit(m.Run())
}
