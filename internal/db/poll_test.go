package db

import (
	"reflect"
	"testing"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

func insertStreamer(t *testing.T, d *Database, nickname string) {
	t.Helper()
	d.UpsertUnconfirmedStatusChanges(
		[]StatusChange{{Nickname: nickname, Status: cmdlib.StatusOffline}},
		1)
}

func TestSetPollAndStreamersToPoll(t *testing.T) {
	db := newTestDB(t)
	defer db.terminate()

	insertStreamer(t, db.Database, "alice")

	if got := db.StreamersToPoll(); len(got) != 0 {
		t.Errorf("expected empty initial set, got %v", got)
	}

	// Toggle on/off on an existing streamer.
	if !db.SetPoll("alice", true) {
		t.Error("SetPoll(alice, true) should succeed")
	}
	if got := db.StreamersToPoll(); !reflect.DeepEqual(got, []string{"alice"}) {
		t.Errorf("after enabling alice, got %v", got)
	}

	if !db.SetPoll("alice", false) {
		t.Error("SetPoll(alice, false) should report success when row exists")
	}
	if got := db.StreamersToPoll(); len(got) != 0 {
		t.Errorf("after disabling alice, got %v", got)
	}

	// Set on for a missing streamer creates rows in both tables.
	if !db.SetPoll("ghost", true) {
		t.Error("SetPoll(ghost, true) should succeed")
	}
	if got := db.StreamersToPoll(); !reflect.DeepEqual(got, []string{"ghost"}) {
		t.Errorf("after upsert ghost, got %v", got)
	}
	if db.MaybeStreamer("ghost") == nil {
		t.Error("ghost streamer row not created")
	}
	if !db.MustBool("select exists(select 1 from nicknames where nickname = $1)", "ghost") {
		t.Error("ghost nicknames row not created")
	}

	// Off for an unknown streamer is reported back so admins notice typos.
	if db.SetPoll("phantom", false) {
		t.Error("SetPoll(phantom, false) should return false (row absent)")
	}
	if db.MaybeStreamer("phantom") != nil {
		t.Error("SetPoll(off) should not create a row")
	}
}
