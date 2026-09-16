package main

import (
	"testing"

	"github.com/bcmk/siren/v5/internal/botconfig"
)

// TestCheckerOnlyStoresAndSendsNothing pins two of the mode's guards:
// a confirmed change queues no notification and counts no report,
// and a message reaches no sender.
func TestCheckerOnlyStoresAndSendsNothing(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	// Its own copy: newTestWorker hands every worker the one shared testConfig.
	cfg := testConfig
	cfg.CheckerOnly = true
	w.cfg = &cfg

	userID, _ := w.db.AddUser(-1001234567890, 3, 0, "channel")
	storeStatusNotifications(w, userID, "a")
	if nots := w.db.NewNotifications(); len(nots) != 0 {
		t.Errorf("checker_only queued %d notifications, want none", len(nots))
	}
	user, _ := w.db.UserByID(userID)
	if user.Reports != 0 {
		t.Errorf("checker_only counted %d reports, want 0", user.Reports)
	}

	w.sendMaintenance("test", -1001234567890, false, "bot started")
	if w.nextOutgoing() != nil {
		t.Error("checker_only queued a message to send")
	}
}

// TestCheckerOnlyArmsNoDeliveryTimers pins the guard on the periodic work:
// the checker tick runs, and the two ticks that would touch the queue do not.
func TestCheckerOnlyArmsNoDeliveryTimers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		checkerOnly bool
		wantArmed   bool
	}{
		{name: "a normal run arms them", wantArmed: true},
		{name: "checker_only does not", checkerOnly: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &worker{cfg: &botconfig.Config{
				CheckerOnly:                     tt.checkerOnly,
				PeriodSeconds:                   1,
				SubsConfirmationPeriodSeconds:   1,
				NotificationsReadyPeriodSeconds: 1,
			}}
			timers := w.newStartupTimers()
			if timers.request == nil {
				t.Error("the checker tick is not armed")
			}
			if armed := timers.subsConfirm != nil; armed != tt.wantArmed {
				t.Errorf("the confirmation tick armed = %v, want %v", armed, tt.wantArmed)
			}
			if armed := timers.notificationSender != nil; armed != tt.wantArmed {
				t.Errorf("the notification fetch armed = %v, want %v", armed, tt.wantArmed)
			}
		})
	}
}
