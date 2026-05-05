package cmdlib

import (
	"errors"
	"os"
	"testing"
)

type TestChecker struct {
	CheckerCommon
	status StatusKind
	info   StreamerInfo
	online map[string]bool //nolint:structcheck
	err    error           //nolint:structcheck
}

type testOnlineListChecker struct {
	TestChecker
}

var queueSize = 1000

func (c *TestChecker) QueryStatus(string) (StreamerInfoWithStatus, error) {
	return StreamerInfoWithStatus{StreamerInfo: c.info, Status: c.status}, nil
}

func (*TestChecker) Capabilities() Capabilities {
	return Capabilities{
		QueryOnlineStreamers:          true,
		QueryFixedListOnlineStreamers: false,
		QueryFixedListStatuses:        false,
		QueryStatus:                   true,
	}
}

func (c *testOnlineListChecker) QueryOnlineStreamers() (map[string]StreamerInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	streamers := map[string]StreamerInfo{}
	for k := range c.online {
		streamers[k] = StreamerInfo{}
	}
	return streamers, nil
}

func (c *testOnlineListChecker) QueryFixedListOnlineStreamers(streamers []string, _ CheckMode) (map[string]StreamerInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	result := map[string]StreamerInfo{}
	for _, ch := range streamers {
		if c.online[ch] {
			result[ch] = StreamerInfo{}
		}
	}
	return result, nil
}

func (*testOnlineListChecker) Capabilities() Capabilities {
	return Capabilities{
		QueryOnlineStreamers:          true,
		QueryFixedListOnlineStreamers: true,
		QueryFixedListStatuses:        false,
		QueryStatus:                   true,
	}
}

func TestOnlineListCheckerHandlesFixedList(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	resultsCh := make(chan CheckerResults)
	StartCheckerDaemon(checker)

	checker.online = toSet("a", "b")
	if err := checker.PushStatusRequest(&FixedListOnlineRequest{
		ResultsCh: resultsCh,
		Streamers: toSet("a", "c"),
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	result := (<-resultsCh).(*FixedListOnlineResults)
	if result.Failed() {
		t.Error("unexpected error")
	}
	if _, ok := result.Streamers["a"]; !ok {
		t.Error("expected a to be in streamers (online)")
	}
	// c was queried but not online, should not be in the results
	if _, ok := result.Streamers["c"]; ok {
		t.Error("expected c to not be in streamers (not online)")
	}
	// b was online but not queried, should not be in the results
	if _, ok := result.Streamers["b"]; ok {
		t.Error("expected b to not be in streamers (not queried)")
	}
}

func TestSingleStatusRequestPropagatesInfo(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
	viewers := 42
	checker.info = StreamerInfo{
		ImageURL: "https://example/img.jpg",
		Viewers:  &viewers,
		ShowKind: ShowGroup,
		Subject:  "subject",
	}
	checker.status = StatusOnline
	resultsCh := make(chan *ExistenceListResults, 1)
	StartCheckerDaemon(checker)

	if err := checker.PushStatusRequest(&SingleStatusRequest{
		Streamer:  "alice",
		ResultsCh: resultsCh,
	}); err != nil {
		t.Fatalf("cannot push request, %v", err)
	}
	result := <-resultsCh
	if result.Failed() {
		t.Fatal("unexpected failure")
	}
	got, ok := result.Streamers["alice"]
	if !ok {
		t.Fatal("alice missing from results")
	}
	if got.Status != StatusOnline {
		t.Errorf("status: got %v, want %v", got.Status, StatusOnline)
	}
	if got.ImageURL != "https://example/img.jpg" {
		t.Errorf("image_url: got %q", got.ImageURL)
	}
	if got.Viewers == nil || *got.Viewers != 42 {
		t.Errorf("viewers: got %v", got.Viewers)
	}
	if got.ShowKind != ShowGroup {
		t.Errorf("show_kind: got %v", got.ShowKind)
	}
	if got.Subject != "subject" {
		t.Errorf("subject: got %q", got.Subject)
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
