package db

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// rewindUniqueTimestampMigrations gives status_changes back its non-unique covering index,
// so the unique prebuild can be replayed against seeded rows.
func rewindUniqueTimestampMigrations(db *testDB) {
	db.MustExec(`drop index ix_status_changes_streamer_id_timestamp`)
	db.MustExec(`
		create index ix_status_changes_streamer_id_timestamp
		on status_changes (streamer_id, timestamp)
		include (status, prev_status)`)
	db.MustExec(`delete from schema_migrations where name like 'status_changes_unique_%'`)
}

// dbNow is the database's clock, which the copy's boundary and margin follow
func dbNow(db *testDB) int {
	return db.MustInt(`select extract(epoch from now())::integer`)
}

// seededRow is a status_changes row with its timestamp as an offset from now
type seededRow struct{ offset, streamerID, status, prevStatus int }

func insertSeededRows(db *testDB, now int, rows []seededRow) {
	for _, r := range rows {
		db.MustExec(`
			insert into status_changes (timestamp, streamer_id, status, prev_status) values ($1, $2, $3, $4)`,
			now+r.offset, r.streamerID, r.status, r.prevStatus)
	}
}

// seededRows reads the table back in timestamp order
func seededRows(db *testDB, now int) []seededRow {
	var rows []seededRow
	var r seededRow
	db.MustQuery(`
		select timestamp - $1, streamer_id, status, prev_status
		from status_changes
		order by timestamp, streamer_id`,
		QueryParams{now},
		ScanTo{&r.offset, &r.streamerID, &r.status, &r.prevStatus},
		func() { rows = append(rows, r) })
	return rows
}

// panicText runs f and returns what it panicked with, or "" if it did not
func panicText(f func()) (text string) {
	defer func() {
		if r := recover(); r != nil {
			text = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}

// TestPrebuiltStatusChangesUnique replays the unique prebuild,
// with rows in closed weeks, rows inside the margin, and rows arriving after it.
func TestPrebuiltStatusChangesUnique(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	rewindUniqueTimestampMigrations(db)

	now := dbNow(db)
	seeded := []seededRow{
		{-2 * 604800, 1, 2, 0},
		{-2*604800 + 100, 1, 1, 2},
		{-604800, 2, 2, 0},
		{-7200, 3, 2, 0},
		// Inside the margin: left for the tail
		{-60, 3, 1, 2},
	}
	insertSeededRows(db, now, seeded)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	if n := db.MustInt(`select count(*) from status_changes_new`); n != 4 {
		t.Fatalf("prebuild should have copied the 4 rows behind the margin, got %d", n)
	}

	// The bot keeps writing.
	tail := seededRow{-30, 2, 1, 2}
	insertSeededRows(db, now, []seededRow{tail})

	db.ApplyMigrations()

	if got, want := seededRows(db, now), append(seeded, tail); !reflect.DeepEqual(got, want) {
		t.Errorf("rows after the cutover = %v, want %v", got, want)
	}
	if !db.MustBool(`
		select indisunique from pg_index
		where indexrelid = 'ix_status_changes_streamer_id_timestamp'::regclass`) {
		t.Error("the covering index should be unique after the cutover")
	}
	if n := db.MustInt(`
		select count(*) from pg_indexes
		where tablename = 'status_changes' and indexname = 'ix_status_changes_timestamp'`); n != 1 {
		t.Error("timestamp index missing after the cutover")
	}
	if n := db.MustInt(`
		select count(*) from timescaledb_information.hypertables
		where hypertable_name = 'status_changes'`); n != 1 {
		t.Error("status_changes should still be a hypertable")
	}
	if n := db.MustInt(`
		select count(*) from pg_constraint
		where conrelid = 'status_changes'::regclass and conname like 'chk_status_changes_%'`); n != 3 {
		t.Errorf("expected the 3 check constraints, got %d", n)
	}
	if n := db.MustInt(`
		select count(*) from pg_constraint
		where conrelid = 'status_changes'::regclass and conname in (
			'status_changes_timestamp_not_null',
			'status_changes_streamer_id_not_null',
			'status_changes_status_not_null',
			'status_changes_prev_status_not_null')`); n != 4 {
		t.Errorf("expected the 4 not-null constraints under their final names, got %d", n)
	}
	if n := db.MustInt(`
		select count(*) from pg_class
		where relname in ('status_changes_new', 'status_changes_conversion')`); n != 0 {
		t.Error("the cutover should have dropped the copy's leftovers")
	}
	text := panicText(func() { insertSeededRows(db, now, []seededRow{{-2 * 604800, 1, 1, 2}}) })
	if !strings.Contains(text, "duplicate key") {
		t.Errorf("a second change of a streamer at the same timestamp should be rejected, got %q", text)
	}
}

// TestUniqueTimestampConversionRefuses covers what stops the conversion, and at which step.
func TestUniqueTimestampConversionRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// rows before the prebuild
		before []seededRow
		// rows written between the prebuild and the cutover
		after []seededRow
		// whether the prebuild fails rather than the cutover
		failsInPrebuild bool
		wantError       string
	}{
		{
			name:            "tie in copied history",
			before:          []seededRow{{-604800, 1, 2, 0}, {-604800, 1, 1, 2}},
			failsInPrebuild: true,
			wantError:       "could not create unique index",
		},
		{
			name: "tie in the tail",
			// A copied row, so the boundary sits at the margin even in a week's first hour
			before:    []seededRow{{-604800, 2, 2, 0}},
			after:     []seededRow{{-30, 1, 2, 0}, {-30, 1, 1, 2}},
			wantError: "duplicate key",
		},
		{
			// A clock stepping back past the margin lands a row below the boundary
			name:      "row below the boundary",
			before:    []seededRow{{-7200, 1, 2, 0}},
			after:     []seededRow{{-3700, 1, 1, 2}},
			wantError: "the day below the boundary holds 2 rows but only 1 were copied",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			defer db.terminate()

			rewindUniqueTimestampMigrations(db)
			now := dbNow(db)
			insertSeededRows(db, now, tc.before)

			text := panicText(func() { db.ApplyNextPrebuildMigrations() })
			if (text != "") != tc.failsInPrebuild {
				t.Fatalf("prebuild failure = %q, want failure = %v", text, tc.failsInPrebuild)
			}
			if !tc.failsInPrebuild {
				insertSeededRows(db, now, tc.after)
				text = panicText(db.ApplyMigrations)
			}
			if !strings.Contains(text, tc.wantError) {
				t.Errorf("got failure %q, want one containing %q", text, tc.wantError)
			}
		})
	}
}
