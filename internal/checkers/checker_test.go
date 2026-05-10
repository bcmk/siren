package checkers

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

type testChecker struct {
	BaseChecker[*stubConfig]
	status cmdlib.StatusKind
	info   cmdlib.StreamerInfo
	online map[string]bool
	err    error
}

type testOnlineListChecker struct {
	testChecker
}

// stubConfig is a minimal config for tests that build a BaseChecker
// without going through the factory.
type stubConfig struct {
	BaseCheckerConfig
}

func (c *testChecker) QueryStatus(string) (cmdlib.StreamerInfoWithStatus, error) {
	return cmdlib.StreamerInfoWithStatus{StreamerInfo: c.info, Status: c.status}, nil
}

func (*testChecker) Site() string { return "test" }

// Init is a no-op: tests construct the checker and assign BaseChecker
// directly via NewBaseChecker, bypassing the production Init path.
func (*testChecker) Init(_ string, _ bool) error { return nil }

// Capabilities reports only QueryStatus: testChecker itself has no
// QueryOnlineStreamers method. testOnlineListChecker overrides this.
func (*testChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryStatus: true,
	}
}

func (c *testOnlineListChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	streamers := map[string]cmdlib.StreamerInfo{}
	for k := range c.online {
		streamers[k] = cmdlib.StreamerInfo{}
	}
	return streamers, nil
}

func (c *testOnlineListChecker) QueryFixedListOnlineStreamers(streamers []string, _ cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	result := map[string]cmdlib.StreamerInfo{}
	for _, ch := range streamers {
		if c.online[ch] {
			result[ch] = cmdlib.StreamerInfo{}
		}
	}
	return result, nil
}

func (*testOnlineListChecker) Capabilities() Capabilities {
	return Capabilities{
		SupportsQueryOnlineStreamers:          true,
		SupportsQueryFixedListOnlineStreamers: true,
		SupportsQueryFixedListStatuses:        false,
		SupportsQueryStatus:                   true,
	}
}

func TestOnlineListCheckerHandlesFixedList(t *testing.T) {
	checker := &testOnlineListChecker{}
	checker.BaseChecker = NewBaseChecker(&stubConfig{}, false)
	resultsCh := make(chan cmdlib.CheckerResults)
	StartCheckerDaemon(t.Context(), checker)

	checker.online = toSet("a", "b")
	if err := checker.PushStatusRequest(&cmdlib.FixedListOnlineRequest{
		ResultsCh: resultsCh,
		Streamers: toSet("a", "c"),
	}); err != nil {
		t.Errorf("cannot query updates, %v", err)
		return
	}
	result := (<-resultsCh).(*cmdlib.FixedListOnlineResults)
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
	checker.BaseChecker = NewBaseChecker(&stubConfig{}, false)
	viewers := 42
	checker.info = cmdlib.StreamerInfo{
		ImageURL: "https://example/img.jpg",
		Viewers:  &viewers,
		ShowKind: cmdlib.ShowGroup,
		Subject:  "subject",
	}
	checker.status = cmdlib.StatusOnline
	resultsCh := make(chan *cmdlib.ExistenceListResults, 1)
	StartCheckerDaemon(t.Context(), checker)

	if err := checker.PushStatusRequest(&cmdlib.SingleStatusRequest{
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
	if got.Status != cmdlib.StatusOnline {
		t.Errorf("status: got %v, want %v", got.Status, cmdlib.StatusOnline)
	}
	if got.ImageURL != "https://example/img.jpg" {
		t.Errorf("image_url: got %q", got.ImageURL)
	}
	if got.Viewers == nil || *got.Viewers != 42 {
		t.Errorf("viewers: got %v", got.Viewers)
	}
	if got.ShowKind != cmdlib.ShowGroup {
		t.Errorf("show_kind: got %v", got.ShowKind)
	}
	if got.Subject != "subject" {
		t.Errorf("subject: got %q", got.Subject)
	}
}

type testPolledChecker struct {
	BaseChecker[*stubConfig]
	online     map[string]bool
	individual map[string]cmdlib.StatusKind
	queryCalls []string
}

func (*testPolledChecker) Site() string { return "test" }

// Init is a no-op: tests construct the checker and assign BaseChecker
// directly via NewBaseChecker, bypassing the production Init path.
func (*testPolledChecker) Init(_ string, _ bool) error { return nil }

func (c *testPolledChecker) QueryStatus(nickname string) (cmdlib.StreamerInfoWithStatus, error) {
	c.queryCalls = append(c.queryCalls, nickname)
	status, ok := c.individual[nickname]
	if !ok {
		return cmdlib.StreamerInfoWithStatus{Status: cmdlib.StatusOffline}, nil
	}
	return cmdlib.StreamerInfoWithStatus{Status: status}, nil
}

func (c *testPolledChecker) QueryOnlineStreamers() (map[string]cmdlib.StreamerInfo, error) {
	out := map[string]cmdlib.StreamerInfo{}
	for k := range c.online {
		out[k] = cmdlib.StreamerInfo{}
	}
	return out, nil
}

func (*testPolledChecker) QueryFixedListOnlineStreamers([]string, cmdlib.CheckMode) (map[string]cmdlib.StreamerInfo, error) {
	return nil, ErrNotImplemented
}

func (*testPolledChecker) Capabilities() Capabilities {
	return Capabilities{SupportsQueryOnlineStreamers: true, SupportsQueryStatus: true}
}

func TestOnlineListCheckerPollsAdditionalStreamers(t *testing.T) {
	cases := []struct {
		name       string
		online     map[string]bool
		individual map[string]cmdlib.StatusKind
		poll       []string
		wantOnline map[string]bool
		wantCalls  map[string]bool
	}{
		{
			name:       "polled-only online appears in result",
			online:     toSet("a"),
			individual: map[string]cmdlib.StatusKind{"b": cmdlib.StatusOnline},
			poll:       []string{"a", "b"},
			wantOnline: toSet("a", "b"),
			wantCalls:  toSet("b"),
		},
		{
			name:       "polled-only offline does not appear",
			online:     toSet("a"),
			individual: map[string]cmdlib.StatusKind{"b": cmdlib.StatusOffline},
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
			checker.BaseChecker = NewBaseChecker(&stubConfig{}, false)
			resultsCh := make(chan cmdlib.CheckerResults, 1)
			StartCheckerDaemon(t.Context(), checker)

			if err := checker.PushStatusRequest(&cmdlib.OnlineListRequest{
				ResultsCh: resultsCh,
				Poll:      tc.poll,
			}); err != nil {
				t.Fatalf("cannot push request: %v", err)
			}
			result := (<-resultsCh).(*cmdlib.OnlineListResults)
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
	checker.BaseChecker = NewBaseChecker(&stubConfig{}, false)
	resultsCh := make(chan cmdlib.CheckerResults)
	StartCheckerDaemon(t.Context(), checker)

	checker.err = errors.New("error")
	if err := checker.PushStatusRequest(&cmdlib.OnlineListRequest{ResultsCh: resultsCh}); err != nil {
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
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	os.Exit(m.Run())
}
