package lib

import (
	"errors"
	"reflect"
	"testing"
	"time"
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

func (c *TestChecker) CheckSingle(string) StatusKind {
	return c.status
}

func (c *testSelectiveChecker) CheckMany(xs []string) (onlineModels map[string]bool, images map[string]string, err error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	return c.online, c.images, nil
}

func (c *testFullChecker) checkEndpoint(endpoint string) (onlineModels map[string]bool, images map[string]string, err error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	return c.online, c.images, nil
}

func (c *testFullChecker) CheckFull() (onlineModels map[string]bool, images map[string]string, err error) {
	return checkEndpoints(c, c.usersOnlineEndpoint, c.dbg)
}

func (c *testFullChecker) Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return fullDaemonStart(c, siteOnlineModels, intervalMs, dbg)
}

func (c *testSelectiveChecker) Start(siteOnlineModels map[string]bool, subscriptions map[string]StatusKind, intervalMs int, dbg bool) (
	statusRequests chan StatusRequest,
	resultsCh chan CheckerResults,
	errorsCh chan struct{},
	elapsedCh chan time.Duration,
) {
	return selectiveDaemonStart(c, siteOnlineModels, subscriptions, dbg)
}

func TestFullChecker(t *testing.T) {
	checker := testFullChecker{}
	checker.Init([]string{""}, nil, nil, false, nil)
	reqs, resultsCh, errs, elapsed := checker.Start(toSet("a", "b"), nil, 0, false)
	checker.online = toSet("b", "c")
	reqs <- StatusRequest{SpecialModels: toSet(), Subscriptions: map[string]StatusKind{}}
	<-elapsed
	uSet := updatesSet((<-resultsCh).Updates)
	expected := map[string]StatusKind{"a": StatusOffline, "c": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}
	checker.err = errors.New("error")
	reqs <- StatusRequest{SpecialModels: toSet(), Subscriptions: map[string]StatusKind{}}
	<-errs
	checker.err = nil
	checker.status = StatusOnline
	checker.online = toSet("a", "b")
	reqs <- StatusRequest{SpecialModels: toSet("d"), Subscriptions: map[string]StatusKind{}}
	<-elapsed
	uSet = updatesSet((<-resultsCh).Updates)
	expected = map[string]StatusKind{"a": StatusOnline, "c": StatusOffline, "d": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}
}

func TestSelectiveChecker(t *testing.T) {
	checker := testSelectiveChecker{}
	checker.Init(nil, nil, nil, false, nil)
	reqs, resultsCh, errs, elapsed := checker.Start(toSet("a", "b"), map[string]StatusKind{"a": StatusOnline, "b": StatusOnline}, 0, false)

	checker.online = toSet("c")
	reqs <- StatusRequest{SpecialModels: toSet(), Subscriptions: map[string]StatusKind{"a": StatusOnline, "c": StatusUnknown}}
	<-elapsed
	uSet := updatesSet((<-resultsCh).Updates)
	expected := map[string]StatusKind{"a": StatusOffline, "b": StatusUnknown, "c": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}

	checker.online = toSet("b", "c")
	reqs <- StatusRequest{SpecialModels: toSet(), Subscriptions: map[string]StatusKind{"b": StatusUnknown, "c": StatusOnline}}
	<-elapsed
	uSet = updatesSet((<-resultsCh).Updates)
	expected = map[string]StatusKind{"a": StatusUnknown, "b": StatusOnline}
	if !reflect.DeepEqual(uSet, expected) {
		t.Errorf("wrong updates, expected: %v, got: %v", expected, uSet)
	}

	checker.err = errors.New("error")
	reqs <- StatusRequest{SpecialModels: toSet(), Subscriptions: map[string]StatusKind{}}
	<-errs

	checker.err = nil
	checker.online = toSet("b")
	reqs <- StatusRequest{SpecialModels: toSet(), Subscriptions: map[string]StatusKind{"b": StatusOnline, "c": StatusOnline}}
	<-elapsed
	uSet = updatesSet((<-resultsCh).Updates)
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
