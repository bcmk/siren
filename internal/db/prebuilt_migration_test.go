package db

import (
	"context"
	"testing"
	"time"
)

// TestPrebuiltStatusChanges replays the early prebuild, with rows arriving before the migration.
func TestPrebuiltStatusChanges(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Rewind to before the conversion, so the prebuild can be replayed.
	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			status integer not null,
			timestamp integer not null,
			streamer_id integer not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)

	// Two streamers, alternating statuses.
	db.MustExec(`
		insert into status_changes (status, timestamp, streamer_id) values
		(2, 100, 1), (1, 100, 2),
		(1, 200, 1), (2, 200, 2),
		(2, 300, 1), (1, 300, 2)`)

	// One streamer per chunk, so the two convert in separate chunks.
	db.MustExec(`set siren.chunk_streamers = '1'`)

	// One call takes the whole prebuild group, both the conversion and its vacuum.
	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	if db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected no prebuild migrations left")
	}

	// The boundary is the last row present, so the prebuild took every one of them.
	if n := db.MustInt(`select count(*) from status_changes_new`); n != 6 {
		t.Fatalf("prebuild should have converted all 6 seeded rows, got %d", n)
	}

	// The bot keeps writing.
	db.MustExec(`
		insert into status_changes (status, timestamp, streamer_id) values
		(1, 400, 1), (2, 400, 2), (2, 500, 1)`)

	// Through ApplyMigrations, as startup does, so its ordering and transaction are covered.
	db.ApplyMigrations()

	if n := db.MustInt(`select count(*) from status_changes`); n != 9 {
		t.Errorf("expected all 9 rows after the migration, got %d", n)
	}

	// The values themselves,
	// not just the count: streamer 1 alternates 2,1,2,1,2 and streamer 2 alternates 1,2,1,2,
	// and the last of each pair crosses from the prebuilt rows into the tail,
	// which is where the boundary chaining is used.
	var got []string
	var row string
	db.MustQuery(`
		select timestamp || ' ' || streamer_id || ' ' || status || ' ' || prev_status
		from status_changes
		order by streamer_id, timestamp`,
		nil, ScanTo{&row}, func() { got = append(got, row) })

	want := []string{
		"100 1 2 0", "200 1 1 2", "300 1 2 1", "400 1 1 2", "500 1 2 1",
		"100 2 1 0", "200 2 2 1", "300 2 1 2", "400 2 2 1",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d rows, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: expected %q, got %q", i, want[i], got[i])
		}
	}
	if n := db.MustInt(`select count(*) from status_changes where timestamp in (400, 500)`); n != 3 {
		t.Errorf("expected the 3 rows written after the prebuild, got %d", n)
	}
	if n := db.MustInt(`select count(*) from pg_class where relname = 'status_changes_conversion'`); n != 0 {
		t.Error("the migration should have dropped status_changes_conversion")
	}

	// The covering index must exist under its final name, renamed from the prebuild's.
	if n := db.MustInt(`
		select count(*) from pg_class
		where relname = 'ix_status_changes_streamer_id_timestamp'`); n != 1 {
		t.Error("covering index missing after the migration")
	}
}

// A prebuild runs against the schema the migrations before it leave,
// so it must refuse while any of them is pending.
// Recording it against the wrong schema would be permanent:
// the bot skips what schema_migrations already holds.
func TestPrebuildRefusesAheadOfPendingMigration(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)
	db.MustExec(`delete from schema_migrations where name = 'profile_photos'`)

	defer func() {
		if recover() == nil {
			t.Error("prebuilding with an earlier migration pending should have failed")
		}
	}()
	db.ApplyNextPrebuildMigrations()
}

// The boundary splits by ctid while prev_status follows timestamp,
// so a row written after the prebuild
// but timestamped before its tail would chain off the wrong predecessor.
// The migration must refuse rather than write it.
func TestPrebuildRefusesBackdatedTail(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			status integer not null,
			timestamp integer not null,
			streamer_id integer not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)

	// The second row is a forward clock glitch, so the prebuild ends on it.
	db.MustExec(`
		insert into status_changes (status, timestamp, streamer_id) values
		(2, 1000, 2), (1, 2000000000, 2)`)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}

	// A later write carrying an ordinary timestamp now sorts before the prebuilt tail.
	db.MustExec(`insert into status_changes (status, timestamp, streamer_id) values (1, 1200, 2)`)

	defer func() {
		if recover() == nil {
			t.Error("the migration should have refused the backdated row")
		}
	}()
	db.ApplyMigrations()
}

// A chunked prebuild also runs at startup, where pausing would only add downtime.
func TestChunkPausesOnlyForPrebuild(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	pause := func() string {
		return db.MustStrings(
			`select coalesce(current_setting('siren.chunk_pause', true), 'unset')`)[0]
	}

	if got := pause(); got != "unset" {
		t.Errorf("a fresh connection should not pause, got %q", got)
	}

	db.Throttle()
	if got := pause(); got != "2" {
		t.Errorf("a prebuild should pause, got %q", got)
	}
}

// A prebuild records itself only when it finishes,
// so for as long as it runs it still reads as pending.
// A second runner must wait rather than convert the same rows again.
func TestMigrationRunsAreSerialized(t *testing.T) {
	t.Parallel()
	holder := newTestDB(t)
	defer holder.terminate()

	var name string
	holder.MustQuery("select current_database()", nil, ScanTo{&name}, func() {})
	other := NewDatabase(connStrFor(name), false, 5)
	defer func() { _ = other.Close() }()

	holder.MustExec("select pg_advisory_lock($1)", migrationLock)

	done := make(chan struct{})
	go func() {
		other.ApplyMigrations()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("the second run should have waited for the lock")
	case <-time.After(time.Second):
	}

	holder.MustExec("select pg_advisory_unlock($1)", migrationLock)

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the second run should have proceeded once the lock was released")
	}
}

// The prebuild counts its own inserts,
// so the cutover checks that count plus the tail accounts for every source row
// and refuses when a row has gone missing under it.
func TestConversionRefusesWhenCountsDisagree(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			status integer not null,
			timestamp integer not null,
			streamer_id integer not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)
	db.MustExec(`
		insert into status_changes (status, timestamp, streamer_id) values
		(2, 100, 1), (1, 200, 1), (2, 300, 1)`)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}

	// A converted row leaves the source after the prebuild counted it,
	// so the count and the table no longer agree.
	db.MustExec(`delete from status_changes where timestamp = 200`)

	defer func() {
		if recover() == nil {
			t.Error("the migration should have refused the unaccounted count")
		}
	}()
	db.ApplyMigrations()
}

// Two check rounds can land in one second, so a flapping streamer gets rows sharing a timestamp.
// Ordered arbitrarily, a flap chains onto itself and prev_status equals status.
func TestConversionOrdersTiedTimestampsByWriteOrder(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			status integer not null,
			timestamp integer not null,
			streamer_id integer not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)
	db.MustExec(`
		insert into status_changes (status, timestamp, streamer_id) values
		(2, 100, 1), (1, 100, 1), (2, 100, 1)`)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	db.ApplyMigrations()

	bad := db.MustInt(`
		select count(*) from (
			select prev_status,
			coalesce(lag(status) over (partition by streamer_id order by timestamp, ctid), 0::smallint) as expected
			from status_changes) t
		where prev_status is distinct from expected`)
	if bad != 0 {
		t.Errorf("%d rows chain off the wrong tied row", bad)
	}
	if n := db.MustInt(`select count(*) from status_changes where status = prev_status`); n != 0 {
		t.Errorf("%d rows repeat their own status", n)
	}
}

// A bot still serving would have the rounds it commits after the tail's snapshot
// dropped with the source table, so the migration waits for them first.
func TestMigrationWaitsForWritersToFinish(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			status integer not null,
			timestamp integer not null,
			streamer_id integer not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)
	db.MustExec(`
		insert into status_changes (status, timestamp, streamer_id) values
		(2, 100, 1), (1, 200, 1)`)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}

	var name string
	db.MustQuery("select current_database()", nil, ScanTo{&name}, func() {})
	writer := NewDatabase(connStrFor(name), false, 5)
	defer func() { _ = writer.Close() }()

	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "select 1 from status_changes limit 1"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		db.ApplyMigrations()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("the migration should have waited for the open round")
	case <-time.After(time.Second):
	}

	checkErr(tx.Rollback(context.Background()))

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the migration should have proceeded once the round ended")
	}
}

// Space freed by old updates lets a newer row sit in an older block,
// so a heap in block order hands a streamer its rows out of sequence.
// The conversion reads through the (streamer_id, timestamp) index,
// so a shuffled heap converts the same,
// and chunking by streamer covers every one across the gaps between their ids.
func TestConversionIgnoresHeapOrder(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			status integer not null,
			timestamp integer not null,
			streamer_id integer not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)

	// Streamer ids 1 and 3, rows shuffled so neither time nor streamer follows the heap.
	db.MustExec(`
		insert into status_changes (status, timestamp, streamer_id) values
		(2, 300, 1), (1, 100, 3),
		(1, 200, 1), (1, 300, 3),
		(2, 100, 1), (2, 200, 3)`)

	// One streamer per chunk, so the loop spans [0,1) empty, [1,2), [2,3) empty, and [3,4).
	db.MustExec(`set siren.chunk_streamers = '1'`)
	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}

	// With chunk size 1 and max id 3 the loop ends at 4;
	// the 50000 default would end at 50000,
	// so this fails if the setting were ignored and everything converted in one chunk.
	if n := db.MustInt(`select next_streamer_id from status_changes_conversion`); n != 4 {
		t.Fatalf("expected the streamer loop to stop at 4, got %d", n)
	}

	db.MustExec(`insert into status_changes (status, timestamp, streamer_id) values (1, 400, 1), (2, 400, 3)`)
	db.ApplyMigrations()

	got := db.MustStrings(`
		select timestamp || ' ' || streamer_id || ' ' || status || ' ' || prev_status
		from status_changes
		order by streamer_id, timestamp`)
	want := []string{
		"100 1 2 0", "200 1 1 2", "300 1 2 1", "400 1 1 2",
		"100 3 1 0", "200 3 2 1", "300 3 1 2", "400 3 2 1",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d rows, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

// A streamer's first row defaults prev_status to 0, and its status may also be 0 (unknown),
// so the invariant check exempts status 0 and the conversion must accept such a row.
func TestConversionAllowsUnknownFirstStatus(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			status integer not null,
			timestamp integer not null,
			streamer_id integer not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)
	db.MustExec(`
		insert into status_changes (status, timestamp, streamer_id) values
		(0, 100, 1), (1, 200, 1)`)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	db.ApplyMigrations()

	if got := db.MustStrings(`
		select status || ' ' || prev_status from status_changes
		order by timestamp`); got[0] != "0 0" {
		t.Errorf("an unknown first row should convert to 0 0, got %q", got[0])
	}
}

// The invariant check rejects a change that lands on the status it came from,
// which is how a mis-ordered same-second flap would surface.
func TestNewTableRejectsSelfPrevStatus(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.ApplyMigrations()

	defer func() {
		if recover() == nil {
			t.Error("a row with status equal to prev_status should be rejected")
		}
	}()
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status)
		values (100, 1, 1, 1)`)
}
