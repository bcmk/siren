package db

import "testing"

// checkTID enforces single-goroutine access;
// SuspendGIDCheck lifts it for the startup handoff,
// where init runs on its own goroutine off the main loop.
func TestGIDCheckSuspend(t *testing.T) {
	d := Database{shouldCheckGID: true, mainGID: gid()}

	// Off the owning goroutine, the check rejects by default.
	if !checkTIDPanics(&d) {
		t.Error("checkTID accepted another goroutine, want rejected")
	}
	// Suspended, it lets the startup goroutine through.
	d.SuspendGIDCheck()
	if checkTIDPanics(&d) {
		t.Error("checkTID rejected another goroutine while suspended")
	}
	// Resumed, it enforces again.
	d.ResumeGIDCheck()
	if !checkTIDPanics(&d) {
		t.Error("checkTID stayed suspended after resume")
	}
}

// checkTIDPanics runs the gid check on a fresh goroutine
// and reports whether it panicked.
func checkTIDPanics(d *Database) (panicked bool) {
	done := make(chan bool, 1)
	go func() {
		defer func() { done <- recover() != nil }()
		d.checkTID()
	}()
	return <-done
}
