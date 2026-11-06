package db

import (
	"flag"
	"os"
	"testing"
	"time"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

var server = newTestServer()

func TestMain(m *testing.M) {
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	// Most tests wait on PostgreSQL rather than a CPU, so more fit than the default of one per CPU.
	// An explicit -parallel still wins.
	checkErr(flag.Set("test.parallel", "16"))
	code := m.Run()
	server.terminate()
	os.Exit(code)
}

type testDB struct {
	*Database
	connStr string
}

// waitForLockWait returns once a session on d's database waits for a lock,
// and fails the test if done closes first, as the work did not wait
func waitForLockWait(t *testing.T, d *Database, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for d.MustInt(`
		select count(*)
		from pg_stat_activity
		where datname = current_database() and wait_event_type = 'Lock'`) == 0 {
		select {
		case <-done:
			t.Fatal("the work finished without waiting for the lock")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing waited for the lock")
		}
		time.Sleep(time.Millisecond)
	}
}

// newTestDB gives the test a database of its own, for as long as the test runs
func newTestDB(t *testing.T) *testDB {
	t.Helper()
	connStr, drop, err := server.clone()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDatabase(connStr, false, 5)
	t.Cleanup(func() {
		_ = d.Close()
		if err := drop(); err != nil {
			t.Error(err)
		}
	})
	return &testDB{Database: &d, connStr: connStr}
}
