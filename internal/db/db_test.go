package db

import (
	"os"
	"testing"

	"github.com/bcmk/siren/v5/internal/pgtest"
	"github.com/bcmk/siren/v5/lib/cmdlib"
)

var clones = pgtest.NewClones(func(connStr string) error {
	d := NewDatabase(connStr, false, 5)
	d.ApplyMigrations()
	return d.Close()
})

func TestMain(m *testing.M) {
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	code := m.Run()
	clones.Terminate()
	os.Exit(code)
}

type testDB struct {
	*Database
	connStr   string
	terminate func()
}

// newTestDB gives the test a database of its own
func newTestDB(t *testing.T) *testDB {
	t.Helper()
	connStr, err := clones.Clone()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDatabase(connStr, false, 5)
	return &testDB{
		Database:  &d,
		connStr:   connStr,
		terminate: func() { _ = d.Close() },
	}
}
