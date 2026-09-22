package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bcmk/siren/v5/internal/db"
	"github.com/bcmk/siren/v5/lib/cmdlib"
)

// statusChangeRow is a status_changes row without its streamer
type statusChangeRow struct {
	timestamp  int
	status     cmdlib.StatusKind
	prevStatus cmdlib.StatusKind
}

// streamerChanges returns a streamer's rows in timestamp order
func streamerChanges(d *db.Database, streamerID int) []statusChangeRow {
	var rows []statusChangeRow
	var row statusChangeRow
	d.MustQuery(`
		select timestamp, status, prev_status
		from status_changes
		where streamer_id = $1
		order by timestamp`,
		db.QueryParams{streamerID},
		db.ScanTo{&row.timestamp, &row.status, &row.prevStatus},
		func() { rows = append(rows, row) })
	return rows
}

// checkStatusChangesConsistent holds the invariants compaction must keep:
// prev_status names the row before, and streamers holds the last two rows.
func checkStatusChangesConsistent(t *testing.T, d *db.Database, nickname string) {
	t.Helper()
	s := d.MaybeStreamer(nickname)
	if s == nil {
		t.Fatalf("streamer %s not found", nickname)
	}
	rows := streamerChanges(d, s.ID)
	prev := cmdlib.StatusUnknown
	for _, r := range rows {
		if r.prevStatus != prev {
			t.Errorf("%s at %d: prev_status = %v, preceding status = %v", nickname, r.timestamp, r.prevStatus, prev)
		}
		prev = r.status
	}
	// A missing row reads as the zero value, which is what streamers holds for it
	var last, beforeLast statusChangeRow
	if len(rows) > 0 {
		last = rows[len(rows)-1]
	}
	if len(rows) > 1 {
		beforeLast = rows[len(rows)-2]
	}
	if s.UnconfirmedStatus != last.status || s.UnconfirmedTimestamp != last.timestamp {
		t.Errorf("%s: streamers holds %v at %d, last row is %v at %d",
			nickname, s.UnconfirmedStatus, s.UnconfirmedTimestamp, last.status, last.timestamp)
	}
	if s.PrevUnconfirmedStatus != beforeLast.status || s.PrevUnconfirmedTimestamp != beforeLast.timestamp {
		t.Errorf("%s: streamers holds prev %v at %d, row before last is %v at %d",
			nickname, s.PrevUnconfirmedStatus, s.PrevUnconfirmedTimestamp, beforeLast.status, beforeLast.timestamp)
	}
}

type compactionInsert struct {
	ts     int
	status cmdlib.StatusKind
}

func insertHistory(d *db.Database, nickname string, inserts []compactionInsert) {
	for _, ins := range inserts {
		d.UpsertUnconfirmedStatusChanges([]db.StatusChange{{Nickname: nickname, Status: ins.status}}, ins.ts)
	}
}

// compact makes one run with no vacuum delay,
// then vacuums every chunk left queued, which the daemon spreads over its next runs
func compact(
	ctx context.Context,
	d *db.Database,
	rules []db.ShortOfflineRule,
	stepSeconds int,
	now int,
) ([]int, int64, error) {
	c, err := run(ctx, d, stepSeconds, rules, 0, now)
	if err != nil {
		return nil, 0, err
	}
	if _, err := vacuumQueued(ctx, d); err != nil {
		return nil, 0, err
	}
	return c.ends(), c.deleted, nil
}

// mustCompact makes a run as compact does and fails the test if the run errs
func mustCompact(
	t *testing.T,
	d *db.Database,
	rules []db.ShortOfflineRule,
	stepSeconds int,
	now int,
) ([]int, int64) {
	t.Helper()
	ends, deleted, err := compact(context.Background(), d, rules, stepSeconds, now)
	if err != nil {
		t.Fatal(err)
	}
	return ends, deleted
}

// testRules are the compactor's default rules
var testRules = []db.ShortOfflineRule{
	{After: 7 * day, ShorterThan: 15 * 60},
	{After: 30 * day, ShorterThan: 30 * 60},
	{After: 60 * day, ShorterThan: 60 * 60, Rewrite: true},
}

func coverageBegin(d *db.Database, rule db.ShortOfflineRule) int {
	return d.MustInt("select begin_timestamp from status_changes_lock where after = $1", rule.After)
}

func coverageEnd(d *db.Database, rule db.ShortOfflineRule) int {
	return d.MustInt("select end_timestamp from status_changes_lock where after = $1", rule.After)
}

// TestCompactStatusChanges makes one run over a whole history, so every rule passes it
func TestCompactStatusChanges(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	const (
		unknown = cmdlib.StatusUnknown
		offline = cmdlib.StatusOffline
		online  = cmdlib.StatusOnline
	)
	tests := []struct {
		name    string
		inserts []compactionInsert
		// the timestamps left after compaction
		want []int
	}{
		{
			name: "short periods go by their tier",
			inserts: []compactionInsert{
				{now - 70*day, online},
				{now - 70*day + 600, offline},
				{now - 70*day + 3600, online},
				{now - 40*day, offline},
				{now - 40*day + 1200, online},
				{now - 40*day + 3600, offline},
				{now - 40*day + 6000, online},
				{now - 10*day, offline},
				{now - 10*day + 600, online},
				{now - 10*day + 3600, offline},
				{now - 10*day + 4800, online},
				{now - 3*day, offline},
				{now - 3*day + 300, online},
				{now - day, offline},
				{now - 3600, online},
			},
			want: []int{
				now - 70*day,
				now - 40*day + 3600,
				now - 40*day + 6000,
				now - 10*day + 3600,
				now - 10*day + 4800,
				now - 3*day,
				now - 3*day + 300,
				now - day,
				now - 3600,
			},
		},
		{
			name: "a period as long as the threshold stays",
			inserts: []compactionInsert{
				{now - 8*day, online},
				{now - 8*day + 100, offline},
				{now - 8*day + 1000, online},
				{now - day, offline},
				{now - 3600, online},
			},
			want: []int{now - 8*day, now - 8*day + 100, now - 8*day + 1000, now - day, now - 3600},
		},
		{
			name: "the threshold grows with age",
			inserts: []compactionInsert{
				{now - 61*day, online},
				{now - 61*day + 100, offline},
				{now - 61*day + 3000, online},
				{now - 31*day, offline},
				{now - 31*day + 1700, online},
				{now - 31*day + 2000, offline},
				{now - 31*day + 3900, online},
				{now - day, offline},
				{now - 3600, online},
			},
			want: []int{now - 61*day, now - 31*day + 2000, now - 31*day + 3900, now - day, now - 3600},
		},
		{
			name: "a period across a tier line follows its end",
			inserts: []compactionInsert{
				{now - 40*day, online},
				{now - 30*day - 600, offline},
				{now - 30*day + 600, online},
				{now - day, offline},
				{now - 3600, online},
			},
			want: []int{now - 40*day, now - 30*day - 600, now - 30*day + 600, now - day, now - 3600},
		},
		{
			name: "a short period across a tier line goes",
			inserts: []compactionInsert{
				{now - 40*day, online},
				{now - 30*day - 300, offline},
				{now - 30*day + 300, online},
				{now - day, offline},
				{now - 3600, online},
			},
			want: []int{now - 40*day, now - day, now - 3600},
		},
		{
			name: "a period across the week line stays",
			inserts: []compactionInsert{
				{now - 10*day, online},
				{now - 7*day - 100, offline},
				{now - 7*day + 200, online},
				{now - day, offline},
				{now - 3600, online},
			},
			want: []int{now - 10*day, now - 7*day - 100, now - 7*day + 200, now - day, now - 3600},
		},
		{
			name: "adjacent short periods both go",
			inserts: []compactionInsert{
				{now - 8*day, online},
				{now - 8*day + 100, offline},
				{now - 8*day + 200, online},
				{now - 8*day + 300, offline},
				{now - 8*day + 400, online},
				{now - day, offline},
				{now - 3600, online},
			},
			want: []int{now - 8*day, now - day, now - 3600},
		},
		{
			name:    "one row",
			inserts: []compactionInsert{{now - 30*day, online}},
			want:    []int{now - 30*day},
		},
		{
			name:    "two rows",
			inserts: []compactionInsert{{now - 30*day, online}, {now - 30*day + 300, offline}},
			want:    []int{now - 30*day, now - 30*day + 300},
		},
		{
			name: "a period ending in the latest row stays however old",
			inserts: []compactionInsert{
				{now - 30*day, online},
				{now - 30*day + 300, offline},
				{now - 30*day + 600, online},
			},
			want: []int{now - 30*day, now - 30*day + 300, now - 30*day + 600},
		},
		{
			name: "a period ending in the row before the latest stays however old",
			inserts: []compactionInsert{
				{now - 30*day, online},
				{now - 30*day + 300, offline},
				{now - 30*day + 600, online},
				{now - 30*day + 900, offline},
			},
			want: []int{now - 30*day, now - 30*day + 300, now - 30*day + 600, now - 30*day + 900},
		},
		{
			name: "a period after unknown stays",
			inserts: []compactionInsert{
				{now - 30*day, unknown},
				{now - 30*day + 300, offline},
				{now - 30*day + 600, online},
				{now - 20*day, offline},
				{now - 20*day + 6000, online},
			},
			want: []int{
				now - 30*day,
				now - 30*day + 300,
				now - 30*day + 600,
				now - 20*day,
				now - 20*day + 6000,
			},
		},
		{
			name: "a gap of unknown between online stretches stays",
			inserts: []compactionInsert{
				{now - 40*day, online},
				{now - 20*day, unknown},
				{now - 20*day + 300, online},
				{now - day, offline},
				{now - 3600, online},
			},
			want: []int{now - 40*day, now - 20*day, now - 20*day + 300, now - day, now - 3600},
		},
		{
			name: "a period ending in unknown stays",
			inserts: []compactionInsert{
				{now - 30*day, online},
				{now - 30*day + 300, offline},
				{now - 30*day + 600, unknown},
				{now - 20*day, online},
				{now - 20*day + 6000, offline},
			},
			want: []int{
				now - 30*day,
				now - 30*day + 300,
				now - 30*day + 600,
				now - 20*day,
				now - 20*day + 6000,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tdb := newTestDB(t)
			defer tdb.terminate()
			insertHistory(tdb.Database, "a", tt.inserts)

			ends, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now)

			if ends[0] != now-7*day {
				t.Errorf("got to %d, want %d", ends[0], now-7*day)
			}
			if want := int64(len(tt.inserts) - len(tt.want)); deleted != want {
				t.Errorf("deleted %d rows, want %d", deleted, want)
			}
			var got []int
			for _, r := range streamerChanges(tdb.Database, streamerID(t, tdb.Database, "a")) {
				got = append(got, r.timestamp)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rows left %v, want %v", got, tt.want)
			}
			checkStatusChangesConsistent(t, tdb.Database, "a")
		})
	}
}

// shortHistory holds one short period ending 20 days before now and three rows a run keeps
var shortHistory = []compactionInsert{
	{200*day - 40*day, cmdlib.StatusOnline},
	{200*day - 20*day, cmdlib.StatusOffline},
	{200*day - 20*day + 300, cmdlib.StatusOnline},
	{200*day - day, cmdlib.StatusOffline},
	{200*day - 3600, cmdlib.StatusOnline},
}

// TestCompactStatusChangesWalks pins the week rule's walk: it begins at the next rule's age,
// moves a step a run, deletes a period once its band reaches the end, and stops at its own age
func TestCompactStatusChangesWalks(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", shortHistory)
	a := streamerID(t, tdb.Database, "a")

	// The week rule begins 30 days before now, at the next rule's age,
	// and an 8-day step falls short of the period's end
	ends, deleted := mustCompact(t, tdb.Database, testRules, 8*day, now)
	if ends[0] != now-22*day || deleted != 0 {
		t.Errorf("first run got to %d deleting %d, want %d deleting 0", ends[0], deleted, now-22*day)
	}
	if got := coverageEnd(tdb.Database, testRules[0]); got != now-22*day {
		t.Errorf("end = %d after the first run, want %d", got, now-22*day)
	}
	if got := coverageBegin(tdb.Database, testRules[0]); got != now-30*day {
		t.Errorf("begin = %d after the first run, want %d", got, now-30*day)
	}
	if rows := streamerChanges(tdb.Database, a); len(rows) != len(shortHistory) {
		t.Errorf("a has %d rows after the first run, want %d", len(rows), len(shortHistory))
	}
	ends, deleted = mustCompact(t, tdb.Database, testRules, 8*day, now)
	if ends[0] != now-14*day || deleted != 2 {
		t.Errorf("second run got to %d deleting %d, want %d deleting 2", ends[0], deleted, now-14*day)
	}
	// The rule's own age before now is the end of the walk
	ends, deleted = mustCompact(t, tdb.Database, testRules, 8*day, now)
	if ends[0] != now-7*day || deleted != 0 {
		t.Errorf("third run got to %d deleting %d, want %d deleting 0", ends[0], deleted, now-7*day)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// TestCompactStatusChangesBandEdges pins the band's edges on the period in shortHistory:
// a run whose week band ends inside the period or exactly at its end leaves it,
// and the next run, whose band begins there, takes it
func TestCompactStatusChangesBandEdges(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tests := []struct {
		name string
		step int
	}{
		// From 30 days before now, the week rule's band ends 100 seconds after the period's start
		{name: "an end past the band's end", step: 10*day + 100},
		{name: "an end exactly at the band's end", step: 10*day + 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tdb := newTestDB(t)
			defer tdb.terminate()
			insertHistory(tdb.Database, "a", shortHistory)

			ends, deleted := mustCompact(t, tdb.Database, testRules, tt.step, now)
			if want := now - 30*day + tt.step; ends[0] != want || deleted != 0 {
				t.Errorf("first run got to %d deleting %d, want %d deleting 0", ends[0], deleted, want)
			}
			if _, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now); deleted != 2 {
				t.Errorf("second run deleted %d rows, want 2", deleted)
			}
			checkStatusChangesConsistent(t, tdb.Database, "a")
		})
	}
}

// TestCompactStatusChangesSkipsABusyLock pins the lock:
// a run finding status_changes_lock held in exclusive mode skips,
// and the next one sees the period the editor added
func TestCompactStatusChangesSkipsABusyLock(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", shortHistory)
	a := streamerID(t, tdb.Database, "a")

	editor := db.NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = editor.Close() }()
	ctx := context.Background()
	edit, err := editor.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edit.Exec(ctx, "lock table status_changes_lock in exclusive mode"); err != nil {
		t.Fatal(err)
	}
	// A second short period, 35 days old, between the history's first two rows
	if _, err := edit.Exec(ctx, `
		insert into status_changes (timestamp, streamer_id, status, prev_status)
		values ($1, $2, 1, 2), ($1 + 300, $2, 2, 1)`,
		now-35*day, a); err != nil {
		t.Fatal(err)
	}

	ends, deleted, err := compact(ctx, tdb.Database, testRules, 400*day, now)
	if !errors.Is(err, db.ErrLockBusy) || ends != nil || deleted != 0 {
		t.Errorf("the run under the editor's lock got %v, %d, %v, want nothing and ErrLockBusy", ends, deleted, err)
	}
	if err := edit.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, deleted, err := compact(ctx, tdb.Database, testRules, 400*day, now); err != nil || deleted != 4 {
		t.Errorf("the run after the editor deleted %d rows with %v, want both periods' 4", deleted, err)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// TestCompactStatusChangesReadsTheRightStreamer pins the streamers lookup's join:
// a period in a quiet streamer's last two rows stays, whatever a busy streamer's rows say
func TestCompactStatusChangesReadsTheRightStreamer(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", []compactionInsert{
		{now - 40*day, cmdlib.StatusOnline},
		{now - 20*day, cmdlib.StatusOffline},
		{now - 20*day + 300, cmdlib.StatusOnline},
	})
	insertHistory(tdb.Database, "b", []compactionInsert{
		{now - 40*day, cmdlib.StatusOnline},
		{now - day, cmdlib.StatusOffline},
		{now - 3600, cmdlib.StatusOnline},
	})
	if _, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now); deleted != 0 {
		t.Errorf("deleted %d rows, want 0", deleted)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
	checkStatusChangesConsistent(t, tdb.Database, "b")
}

// TestCompactStatusChangesRunsOneAtATime pins the compactor's own lock:
// of two runs held up together one skips, and the next run finds the other's coverages
func TestCompactStatusChangesRunsOneAtATime(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", shortHistory)
	other := db.NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = other.Close() }()
	holder := db.NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = holder.Close() }()

	ctx := context.Background()
	hold, err := holder.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// Holds the run that got the lock at its delete from status_changes, so the other finds it held
	if _, err := hold.Exec(ctx, "lock table status_changes in share mode"); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		end     int
		deleted int64
		failure any
	}
	outcomes := make(chan outcome)
	runCtx, stopRuns := context.WithCancel(ctx)
	defer stopRuns()
	for _, d := range []*db.Database{tdb.Database, &other} {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					outcomes <- outcome{failure: r}
				}
			}()
			ends, n, err := compact(runCtx, d, testRules, 8*day, now)
			if err != nil {
				outcomes <- outcome{failure: err}
				return
			}
			outcomes <- outcome{end: ends[0], deleted: n}
		}()
	}
	received := 0
	receive := func() outcome {
		select {
		case o := <-outcomes:
			received++
			return o
		case <-time.After(30 * time.Second):
			// Stopped and drained, or the deferred closes would pull the connections from under them
			stopRuns()
			for ; received < 2; received++ {
				<-outcomes
			}
			t.Fatal("a run did not finish")
			return outcome{}
		}
	}
	// The run that lost the lock reports while the other still waits at its delete
	first := receive()
	if err := hold.Commit(ctx); err != nil {
		t.Error(err)
	}
	second := receive()
	var deleted int64
	var weekEnds []int
	skipped := 0
	for _, o := range []outcome{first, second} {
		if failure, ok := o.failure.(error); ok && errors.Is(failure, db.ErrLockBusy) {
			skipped++
			continue
		}
		if o.failure != nil {
			t.Errorf("a run failed: %v", o.failure)
		}
		deleted += o.deleted
		weekEnds = append(weekEnds, o.end)
	}
	// One run made its first step, the other skipped; the next run takes up where the first ended
	if skipped != 1 || !reflect.DeepEqual(weekEnds, []int{now - 22*day}) || deleted != 0 {
		t.Errorf("%d runs skipped, the week rule's ends were %v deleting %d, want 1, [%d] and 0",
			skipped, weekEnds, deleted, now-22*day)
	}
	ends, n, err := compact(ctx, tdb.Database, testRules, 8*day, now)
	if err != nil || ends[0] != now-14*day || n != 2 {
		t.Errorf("the next run got to %v deleting %d with %v, want %d deleting 2", ends, n, err, now-14*day)
	}
	if stored := tdb.MustInt("select count(*) from status_changes_lock"); stored != len(testRules) {
		t.Errorf("%d coverages stored, want %d", stored, len(testRules))
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// TestCompactStatusChangesTellsStreamersApart pins the joins on streamer_id:
// two streamers change at the same timestamps, and only the one with a short period loses rows
func TestCompactStatusChangesTellsStreamersApart(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// a has a 5-minute period ending 20 days ago; b is online for those 5 minutes, after 20 offline
	for _, ins := range []struct {
		ts     int
		status map[string]cmdlib.StatusKind
	}{
		{now - 40*day, map[string]cmdlib.StatusKind{"a": cmdlib.StatusOnline, "b": cmdlib.StatusOnline}},
		{now - 20*day - 1200, map[string]cmdlib.StatusKind{"b": cmdlib.StatusOffline}},
		{now - 20*day, map[string]cmdlib.StatusKind{"a": cmdlib.StatusOffline, "b": cmdlib.StatusOnline}},
		{now - 20*day + 300, map[string]cmdlib.StatusKind{"a": cmdlib.StatusOnline, "b": cmdlib.StatusOffline}},
		{now - day, map[string]cmdlib.StatusKind{"a": cmdlib.StatusOffline, "b": cmdlib.StatusOnline}},
		{now - 3600, map[string]cmdlib.StatusKind{"a": cmdlib.StatusOnline, "b": cmdlib.StatusOffline}},
	} {
		var changes []db.StatusChange
		for nickname, status := range ins.status {
			changes = append(changes, db.StatusChange{Nickname: nickname, Status: status})
		}
		tdb.UpsertUnconfirmedStatusChanges(changes, ins.ts)
	}
	b := streamerID(t, tdb.Database, "b")

	if _, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now); deleted != 2 {
		t.Errorf("deleted %d rows, want a's 2", deleted)
	}
	if rows := streamerChanges(tdb.Database, b); len(rows) != 6 {
		t.Errorf("b has %d rows, want all 6", len(rows))
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
	checkStatusChangesConsistent(t, tdb.Database, "b")
}

// TestCompactStatusChangesTakesOverADroppedOlderRule pins the oldest rule's begin:
// a rule dropped before its walk reached a stretch leaves that stretch to the oldest rule left
func TestCompactStatusChangesTakesOverADroppedOlderRule(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// A 20-minute period ending 100 days ago, over the week threshold but under the 30-day one
	insertHistory(tdb.Database, "a", []compactionInsert{
		{now - 200*day, cmdlib.StatusOnline},
		{now - 100*day, cmdlib.StatusOffline},
		{now - 100*day + 1200, cmdlib.StatusOnline},
		{now - day, cmdlib.StatusOffline},
		{now - 3600, cmdlib.StatusOnline},
	})
	// One run of a 5-day step: the 60-day rule has 195 days to go
	if _, deleted := mustCompact(t, tdb.Database, testRules, 5*day, now); deleted != 0 {
		t.Errorf("the first run deleted %d rows, want 0", deleted)
	}
	_, deleted := mustCompact(t, tdb.Database, testRules[:2], 400*day, now)
	if deleted != 2 {
		t.Errorf("the run without the 60-day rule deleted %d rows, want 2", deleted)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// TestCompactStatusChangesRestartsAChangedRule pins reconfiguration:
// a rule with a larger threshold sweeps again from where a coverage at one at least its own ends,
// the others keep their walk, and a dropped rule forgets its walk
func TestCompactStatusChangesRestartsAChangedRule(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// A 40-minute period ending 31 days ago, over the 30-minute threshold
	insertHistory(tdb.Database, "a", []compactionInsert{
		{now - 40*day, cmdlib.StatusOnline},
		{now - 31*day, cmdlib.StatusOffline},
		{now - 31*day + 2400, cmdlib.StatusOnline},
		{now - day, cmdlib.StatusOffline},
		{now - 3600, cmdlib.StatusOnline},
	})
	if _, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now); deleted != 0 {
		t.Errorf("the default rules deleted %d rows, want 0", deleted)
	}

	wider := []db.ShortOfflineRule{testRules[0], {After: 30 * day, ShorterThan: 45 * 60}}
	// The hour rule's coverage, with nothing 60 days old, is empty,
	// so the wider rule takes up there, and a step short of the period cannot reach it yet
	if _, deleted := mustCompact(t, tdb.Database, wider, 5*day, now); deleted != 0 {
		t.Errorf("the first run of the wider rule deleted %d rows, want 0", deleted)
	}
	if got := coverageEnd(tdb.Database, wider[1]); got != now-35*day {
		t.Errorf("the wider rule's end = %d, want %d", got, now-35*day)
	}
	if got := coverageEnd(tdb.Database, wider[0]); got != now-7*day {
		t.Errorf("the unchanged rule's end = %d, want %d", got, now-7*day)
	}
	if _, deleted := mustCompact(t, tdb.Database, wider, 400*day, now); deleted != 2 {
		t.Errorf("the second run of the wider rule deleted %d rows, want 2", deleted)
	}
	if left := tdb.MustInt("select count(*) from status_changes_lock"); left != 2 {
		t.Errorf("%d rules stored, want the 2 configured", left)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// TestCompactStatusChangesKeepsAWalkOnATighterThreshold pins a smaller threshold:
// the rule's walk goes on, since the looser threshold deleted everything the tighter one would
func TestCompactStatusChangesKeepsAWalkOnATighterThreshold(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", shortHistory)
	if _, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now); deleted != 2 {
		t.Errorf("the default rules deleted %d rows, want 2", deleted)
	}

	tighter := []db.ShortOfflineRule{testRules[0], {After: 30 * day, ShorterThan: 20 * 60}, testRules[2]}
	// A step short of the history would show a restart
	if _, deleted := mustCompact(t, tdb.Database, tighter, 5*day, now); deleted != 0 {
		t.Errorf("the tighter rule deleted %d rows, want 0", deleted)
	}
	if got := coverageEnd(tdb.Database, tighter[1]); got != now-30*day {
		t.Errorf("the tighter rule's end = %d, want %d", got, now-30*day)
	}
	if got := tdb.MustInt("select shorter_than from status_changes_lock where after = $1", 30*day); got != 20*60 {
		t.Errorf("the tightened rule stores shorter_than = %d, want %d", got, 20*60)
	}
}

// TestCompactStatusChangesTakesUpAMovedRule pins reconfiguration by a shifted age:
// the rule takes up where the pass it replaces got to, in its own age, so only a day of ends is new
func TestCompactStatusChangesTakesUpAMovedRule(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// A 25-minute period ending 29 and a half days ago, a day short of the 30-minute rule
	insertHistory(tdb.Database, "a", []compactionInsert{
		{now - 40*day, cmdlib.StatusOnline},
		{now - 29*day - day/2, cmdlib.StatusOffline},
		{now - 29*day - day/2 + 1500, cmdlib.StatusOnline},
		{now - day, cmdlib.StatusOffline},
		{now - 3600, cmdlib.StatusOnline},
	})
	if _, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now); deleted != 0 {
		t.Errorf("the default rules deleted %d rows, want 0", deleted)
	}

	moved := []db.ShortOfflineRule{testRules[0], {After: 29 * day, ShorterThan: 30 * 60}, testRules[2]}
	// A step short of the history would show a restart
	if _, deleted := mustCompact(t, tdb.Database, moved, 5*day, now); deleted != 2 {
		t.Errorf("the moved rule deleted %d rows, want 2", deleted)
	}
	if got := coverageEnd(tdb.Database, moved[1]); got != now-29*day {
		t.Errorf("the moved rule's end = %d, want %d", got, now-29*day)
	}
	if stored := tdb.MustInt("select count(*) from status_changes_lock"); stored != 3 {
		t.Errorf("%d rules stored, want the 3 configured", stored)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// TestCompactStatusChangesReachesBackForARaisedAge pins reconfiguration by an older next rule:
// the stretch between the new age and the old one,
// which that rule never reached, is this rule's now
func TestCompactStatusChangesReachesBackForARaisedAge(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// A 10-minute period ending 75 days ago, under every threshold
	insertHistory(tdb.Database, "a", []compactionInsert{
		{now - 120*day, cmdlib.StatusOnline},
		{now - 75*day, cmdlib.StatusOffline},
		{now - 75*day + 600, cmdlib.StatusOnline},
		{now - day, cmdlib.StatusOffline},
		{now - 3600, cmdlib.StatusOnline},
	})
	rules := []db.ShortOfflineRule{testRules[0], testRules[2]}
	// One run of a day's step: the week rule got to 59 days ago, the 60-day rule to 119
	if _, deleted := mustCompact(t, tdb.Database, rules, day, now); deleted != 0 {
		t.Errorf("the first run deleted %d rows, want 0", deleted)
	}

	raised := []db.ShortOfflineRule{testRules[0], {After: 90 * day, ShorterThan: 60 * 60}}
	if _, deleted := mustCompact(t, tdb.Database, raised, 400*day, now); deleted != 2 {
		t.Errorf("the run with the raised age deleted %d rows, want 2", deleted)
	}
	// The week rule's coverage runs from the raised age, through its day, to its own age
	if got := coverageBegin(tdb.Database, raised[0]); got != now-90*day {
		t.Errorf("the week rule's begin = %d, want %d", got, now-90*day)
	}
	if got := coverageEnd(tdb.Database, raised[0]); got != now-7*day {
		t.Errorf("the week rule's end = %d, want %d", got, now-7*day)
	}
	if got := coverageEnd(tdb.Database, raised[1]); got != now-90*day {
		t.Errorf("the raised rule's end = %d, want %d", got, now-90*day)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// TestCompactStatusChangesAddsAnOlderRule pins reconfiguration by a bigger rule:
// it sweeps the history older than its age alone, and the others keep their walk
func TestCompactStatusChangesAddsAnOlderRule(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// A 90-minute period ending 100 days ago, over every default threshold
	insertHistory(tdb.Database, "a", []compactionInsert{
		{now - 120*day, cmdlib.StatusOnline},
		{now - 100*day, cmdlib.StatusOffline},
		{now - 100*day + 5400, cmdlib.StatusOnline},
		{now - day, cmdlib.StatusOffline},
		{now - 3600, cmdlib.StatusOnline},
	})
	if _, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now); deleted != 0 {
		t.Errorf("the default rules deleted %d rows, want 0", deleted)
	}

	older := append(slices.Clone(testRules), db.ShortOfflineRule{After: 90 * day, ShorterThan: 120 * 60})
	// A step short of the history: the new rule starts over, so it cannot reach the period yet
	if _, deleted := mustCompact(t, tdb.Database, older, 5*day, now); deleted != 0 {
		t.Errorf("the first run of the older rule deleted %d rows, want 0", deleted)
	}
	if got := coverageEnd(tdb.Database, older[3]); got != now-115*day {
		t.Errorf("the older rule's end = %d, want %d", got, now-115*day)
	}
	for _, rule := range testRules {
		if got := coverageEnd(tdb.Database, rule); got != now-rule.After {
			t.Errorf("the unchanged rule after %d has end = %d, want %d", rule.After, got, now-rule.After)
		}
	}
	if _, deleted := mustCompact(t, tdb.Database, older, 400*day, now); deleted != 2 {
		t.Errorf("the second run of the older rule deleted %d rows, want 2", deleted)
	}
	if stored := tdb.MustInt("select count(*) from status_changes_lock"); stored != 4 {
		t.Errorf("%d rules stored, want the 4 configured", stored)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// chunkVacuums returns each chunk's vacuum count by its number on the test database's 7-day grid
func chunkVacuums(d *db.Database) map[int]int {
	counts := map[int]int{}
	var chunk, vacuums int
	d.MustQuery(`
		select c.range_start_integer / $1, s.vacuum_count
		from timescaledb_information.chunks c
		join pg_stat_all_tables s on s.schemaname = c.chunk_schema and s.relname = c.chunk_name
		where c.hypertable_name = 'status_changes'`,
		db.QueryParams{7 * day},
		db.ScanTo{&chunk, &vacuums},
		func() { counts[chunk] = vacuums })
	return counts
}

// TestCompactStatusChangesVacuumsFinishedChunks pins the vacuums:
// a rule's band queues a chunk once past its end by the threshold plus the delay, and once only
func TestCompactStatusChangesVacuumsFinishedChunks(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// Rows in chunks 18, 20, 24, 27 and 28; the hour's periods are over every threshold
	insertHistory(tdb.Database, "a", []compactionInsert{
		{130 * day, cmdlib.StatusOnline},
		{145 * day, cmdlib.StatusOffline},
		{145*day + 3600, cmdlib.StatusOnline},
		{172 * day, cmdlib.StatusOffline},
		{172*day + 3600, cmdlib.StatusOnline},
		{190 * day, cmdlib.StatusOffline},
		{190*day + 3600, cmdlib.StatusOnline},
		{199 * day, cmdlib.StatusOffline},
		{199*day + 3600, cmdlib.StatusOnline},
	})
	runs := []struct {
		name  string
		now   int
		delay int
		want  map[int]int
	}{
		// The bands, [130d, 140d), [140d, 170d) and [170d, 193d), finish chunks 18, 20 and 24
		{"the first run", now, 0, map[int]int{18: 1, 20: 1, 24: 1, 27: 0, 28: 0}},
		{"a period later", now + 30, 0, map[int]int{18: 1, 20: 1, 24: 1, 27: 0, 28: 0}},
		// The week band passes 196d, chunk 27's end, by a threshold, but not by the delay too
		{"short of the delay", now + 3*day + 900, 600, map[int]int{18: 1, 20: 1, 24: 1, 27: 0, 28: 0}},
		{"past the delay", now + 3*day + 1500, 600, map[int]int{18: 1, 20: 1, 24: 1, 27: 1, 28: 0}},
		// The 30-day band finishes chunk 24, which the week band finished already
		{"another rule", now + 5*day + 2400, 600, map[int]int{18: 1, 20: 1, 24: 2, 27: 1, 28: 0}},
	}
	// The runs share the database, so each compacts on the parent, and only its check is a subtest
	ctx := context.Background()
	for _, r := range runs {
		if _, err := run(ctx, tdb.Database, 400*day, testRules, r.delay, r.now); err != nil {
			t.Fatal(err)
		}
		if _, err := vacuumQueued(ctx, tdb.Database); err != nil {
			t.Fatal(err)
		}
		t.Run(r.name, func(t *testing.T) {
			if got := chunkVacuums(tdb.Database); !reflect.DeepEqual(got, r.want) {
				t.Errorf("vacuums = %v, want %v", got, r.want)
			}
		})
	}
}

// vacuumQueued vacuums every queued chunk and returns how many it vacuumed
func vacuumQueued(ctx context.Context, d *db.Database) (int, error) {
	vacuumed := 0
	for {
		n, err := d.VacuumNextStatusChangesChunk(ctx, true)
		if err != nil || n == 0 {
			return vacuumed, err
		}
		vacuumed += n
	}
}

// TestCompactStatusChangesKeepsNowMonotonic pins the run's clock:
// a run timed before the last one takes the last one's time, so its coverages still hold
func TestCompactStatusChangesKeepsNowMonotonic(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", shortHistory)
	first, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now)
	if deleted != 2 {
		t.Errorf("the first run deleted %d rows, want 2", deleted)
	}
	// A second earlier: the bands would move back and walk again
	ends, deleted := mustCompact(t, tdb.Database, testRules, 5*day, now-1)
	if !reflect.DeepEqual(ends, first) || deleted != 0 {
		t.Errorf("the earlier run got to %v deleting %d, want %v deleting 0", ends, deleted, first)
	}
}

// TestCompactStatusChangesStopsWithTheContext pins the shutdown path:
// a cancel in the middle of a run stops it at once with its error, and the history stays as it was
func TestCompactStatusChangesStopsWithTheContext(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", shortHistory)
	a := streamerID(t, tdb.Database, "a")
	// pgx closes a connection whose statement it cancels, so the run gets one of its own
	runner := db.NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = runner.Close() }()
	holder := db.NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = holder.Close() }()

	ctx := context.Background()
	hold, err := holder.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	// Holds the run at its delete from status_changes
	if _, err := hold.Exec(ctx, "lock table status_changes in share mode"); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := compact(runCtx, &runner, testRules, 400*day, now)
		result <- err
	}()
	// Before a fatal, so the deferred closes never pull the connections from under the run
	stop := func() {
		cancel()
		_ = hold.Rollback(ctx)
		<-result
	}
	deadline := time.Now().Add(30 * time.Second)
	for tdb.MustInt(`
		select count(*)
		from pg_stat_activity
		where datname = current_database() and wait_event_type = 'Lock'`) == 0 {
		if time.Now().After(deadline) {
			stop()
			t.Fatal("the run never waited at its delete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		stop()
		t.Fatal("the run did not stop with its context")
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if rows := streamerChanges(tdb.Database, a); len(rows) != len(shortHistory) {
		t.Errorf("a has %d rows after the cancelled run, want %d", len(rows), len(shortHistory))
	}
}

// TestCompactStatusChangesCapsAtTheClock pins the clock cap:
// the next runs take the time of a run whose clock ran ahead, yet their bands end by their own
func TestCompactStatusChangesCapsAtTheClock(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// A 5-minute period ending 5 days ago, younger than the week rule's age
	insertHistory(tdb.Database, "a", []compactionInsert{
		{now - 40*day, cmdlib.StatusOnline},
		{now - 5*day, cmdlib.StatusOffline},
		{now - 5*day + 300, cmdlib.StatusOnline},
		{now - day, cmdlib.StatusOffline},
		{now - 3600, cmdlib.StatusOnline},
	})
	// A day's step three days ahead: the week band ends 26 days back
	if _, deleted := mustCompact(t, tdb.Database, testRules, day, now+3*day); deleted != 0 {
		t.Errorf("the run ahead deleted %d rows, want 0", deleted)
	}
	// Back on time, the week band ends at the rule's age by this clock, not by the stored time
	ends, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now)
	if ends[0] != now-7*day || deleted != 0 {
		t.Errorf("the run on time got to %d deleting %d, want %d deleting 0", ends[0], deleted, now-7*day)
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// TestCompactStatusChangesLogsPerformance pins the performance_log row a run leaves:
// its duration, the deleted count, the ends, the lag and a delete time per rule,
// the time after the vacuum, the vacuum's chunks and time, and the chunks left queued
func TestCompactStatusChangesLogsPerformance(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", shortHistory)
	// A 5-day step: the 30-day rule's band, [160d, 165d), passes a chunk end by a threshold
	// and stops 5 days short of its age; the week rule's, [170d, 175d), stops short of the period
	ctx := context.Background()
	c, err := run(ctx, tdb.Database, 5*day, testRules, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	ends := c.ends()
	queued, err := tdb.QueuedStatusChangesVacuums(ctx)
	if err != nil || queued == 0 {
		t.Fatalf("%d queued with %v, want some", queued, err)
	}

	var kind, durationMs int
	var data []byte
	rows := 0
	tdb.MustQuery(
		"select kind, duration_ms, data from performance_log",
		nil,
		db.ScanTo{&kind, &durationMs, &data},
		func() { rows++ })
	if rows != 1 || kind != int(db.PerformanceLogCompaction) || durationMs < 0 {
		t.Fatalf("%d rows, kind = %d, duration_ms = %d, want 1 row of kind %d",
			rows, kind, durationMs, db.PerformanceLogCompaction)
	}
	var logged struct {
		Deleted        int   `json:"deleted"`
		Ends           []int `json:"ends"`
		LagS           []int `json:"lag_s"`
		CompactionMs   *int  `json:"compaction_ms"`
		DeleteMs       []int `json:"delete_ms"`
		VacuumedChunks int   `json:"vacuumed_chunks"`
		QueuedChunks   *int  `json:"queued_chunks"`
		VacuumMs       *int  `json:"vacuum_ms"`
	}
	if err := json.Unmarshal(data, &logged); err != nil {
		t.Fatal(err)
	}
	if logged.Deleted != 0 || !reflect.DeepEqual(logged.Ends, ends) || len(logged.DeleteMs) != len(testRules) {
		t.Errorf("logged %+v, want 0 deleted, ends %v and %d delete times", logged, ends, len(testRules))
	}
	if want := []int{18 * day, 5 * day, 0}; !reflect.DeepEqual(logged.LagS, want) {
		t.Errorf("logged lag_s = %v, want %v", logged.LagS, want)
	}
	if logged.QueuedChunks == nil || *logged.QueuedChunks != queued {
		t.Errorf("logged queued_chunks = %v, want %d", logged.QueuedChunks, queued)
	}
	if logged.CompactionMs == nil || *logged.CompactionMs < 0 {
		t.Errorf("logged compaction_ms = %v, want a time", logged.CompactionMs)
	}
	// The queue was empty when the run began, so there is no vacuum to time
	if logged.VacuumedChunks != 0 || logged.VacuumMs != nil {
		t.Errorf("logged vacuumed_chunks = %d, vacuum_ms = %v, want 0 and none", logged.VacuumedChunks, logged.VacuumMs)
	}
}

// TestRunVacuumsOneChunk pins a run's vacuum: the one chunk at the front of the queue
func TestRunVacuumsOneChunk(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// Rows in chunks 18, 20, 24, 27 and 28
	insertHistory(tdb.Database, "a", []compactionInsert{
		{130 * day, cmdlib.StatusOnline},
		{145 * day, cmdlib.StatusOffline},
		{172 * day, cmdlib.StatusOnline},
		{190 * day, cmdlib.StatusOffline},
		{199 * day, cmdlib.StatusOnline},
	})
	ctx := context.Background()
	// The bands catch up, queueing chunks 18, 20 and 24 behind an empty queue
	if _, err := run(ctx, tdb.Database, 400*day, testRules, 0, now); err != nil {
		t.Fatal(err)
	}
	want := map[int]int{18: 0, 20: 0, 24: 0, 27: 0, 28: 0}
	if got := chunkVacuums(tdb.Database); !reflect.DeepEqual(got, want) {
		t.Errorf("vacuums after the first run = %v, want %v", got, want)
	}
	// A period later no band finishes a chunk, and the run vacuums the front chunk alone
	if _, err := run(ctx, tdb.Database, 400*day, testRules, 0, now+30); err != nil {
		t.Fatal(err)
	}
	want = map[int]int{18: 1, 20: 0, 24: 0, 27: 0, 28: 0}
	if got := chunkVacuums(tdb.Database); !reflect.DeepEqual(got, want) {
		t.Errorf("vacuums after the second run = %v, want %v", got, want)
	}
	if n, err := tdb.QueuedStatusChangesVacuums(ctx); n != 2 || err != nil {
		t.Errorf("%d queued with %v, want 2", n, err)
	}
	if !tdb.MustBool("select data ? 'vacuum_ms' from performance_log where kind = 4 order by timestamp desc limit 1") {
		t.Error("the run that vacuumed a chunk logged no vacuum_ms")
	}
}

// TestRunLogsAHeldChunksWait pins a run whose front chunk another session holds:
// it vacuums nothing, so its wait shows in duration_ms, not in vacuum_ms
func TestRunLogsAHeldChunksWait(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	insertHistory(tdb.Database, "a", []compactionInsert{{140 * day, cmdlib.StatusOnline}})
	ctx := context.Background()
	tx, err := tdb.Begin()
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := tdb.StatusChangesChunks(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tdb.QueueStatusChangesVacuums(ctx, tx, []db.StatusChangesVacuum{{Chunk: chunks[0].Name}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	holder := db.NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = holder.Close() }()
	hold, err := holder.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, "lock table "+chunks[0].Name+" in share update exclusive mode"); err != nil {
		t.Fatal(err)
	}
	// Fails rather than hangs should the lock timeout go
	tdb.MustExec("set statement_timeout = '10s'")
	if _, err := run(ctx, tdb.Database, 400*day, testRules, 0, now); err != nil {
		t.Fatal(err)
	}
	var durationMs, vacuumed int
	var vacuumMs *int
	tdb.MustQuery(`
		select duration_ms, (data ->> 'vacuumed_chunks')::int, (data ->> 'vacuum_ms')::int
		from performance_log
		where kind = 4`,
		nil,
		db.ScanTo{&durationMs, &vacuumed, &vacuumMs},
		func() {})
	if durationMs < 4000 || vacuumed != 0 || vacuumMs != nil {
		t.Errorf("logged duration_ms = %d, vacuumed_chunks = %d, vacuum_ms = %v, want the lock timeout's wait, 0 and none",
			durationMs, vacuumed, vacuumMs)
	}
}

// TestRunRewritesARewritingRulesChunks pins the rewrite:
// a chunk the rewriting 60-day rule finished gets a new file,
// and one the 30-day rule finished keeps its own
func TestRunRewritesARewritingRulesChunks(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// Rows in chunks 18 and 20
	insertHistory(tdb.Database, "a", []compactionInsert{
		{130 * day, cmdlib.StatusOnline},
		{145 * day, cmdlib.StatusOffline},
	})
	files := func() map[int]int {
		nodes := map[int]int{}
		var chunk, node int
		tdb.MustQuery(`
			select range_start_integer / $1, pg_relation_filenode(format('%I.%I', chunk_schema, chunk_name)::regclass)::bigint
			from timescaledb_information.chunks
			where hypertable_name = 'status_changes'`,
			db.QueryParams{7 * day},
			db.ScanTo{&chunk, &node},
			func() { nodes[chunk] = node })
		return nodes
	}
	before := files()
	// The 60-day band, [130d, 140d), finishes chunk 18, and the 30-day one, [140d, 170d), chunk 20
	_, _ = mustCompact(t, tdb.Database, testRules, 400*day, now)
	after := files()
	if after[18] == before[18] || after[20] != before[20] {
		t.Errorf("files %v before the run, %v after, want chunk 18's alone replaced", before, after)
	}
}

func TestLags(t *testing.T) {
	const now = 200 * day
	tests := []struct {
		name string
		ends []int
		want []int
	}{
		{name: "caught up", ends: []int{now - 7*day, now - 30*day, now - 60*day}, want: []int{0, 0, 0}},
		{name: "lagging", ends: []int{now - 7*day, now - 35*day, now - 100*day}, want: []int{0, 5 * day, 40 * day}},
		{name: "past its age", ends: []int{now - 7*day, now - 30*day, now - 40*day}, want: []int{0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make([]step, len(tt.ends))
			for i, end := range tt.ends {
				steps[i] = step{rule: testRules[i], end: end}
			}
			if got := (compaction{steps: steps}).lags(now); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("lags = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompactStatusChangesEmpty(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	defer tdb.terminate()
	// With no oldest row, the walk starts at now
	ends, deleted := mustCompact(t, tdb.Database, testRules, 400*day, now)
	if ends[0] != now || deleted != 0 {
		t.Errorf("got to %d deleting %d, want %d deleting none", ends[0], deleted, now)
	}
	if got := coverageEnd(tdb.Database, testRules[0]); got != now {
		t.Errorf("end = %d, want %d", got, now)
	}
}
