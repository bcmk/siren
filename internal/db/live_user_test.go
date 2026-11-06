package db

import "testing"

// LiveUserID follows migrated_to, so send-result bookkeeping for a user
// tombstoned mid-flight lands on the live user it merged into.
func TestLiveUserID(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	d := tdb.Database

	const src, dst = int64(-100), int64(-100500)
	// First, so a chain not seeded by the given id returns this user
	otherID, _ := d.AddUser(-200, 5, 1000, "group")
	srcID, _ := d.AddUser(src, 5, 1000, "group")
	dstID, _ := d.AddUser(dst, 5, 1000, "supergroup")
	d.MigrateChat(src, dst) // tombstones srcID, migrated_to = dstID

	if got := d.LiveUserID(srcID); got != dstID {
		t.Errorf("LiveUserID(tombstone %d) = %d, want live %d", srcID, got, dstID)
	}
	if got := d.LiveUserID(dstID); got != dstID {
		t.Errorf("LiveUserID(live %d) = %d, want unchanged", dstID, got)
	}
	if got := d.LiveUserID(otherID); got != otherID {
		t.Errorf("LiveUserID(live %d) = %d, want unchanged", otherID, got)
	}
	const unknown = UserID(999999)
	if got := d.LiveUserID(unknown); got != unknown {
		t.Errorf("LiveUserID(unknown %d) = %d, want unchanged", unknown, got)
	}
}
