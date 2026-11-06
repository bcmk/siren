//go:build migrations

package db

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A prebuild runs against the schema the migrations before it leave,
// so it must refuse while any of them is pending.
// Recording it against the wrong schema would be permanent:
// the bot skips what schema_migrations already holds.
func TestPrebuildRefusesAheadOfPendingMigration(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	db.MustExec(`delete from schema_migrations where name like '%status_changes_prev_status%'`)
	db.MustExec(`delete from schema_migrations where name = 'profile_photos'`)

	defer func() {
		if recover() == nil {
			t.Error("prebuilding with an earlier migration pending should have failed")
		}
	}()
	db.ApplyNextPrebuildMigrations()
}

// A prebuild group behind an ordinary migration could only run at startup,
// so the prebuild refuses before doing any of the work.
func TestPrebuildRefusesGroupBehindOrdinaryMigration(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// Pending: the prev_status group, its ordinary cutover, then the hypertable group.
	db.MustExec(`
		delete from schema_migrations
		where name like '%status_changes_prev_status%' or name like '%status_changes_hypertable%'`)

	func() {
		defer func() {
			if r := recover(); !strings.Contains(fmt.Sprint(r), "waits behind an ordinary migration") {
				t.Errorf("expected the group behind the cutover to be refused, got %v", r)
			}
		}()
		db.ApplyNextPrebuildMigrations()
	}()

	if n := db.MustInt(`select count(*) from schema_migrations where name like '%status_changes_prev_status%'`); n != 0 {
		t.Errorf("the refusal should come before any work, but %d migrations were recorded", n)
	}
}

// Startup gives an empty database every prebuild through the schema snapshot.
func TestPrebuildLeavesAnEmptyDatabaseToStartup(t *testing.T) {
	t.Parallel()
	d, _ := newEmptyDatabase(t)

	if d.ApplyNextPrebuildMigrations() {
		t.Error("a prebuild ran on an empty database")
	}
	if !d.noTablesInPublic() {
		t.Error("the prebuild left tables behind")
	}
}

// A chunked prebuild also runs at startup, where pausing would only add downtime.
func TestChunkPausesOnlyForPrebuild(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

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

	other := NewDatabase(holder.connStr, false, 5)
	defer func() { _ = other.Close() }()

	// Replay-safe, and pending as a running prebuild is.
	holder.MustExec(`delete from schema_migrations where name = 'message_log_chat_id_indexes_rework_2'`)
	holder.MustExec("select pg_advisory_lock($1)", migrationLock)

	done := make(chan struct{})
	go func() {
		other.ApplyMigrations()
		close(done)
	}()

	waitForLockWait(t, holder.Database, done)

	holder.MustExec("select pg_advisory_unlock($1)", migrationLock)
	// A run that never proceeds hits go test's own timeout, before Close takes its connection.
	<-done
}

// Both runners can find the database empty, so the check and the schema snapshot need the lock.
func TestMigrationRunsOnAnEmptyDatabaseAreSerialized(t *testing.T) {
	t.Parallel()
	d, connStr := newEmptyDatabase(t)

	const runners = 2
	start := make(chan struct{})
	// A panic in a goroutine would end the test binary
	failures := make(chan any, runners)
	for range runners {
		runner := NewDatabase(connStr, false, 5)
		go func() {
			defer func() {
				failure := recover()
				_ = runner.Close()
				failures <- failure
			}()
			<-start
			runner.ApplyMigrations()
		}()
	}
	close(start)
	for range runners {
		if failure := <-failures; failure != nil {
			t.Errorf("a run failed: %v", failure)
		}
	}

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if n := d.MustInt("select count(*) from schema_migrations"); n != len(migrations) {
		t.Errorf("%d migrations recorded, want %d", n, len(migrations))
	}
}

// A bot with nothing to apply starts without waiting for a prebuild holding the lock.
func TestMigrationRunSkipsLockWhenNothingPending(t *testing.T) {
	t.Parallel()
	holder := newTestDB(t)

	other := NewDatabase(holder.connStr, false, 5)
	defer func() { _ = other.Close() }()

	holder.MustExec("select pg_advisory_lock($1)", migrationLock)

	done := make(chan struct{})
	go func() {
		other.ApplyMigrations()
		close(done)
	}()

	waited := false
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		waited = true
	}
	// The run must end before the deferred Close takes its connection.
	holder.MustExec("select pg_advisory_unlock($1)", migrationLock)
	<-done
	if waited {
		t.Fatal("a run with nothing to apply should not have waited for the lock")
	}
}

func TestSchemaSnapshotStartsAnEmptyDatabase(t *testing.T) {
	t.Parallel()
	d, _ := newEmptyDatabase(t)
	const sessionSettings = `
		select coalesce(string_agg(name || ' = ' || setting, ', ' order by name), '')
		from pg_settings
		where source = 'session'`
	before := d.MustStrings(sessionSettings)[0]

	d.ApplyMigrations()

	// The schema snapshot records every migration at time 0
	if n := d.MustInt(`select count(*) from schema_migrations where applied_at <> 0`); n != 0 {
		t.Errorf("%d migrations ran instead of the schema snapshot", n)
	}
	// A set in the schema snapshot would outlive it on this connection
	if after := d.MustStrings(sessionSettings)[0]; after != before {
		t.Errorf("session settings changed from %q to %q", before, after)
	}
}

func TestMigratedDatabaseSkipsTheSchemaSnapshot(t *testing.T) {
	t.Parallel()
	d := newTestDB(t)
	// This migration, a vacuum analyze, replays safely
	d.MustExec(`delete from schema_migrations where name = 'message_log_chat_id_indexes_rework_2'`)

	d.ApplyMigrations()

	if n := d.MustInt(`select count(*) from schema_migrations where applied_at <> 0`); n != 1 {
		t.Errorf("%d migrations ran, want the 1 missing", n)
	}
}

func TestNoTablesInPublic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare string
		want    bool
	}{
		{name: "no tables", want: true},
		{name: "a table", prepare: "create table bystander (id integer)"},
		{
			name:    "a table in another schema",
			prepare: "create schema other; create table other.bystander (id integer)",
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, _ := newEmptyDatabase(t)
			if tt.prepare != "" {
				d.MustExec(tt.prepare)
			}
			if got := d.noTablesInPublic(); got != tt.want {
				t.Errorf("noTablesInPublic() = %v, want %v", got, tt.want)
			}
		})
	}
}
