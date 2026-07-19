package main

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bcmk/siren/v3/internal/botconfig"
	"github.com/bcmk/siren/v3/internal/db"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// migrateThenOK fails the first send with a group-to-supergroup migrate error
// carrying target, then succeeds, so a test can watch the redirect.
type migrateThenOK struct {
	id     int64
	target int
	sends  int
}

func (m *migrateThenOK) chatID() int64      { return m.id }
func (m *migrateThenOK) setChatID(id int64) { m.id = id }

func (m *migrateThenOK) send(_ context.Context, _ *bot.Bot) (*models.Message, error) {
	m.sends++
	if m.sends == 1 {
		return nil, &bot.MigrateError{MigrateToChatID: m.target}
	}
	return nil, nil
}

// migrateToSelf always fails with a migrate error pointing at the same chat
// id it was addressed — a degenerate migrate that must not re-queue.
type migrateToSelf struct{ id int64 }

func (m *migrateToSelf) chatID() int64      { return m.id }
func (m *migrateToSelf) setChatID(id int64) { m.id = id }
func (m *migrateToSelf) send(_ context.Context, _ *bot.Bot) (*models.Message, error) {
	return nil, &bot.MigrateError{MigrateToChatID: int(m.id)}
}

// TestDeliverDropsMigrateToSelf checks the loop guard: a migrate whose target
// is the id just addressed does not re-queue, so it cannot loop in place.
func TestDeliverDropsMigrateToSelf(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledUsers: make(chan db.UserID, 16),
		}
		go w.deliver(&queuedMessage{
			userID:   1,
			endpoint: "ep",
			message:  &migrateToSelf{id: -100},
			priority: db.PriorityHigh,
			tag:      unprompted(db.MessagePacket),
		})

		r := <-w.sendResults
		if r.result != messageMigrate {
			t.Fatalf("result = %d, want messageMigrate", r.result)
		}
		if r.resend != nil {
			t.Errorf("a migrate to the same chat id re-queued; want no resend")
		}
	})
}

// TestDeliverReportsMigrateForRequeue checks deliver's half of a migration:
// a single result carrying the new chat id and the message to re-queue,
// and exactly one user release.
func TestDeliverReportsMigrateForRequeue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const oldID, newID = int64(-100), int64(-1001234)
		const userID = db.UserID(42)
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledUsers: make(chan db.UserID, 16),
		}
		q := &queuedMessage{
			userID:   userID,
			endpoint: "ep",
			message:  &migrateThenOK{id: oldID, target: int(newID)},
			priority: db.PriorityHigh,
			tag:      unprompted(db.MessagePacket),
		}
		go w.deliver(q)

		r := <-w.sendResults
		if r.result != messageMigrate || r.migrateToChatID != newID || r.resend != q {
			t.Fatalf("result = {result %d, migrateTo %d, resend set %t}, want a re-queueing migrate to %d",
				r.result, r.migrateToChatID, r.resend != nil, newID)
		}
		// The chat is a group, released after groupCooldown — exactly once.
		if id := <-w.cooledUsers; id != userID {
			t.Errorf("released id = %d, want %d", id, userID)
		}
		synctest.Wait()
		select {
		case r := <-w.sendResults:
			t.Errorf("unexpected extra result %d", r.result)
		case id := <-w.cooledUsers:
			t.Errorf("unexpected extra release of %d", id)
		default:
		}
	})
}

// TestMigrateAppliesBeforeResendDispatch guards the bookkeeping order
// in completeSendResult: a stalled main loop can release the user's cooling
// before the migrate result is processed,
// so the resend may dispatch the moment the slot frees,
// and the chat row must already point at the new id by then.
// The test simulates the post-stall state — the user is not cooling —
// so the resend dispatches synchronously inside completeSendResult.
func TestMigrateAppliesBeforeResendDispatch(t *testing.T) {
	t.Parallel()
	const oldID, newID = int64(-100), int64(-1001234)
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.commonCooling = false

	userID, _ := w.db.AddUser(oldID, 3, 0, "group")
	msg := &okMessage{}
	w.completeSendResult(msgSendResult{
		result:          messageMigrate,
		endpoint:        "test",
		chatID:          oldID,
		userID:          userID,
		migrateToChatID: newID,
		tag:             unprompted(db.MessagePacket),
		resend: &queuedMessage{
			userID:   userID,
			endpoint: "test",
			message:  msg,
			priority: db.PriorityHigh,
			tag:      unprompted(db.MessagePacket),
		},
	})

	// trySend resolved and set the chat id synchronously at dispatch.
	if got := msg.chatID(); got != newID {
		t.Errorf("resend dispatched to chat %d, want the migrated %d", got, newID)
	}
}

// TestSenderRedeliversAcrossMigrate drives the real pipeline
// through a migration: the bounced message is re-queued, the chat data moves,
// and after the cooldown the re-dispatch resolves the new chat id
// and delivers there.
func TestSenderRedeliversAcrossMigrate(t *testing.T) {
	t.Parallel()
	const oldID, newID = int64(-100), int64(-1001234)
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.commonCooling = false
	// Skip the member-count network lookup in the sent bookkeeping.
	w.shuttingDown.Store(true)

	userID, _ := w.db.AddUser(oldID, 3, 0, "group")
	msg := &migrateThenOK{target: int(newID)}
	w.enqueueMessage(db.PriorityHigh, "test", msg, unprompted(db.MessagePacket), userID, 0)

	// One timer for the whole wait:
	// a per-iteration time.After would re-arm on every event,
	// letting an endless re-queue loop spin forever.
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case r := <-w.sendResults:
			w.completeSendResult(r)
			if r.result == messageMigrate {
				continue
			}
			if r.result != messageSent || r.chatID != newID {
				t.Fatalf("delivery = {result %d, chat %d}, want {messageSent, %d}",
					r.result, r.chatID, newID)
			}
			if got := msg.chatID(); got != newID {
				t.Errorf("message not retargeted: chatID = %d, want %d", got, newID)
			}
			return
		case u := <-w.cooledUsers:
			w.onUserCooled(u)
		case <-deadline.C:
			t.Fatal("timed out waiting for the redelivery")
		}
	}
}
