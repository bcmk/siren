package main

import (
	"os"
	"testing"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/internal/pgtest"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

var clones = pgtest.NewClones(func(connStr string) error {
	d := db.NewDatabase(connStr, false, 5)
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
	*db.Database
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
	d := db.NewDatabase(connStr, false, 5)
	return &testDB{
		Database:  &d,
		connStr:   connStr,
		terminate: func() { _ = d.Close() },
	}
}

func streamerID(t *testing.T, d *db.Database, nickname string) int {
	t.Helper()
	s := d.MaybeStreamer(nickname)
	if s == nil {
		t.Fatalf("streamer %s not found", nickname)
	}
	return s.ID
}
