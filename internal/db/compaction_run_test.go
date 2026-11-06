package db

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// statusChangeRow is a status_changes row without its streamer
type statusChangeRow struct {
	timestamp  int
	status     cmdlib.StatusKind
	prevStatus cmdlib.StatusKind
}

// streamerChanges returns a streamer's rows in timestamp order
func streamerChanges(d *Database, streamerID int) []statusChangeRow {
	var rows []statusChangeRow
	var row statusChangeRow
	d.MustQuery(`
		select timestamp, status, prev_status
		from status_changes
		where streamer_id = $1
		order by timestamp`,
		QueryParams{streamerID},
		ScanTo{&row.timestamp, &row.status, &row.prevStatus},
		func() { rows = append(rows, row) })
	return rows
}

// checkStatusChangesConsistent holds the status change invariants:
// prev_status names the row before, and streamers holds the last two rows.
func checkStatusChangesConsistent(t *testing.T, d *Database, nickname string) {
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

// compact makes one run with no vacuum delay, under testRules
func compact(ctx context.Context, d *Database, stepSeconds int, now int) ([]int, int64, error) {
	c, err := d.RunCompaction(ctx, stepSeconds, testRules, 0, now)
	if err != nil {
		return nil, 0, err
	}
	return c.ends(), c.deleted, nil
}

// mustCompact makes a run as compact does and fails the test if the run errs
func mustCompact(t *testing.T, d *Database, stepSeconds int, now int) ([]int, int64) {
	t.Helper()
	ends, deleted, err := compact(context.Background(), d, stepSeconds, now)
	if err != nil {
		t.Fatal(err)
	}
	return ends, deleted
}

// testRules are the compactor's default rules
var testRules = []ShortOfflineRule{
	{After: 7 * day, ShorterThan: 15 * 60},
	{After: 30 * day, ShorterThan: 30 * 60},
	{After: 60 * day, ShorterThan: 60 * 60, Rewrite: true},
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
			insertHistory(tdb.Database, "a", tt.inserts)

			ends, deleted := mustCompact(t, tdb.Database, 400*day, now)

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
			insertHistory(tdb.Database, "a", shortHistory)

			ends, deleted := mustCompact(t, tdb.Database, tt.step, now)
			if want := now - 30*day + tt.step; ends[0] != want || deleted != 0 {
				t.Errorf("first run got to %d deleting %d, want %d deleting 0", ends[0], deleted, want)
			}
			if _, deleted := mustCompact(t, tdb.Database, 400*day, now); deleted != 2 {
				t.Errorf("second run deleted %d rows, want 2", deleted)
			}
			checkStatusChangesConsistent(t, tdb.Database, "a")
		})
	}
}

// TestCompactStatusChangesSkipsABusyLock pins the lock:
// a run finding status_changes_lock held skips, and the next one goes through
func TestCompactStatusChangesSkipsABusyLock(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	insertHistory(tdb.Database, "a", shortHistory)

	holder := NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = holder.Close() }()
	ctx := context.Background()
	hold, err := holder.begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if err := holder.lockStatusChanges(ctx, hold); err != nil {
		t.Fatal(err)
	}
	// Fails rather than hangs should the lock wait
	tdb.MustExec("set statement_timeout = '10s'")

	ends, deleted, err := compact(ctx, tdb.Database, 400*day, now)
	if !errors.Is(err, ErrLockBusy) || ends != nil || deleted != 0 {
		t.Errorf("the run under the held lock got %v, %d, %v, want nothing and ErrLockBusy", ends, deleted, err)
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, deleted, err := compact(ctx, tdb.Database, 400*day, now); err != nil || deleted != 2 {
		t.Errorf("the run after the lock deleted %d rows with %v, want 2", deleted, err)
	}
	if stored := tdb.MustInt("select count(*) from status_changes_lock"); stored != len(testRules) {
		t.Errorf("%d coverages stored, want %d", stored, len(testRules))
	}
	checkStatusChangesConsistent(t, tdb.Database, "a")
}

// chunkVacuums returns each chunk's vacuum count by its number on the test database's 7-day grid
func chunkVacuums(d *Database) map[int]int {
	counts := map[int]int{}
	var chunk, vacuums int
	d.MustQuery(`
		select c.range_start_integer / $1, s.vacuum_count
		from timescaledb_information.chunks c
		join pg_stat_all_tables s on s.schemaname = c.chunk_schema and s.relname = c.chunk_name
		where c.hypertable_name = 'status_changes'`,
		QueryParams{7 * day},
		ScanTo{&chunk, &vacuums},
		func() { counts[chunk] = vacuums })
	return counts
}

// TestCompactStatusChangesVacuumsFinishedChunks pins the vacuums:
// a rule's band queues a chunk once past its end by the threshold plus the delay, and once only
func TestCompactStatusChangesVacuumsFinishedChunks(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	// Rows in chunks 18, 20, 24, 27 and 28
	insertHistory(tdb.Database, "a", []compactionInsert{
		{130 * day, cmdlib.StatusOnline},
		{145 * day, cmdlib.StatusOffline},
		{172 * day, cmdlib.StatusOnline},
		{190 * day, cmdlib.StatusOffline},
		{199 * day, cmdlib.StatusOnline},
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
		if _, err := tdb.RunCompaction(ctx, 400*day, testRules, r.delay, r.now); err != nil {
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

// TestCompactStatusChangesStopsWithTheContext pins the shutdown path:
// a cancel in the middle of a run stops it at once with its error, and the history stays as it was
func TestCompactStatusChangesStopsWithTheContext(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	insertHistory(tdb.Database, "a", shortHistory)
	a := streamerID(t, tdb.Database, "a")
	// pgx closes a connection whose statement it cancels, so the run gets one of its own
	runner := NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = runner.Close() }()
	holder := NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = holder.Close() }()

	ctx := context.Background()
	hold, err := holder.begin()
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
		_, _, err := compact(runCtx, &runner, 400*day, now)
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

// TestCompactStatusChangesLogsPerformance pins the performance_log row a run leaves:
// its duration, the deleted count, the ends, the lag and a delete time per rule,
// the time after the vacuum, the vacuum's chunks and time, and the chunks left queued
func TestCompactStatusChangesLogsPerformance(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	insertHistory(tdb.Database, "a", shortHistory)
	// A 5-day step: the 30-day rule's band, [160d, 165d), passes a chunk end by a threshold
	// and stops 5 days short of its age; the week rule's, [170d, 175d), stops short of the period
	ctx := context.Background()
	c, err := tdb.RunCompaction(ctx, 5*day, testRules, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	ends := c.ends()
	queued, err := tdb.queuedStatusChangesVacuums(ctx)
	if err != nil || queued == 0 {
		t.Fatalf("%d queued with %v, want some", queued, err)
	}

	var kind, durationMs int
	var data []byte
	rows := 0
	tdb.MustQuery(
		"select kind, duration_ms, data from performance_log",
		nil,
		ScanTo{&kind, &durationMs, &data},
		func() { rows++ })
	if rows != 1 || kind != int(PerformanceLogCompaction) || durationMs < 0 {
		t.Fatalf("%d rows, kind = %d, duration_ms = %d, want 1 row of kind %d",
			rows, kind, durationMs, PerformanceLogCompaction)
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

// TestRunCompactionVacuumsOneChunk pins a run's vacuum: the one chunk at the front of the queue
func TestRunCompactionVacuumsOneChunk(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
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
	if _, err := tdb.RunCompaction(ctx, 400*day, testRules, 0, now); err != nil {
		t.Fatal(err)
	}
	want := map[int]int{18: 0, 20: 0, 24: 0, 27: 0, 28: 0}
	if got := chunkVacuums(tdb.Database); !reflect.DeepEqual(got, want) {
		t.Errorf("vacuums after the first run = %v, want %v", got, want)
	}
	// A period later no band finishes a chunk, and the run vacuums the front chunk alone
	if _, err := tdb.RunCompaction(ctx, 400*day, testRules, 0, now+30); err != nil {
		t.Fatal(err)
	}
	want = map[int]int{18: 1, 20: 0, 24: 0, 27: 0, 28: 0}
	if got := chunkVacuums(tdb.Database); !reflect.DeepEqual(got, want) {
		t.Errorf("vacuums after the second run = %v, want %v", got, want)
	}
	if n, err := tdb.queuedStatusChangesVacuums(ctx); n != 2 || err != nil {
		t.Errorf("%d queued with %v, want 2", n, err)
	}
	if !tdb.MustBool("select data ? 'vacuum_ms' from performance_log where kind = 4 order by timestamp desc limit 1") {
		t.Error("the run that vacuumed a chunk logged no vacuum_ms")
	}
}

// TestRunCompactionLogsAHeldChunksWait pins a run whose front chunk another session holds:
// it vacuums nothing, so its wait shows in duration_ms, not in vacuum_ms
func TestRunCompactionLogsAHeldChunksWait(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	insertHistory(tdb.Database, "a", []compactionInsert{{140 * day, cmdlib.StatusOnline}})
	queueChunk(t, tdb.Database, 140*day, false)
	ctx := context.Background()
	holder := NewDatabase(tdb.connStr, false, 5)
	defer func() { _ = holder.Close() }()
	hold, err := holder.begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	lock := "lock table " + chunkName(tdb.Database, 140*day) + " in share update exclusive mode"
	if _, err := hold.Exec(ctx, lock); err != nil {
		t.Fatal(err)
	}
	// Fails rather than hangs should the lock timeout be dropped
	tdb.MustExec("set statement_timeout = '10s'")
	tdb.vacuumLockTimeout = 50 * time.Millisecond
	if _, err := tdb.RunCompaction(ctx, 400*day, testRules, 0, now); err != nil {
		t.Fatal(err)
	}
	var durationMs, vacuumed int
	var vacuumMs *int
	tdb.MustQuery(`
		select duration_ms, (data ->> 'vacuumed_chunks')::int, (data ->> 'vacuum_ms')::int
		from performance_log
		where kind = 4`,
		nil,
		ScanTo{&durationMs, &vacuumed, &vacuumMs},
		func() {})
	if durationMs < int(tdb.vacuumLockTimeout.Milliseconds()) || vacuumed != 0 || vacuumMs != nil {
		t.Errorf("logged duration_ms = %d, vacuumed_chunks = %d, vacuum_ms = %v, want the lock timeout's wait, 0 and none",
			durationMs, vacuumed, vacuumMs)
	}
}

// TestRunCompactionRewritesARewritingRulesChunks pins the rewrite:
// a chunk the rewriting 60-day rule finished gets a new file,
// and one the 30-day rule finished keeps its own
func TestRunCompactionRewritesARewritingRulesChunks(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
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
			QueryParams{7 * day},
			ScanTo{&chunk, &node},
			func() { nodes[chunk] = node })
		return nodes
	}
	before := files()
	// The 60-day band, [130d, 140d), finishes chunk 18, and the 30-day one, [140d, 170d), chunk 20.
	// The first run queues them, and each next run takes one.
	for _, at := range []int{now, now + 30, now + 60} {
		mustCompact(t, tdb.Database, 400*day, at)
	}
	after := files()
	if after[18] == before[18] || after[20] != before[20] {
		t.Errorf("files %v before the run, %v after, want chunk 18's alone replaced", before, after)
	}
}

func TestCompactStatusChangesEmpty(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	tdb := newTestDB(t)
	// With no oldest row, the walk starts at now
	ends, deleted := mustCompact(t, tdb.Database, 400*day, now)
	if ends[0] != now || deleted != 0 {
		t.Errorf("got to %d deleting %d, want %d deleting none", ends[0], deleted, now)
	}
	got := tdb.MustInt("select end_timestamp from status_changes_lock where after = $1", testRules[0].After)
	if got != now {
		t.Errorf("end = %d, want %d", got, now)
	}
}

func TestFormatLags(t *testing.T) {
	tests := []struct {
		name string
		lags []int
		want string
	}{
		{name: "caught up", lags: []int{0, 0, 0}, want: "[0 0 0]"},
		{name: "lagging", lags: []int{0, 5 * day, 59*day + 3*hour + 59*60}, want: "[0 5d0h0m0s 59d3h59m0s]"},
		{name: "under an hour", lags: []int{900, 59, 0}, want: "[0d0h15m0s 0d0h0m59s 0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatLags(tt.lags); got != tt.want {
				t.Errorf("formatLags = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCompactStatusChangesKeepsStreamersApart pins the joins on streamer_id:
// one streamer's rows never decide another's period
func TestCompactStatusChangesKeepsStreamersApart(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	const (
		offline = cmdlib.StatusOffline
		online  = cmdlib.StatusOnline
	)
	tests := []struct {
		name        string
		histories   map[string][]compactionInsert
		wantDeleted int64
		wantRows    map[string]int
	}{
		{
			name: "a period in a quiet streamer's last two rows stays, whatever a busy streamer holds",
			histories: map[string][]compactionInsert{
				"a": {{now - 40*day, online}, {now - 20*day, offline}, {now - 20*day + 300, online}},
				"b": {{now - 40*day, online}, {now - day, offline}, {now - 3600, online}},
			},
			wantRows: map[string]int{"a": 3, "b": 3},
		},
		{
			name: "streamers changing at the same timestamps: only the short offline period goes",
			histories: map[string][]compactionInsert{
				// A 5-minute offline period ending 20 days ago
				"a": {
					{now - 40*day, online},
					{now - 20*day, offline},
					{now - 20*day + 300, online},
					{now - day, offline},
					{now - 3600, online},
				},
				// Online for those 5 minutes, after 20 offline
				"b": {
					{now - 40*day, online},
					{now - 20*day - 1200, offline},
					{now - 20*day, online},
					{now - 20*day + 300, offline},
					{now - day, online},
					{now - 3600, offline},
				},
			},
			wantDeleted: 2,
			wantRows:    map[string]int{"a": 3, "b": 6},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tdb := newTestDB(t)
			for nickname, history := range tt.histories {
				insertHistory(tdb.Database, nickname, history)
			}
			if _, deleted := mustCompact(t, tdb.Database, 400*day, now); deleted != tt.wantDeleted {
				t.Errorf("deleted %d rows, want %d", deleted, tt.wantDeleted)
			}
			for nickname, want := range tt.wantRows {
				if rows := streamerChanges(tdb.Database, streamerID(t, tdb.Database, nickname)); len(rows) != want {
					t.Errorf("%s has %d rows, want %d", nickname, len(rows), want)
				}
				checkStatusChangesConsistent(t, tdb.Database, nickname)
			}
		})
	}
}

// TestCompactStatusChangesFollowsTheClock pins a clock that jumps between runs
func TestCompactStatusChangesFollowsTheClock(t *testing.T) {
	t.Parallel()
	const now = 200 * day
	type compactionRun struct {
		step        int
		now         int
		wantDeleted int64
		// wantEnds is left out where the run's ends do not matter
		wantEnds []int
	}
	tests := []struct {
		name    string
		history []compactionInsert
		runs    []compactionRun
	}{
		{
			name:    "a run timed before the last one takes its time, so its bands do not walk again",
			history: shortHistory,
			runs: []compactionRun{
				{step: 400 * day, now: now, wantDeleted: 2, wantEnds: []int{now - 7*day, now - 30*day, now - 40*day}},
				{step: 5 * day, now: now - 1, wantEnds: []int{now - 7*day, now - 30*day, now - 40*day}},
			},
		},
		{
			name: "a run after one ahead of the clock takes that time, yet its bands end by its own clock",
			// A 5-minute period ending 5 days ago, younger than the week rule's age
			history: []compactionInsert{
				{now - 40*day, cmdlib.StatusOnline},
				{now - 5*day, cmdlib.StatusOffline},
				{now - 5*day + 300, cmdlib.StatusOnline},
				{now - day, cmdlib.StatusOffline},
				{now - 3600, cmdlib.StatusOnline},
			},
			runs: []compactionRun{
				{step: day, now: now + 3*day},
				{step: 400 * day, now: now, wantEnds: []int{now - 7*day, now - 30*day, now - 40*day}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tdb := newTestDB(t)
			insertHistory(tdb.Database, "a", tt.history)
			for i, run := range tt.runs {
				ends, deleted := mustCompact(t, tdb.Database, run.step, run.now)
				if deleted != run.wantDeleted {
					t.Errorf("run %d deleted %d rows, want %d", i, deleted, run.wantDeleted)
				}
				if run.wantEnds != nil && !reflect.DeepEqual(ends, run.wantEnds) {
					t.Errorf("run %d got to %v, want %v", i, ends, run.wantEnds)
				}
			}
			checkStatusChangesConsistent(t, tdb.Database, "a")
		})
	}
}
