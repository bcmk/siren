package db

import (
	"reflect"
	"testing"
)

func TestSetPollAndStreamersToPoll(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	insertStreamerRow(db.Database, Streamer{Nickname: "alice"})

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
	if n := db.MustInt("select count(*) from nicknames where nickname = $1", "alice"); n != 1 {
		t.Errorf("%d nicknames rows for a known streamer, want 1", n)
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
