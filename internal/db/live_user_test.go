package db

import "testing"

// LiveUserID follows migrated_to, so send-result bookkeeping for a user
// tombstoned mid-flight lands on the live user it merged into.
func TestLiveUserID(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.terminate()
	d := tdb.Database

	const src, dst = int64(-100), int64(-100500)
	srcID, _ := d.AddUser(src, 5, 1000, "group")
	dstID, _ := d.AddUser(dst, 5, 1000, "supergroup")
	d.MigrateChat(src, dst) // tombstones srcID, migrated_to = dstID

	if got := d.LiveUserID(srcID); got != dstID {
		t.Errorf("LiveUserID(tombstone %d) = %d, want live %d", srcID, got, dstID)
	}
	if got := d.LiveUserID(dstID); got != dstID {
		t.Errorf("LiveUserID(live %d) = %d, want unchanged", dstID, got)
	}
}
