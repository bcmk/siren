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

type testPolledChecker struct {
	CheckerCommon
	online     map[string]bool
	individual map[string]StatusKind
	queryCalls []string
}

func (c *testPolledChecker) QueryStatus(nickname string) (StreamerInfoWithStatus, error) {
	c.queryCalls = append(c.queryCalls, nickname)
	status, ok := c.individual[nickname]
	if !ok {
		return StreamerInfoWithStatus{Status: StatusOffline}, nil
	}
	return StreamerInfoWithStatus{Status: status}, nil
}

func (c *testPolledChecker) QueryOnlineStreamers() (map[string]StreamerInfo, error) {
	out := map[string]StreamerInfo{}
	for k := range c.online {
		out[k] = StreamerInfo{}
	}
	return out, nil
}

func (*testPolledChecker) QueryFixedListOnlineStreamers([]string, CheckMode) (map[string]StreamerInfo, error) {
	return nil, ErrNotImplemented
}

func (*testPolledChecker) Capabilities() Capabilities {
	return Capabilities{QueryOnlineStreamers: true, QueryStatus: true}
}

func TestOnlineListCheckerPollsAdditionalStreamers(t *testing.T) {
	cases := []struct {
		name       string
		online     map[string]bool
		individual map[string]StatusKind
		poll       []string
		wantOnline map[string]bool
		wantCalls  map[string]bool
	}{
		{
			name:       "polled-only online appears in result",
			online:     toSet("a"),
			individual: map[string]StatusKind{"b": StatusOnline},
			poll:       []string{"a", "b"},
			wantOnline: toSet("a", "b"),
			wantCalls:  toSet("b"),
		},
		{
			name:       "polled-only offline does not appear",
			online:     toSet("a"),
			individual: map[string]StatusKind{"b": StatusOffline},
			poll:       []string{"a", "b"},
			wantOnline: toSet("a"),
			wantCalls:  toSet("b"),
		},
		{
			name:       "no poll set means no individual queries",
			online:     toSet("a"),
			poll:       nil,
			wantOnline: toSet("a"),
			wantCalls:  toSet(),
		},
		{
			name:       "polled streamer already in bulk is not re-queried",
			online:     toSet("a", "b"),
			poll:       []string{"a", "b"},
			wantOnline: toSet("a", "b"),
			wantCalls:  toSet(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &testPolledChecker{online: tc.online, individual: tc.individual}
			checker.Init(CheckerConfig{UsersOnlineEndpoints: []string{""}, QueueSize: queueSize})
			resultsCh := make(chan CheckerResults, 1)
			StartCheckerDaemon(checker)

			if err := checker.PushStatusRequest(&OnlineListRequest{
				ResultsCh: resultsCh,
				Poll:      tc.poll,
			}); err != nil {
				t.Fatalf("cannot push request: %v", err)
			}
			result := (<-resultsCh).(*OnlineListResults)
			if result.Failed() {
				t.Fatal("unexpected failure")
			}
			gotOnline := map[string]bool{}
			for k := range result.Streamers {
				gotOnline[k] = true
			}
			if !reflect.DeepEqual(gotOnline, tc.wantOnline) {
				t.Errorf("online: got %v, want %v", gotOnline, tc.wantOnline)
			}
			gotCalls := map[string]bool{}
			for _, n := range checker.queryCalls {
				gotCalls[n] = true
			}
			if !reflect.DeepEqual(gotCalls, tc.wantCalls) {
				t.Errorf("query calls: got %v, want %v", gotCalls, tc.wantCalls)
			}
		})
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
