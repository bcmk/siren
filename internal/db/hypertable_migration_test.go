package db

import (
	"testing"
	"time"
)

// rewindHypertableMigrations returns status_changes to its pre-hypertable shape,
// so the prebuild can be replayed against seeded rows.
func rewindHypertableMigrations(db *testDB) {
	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			timestamp integer not null,
			streamer_id integer not null,
			status smallint not null,
			prev_status smallint not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_hypertable%'`)
}

// TestPrebuiltStatusChangesHypertable replays the hypertable prebuild,
// with rows arriving before it, a delete after it, and rows arriving after it.
func TestPrebuiltStatusChangesHypertable(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	rewindHypertableMigrations(db)

	// Three rows across two closed weeks, and two at the head week that stay for the tail.
	now := int(time.Now().Unix())
	w3 := now/604800*604800 - 3*604800
	w2 := w3 + 604800
	head := now/604800*604800 + 100
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status) values
		($1, 1, 2, 0), ($2, 1, 1, 2), ($3, 2, 2, 0), ($4, 1, 2, 1)`,
		w3+100, w3+200, w2+100, head)

	// One call takes the whole prebuild group, through the copy and its vacuum.
	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	if db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected no prebuild migrations left")
	}

	if n := db.MustInt(`select count(*) from status_changes_new`); n != 3 {
		t.Fatalf("prebuild should have copied the 3 closed-week rows, got %d", n)
	}

	// A rename fold deletes a copied row; the copy keeps it as orphaned history.
	db.MustExec(`delete from status_changes where timestamp = $1`, w3+200)

	// The bot keeps writing.
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status) values ($1, 2, 1, 2)`,
		head+1)

	// Through ApplyMigrations, as startup does, so its ordering and transaction are covered.
	db.ApplyMigrations()

	// The 3 copied rows, the deleted one included, plus the 2 tail rows.
	if n := db.MustInt(`select count(*) from status_changes`); n != 5 {
		t.Errorf("expected 5 rows after the cutover, got %d", n)
	}
	if n := db.MustInt(`select count(*) from status_changes where timestamp = $1`, w3+200); n != 1 {
		t.Error("the row deleted after the copy should survive as orphaned history")
	}
	if n := db.MustInt(
		`select count(*) from status_changes where timestamp in ($1, $2)`, head, head+1); n != 2 {
		t.Error("the tail rows should have been appended at the cutover")
	}
	if n := db.MustInt(
		`select count(*) from status_changes where timestamp = $1 and status = 2 and prev_status = 0`,
		w3+100); n != 1 {
		t.Error("a copied row lost its values")
	}

	if n := db.MustInt(`
		select count(*) from timescaledb_information.hypertables
		where hypertable_name = 'status_changes'`); n != 1 {
		t.Error("status_changes should be a hypertable after the cutover")
	}
	if n := db.MustInt(`
		select count(*) from timescaledb_information.chunks
		where hypertable_name = 'status_changes'`); n != 3 {
		t.Errorf("expected the rows to span 3 week chunks, got %d", n)
	}

	if n := db.MustInt(`select count(*) from pg_class where relname = 'status_changes_conversion'`); n != 0 {
		t.Error("the cutover should have dropped status_changes_conversion")
	}
	if n := db.MustInt(`
		select count(*) from pg_indexes
		where indexname = 'ix_status_changes_streamer_id_timestamp'`); n != 1 {
		t.Error("covering index missing after the cutover")
	}
	if n := db.MustInt(`
		select count(*) from pg_indexes
		where indexname = 'ix_status_changes_timestamp' and indexdef like '%USING btree%'`); n != 1 {
		t.Error("timestamp btree missing after the cutover")
	}
}

// A row timestamped below the boundary after its week was copied would vanish in the swap,
// so the cutover refuses it.
func TestHypertableCutoverRefusesRowBelowBoundary(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	rewindHypertableMigrations(db)

	now := int(time.Now().Unix())
	w3 := now/604800*604800 - 3*604800
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status) values ($1, 1, 2, 0)`,
		w3+100)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}

	// A clock stepping back past the margin lands a row below an already copied week.
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status) values ($1, 1, 1, 2)`,
		w3+200)

	defer func() {
		if recover() == nil {
			t.Error("the cutover should have refused the row below the boundary")
		}
	}()
	db.ApplyMigrations()
}
