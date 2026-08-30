package db

import (
	"testing"
	"time"
)

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
