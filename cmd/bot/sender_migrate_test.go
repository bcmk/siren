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
// carrying target, then succeeds, so the sender resends to the new id.
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

func receiveResult(t *testing.T, ch chan msgSendResult) msgSendResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a send result")
		return msgSendResult{}
	}
}

// TestSenderResendsOnMigrate calls deliver with a migrate error
// and checks it reports the new id and resends there, not to the old one.
func TestSenderResendsOnMigrate(t *testing.T) {
	const oldID, newID = int64(-100), int64(-1001234)
	w := &worker{
		cfg:         &botconfig.Config{},
		bots:        map[string]*bot.Bot{"ep": nil},
		sendResults: make(chan msgSendResult, 16),
		cooledChats: make(chan int64, 16),
	}

	// We drive deliver directly with the chat preset,
	// exercising the migrate and resend path without a database.
	msg := &migrateThenOK{id: oldID, target: int(newID)}
	go w.deliver(&queuedMessage{
		chatID:   oldID,
		endpoint: "ep",
		message:  msg,
		priority: db.PriorityHigh,
		kind:     db.MessagePacket,
	})

	// First result: the bounce off the old id, carrying the new id.
	if r := receiveResult(t, w.sendResults); r.result != messageMigrate ||
		r.chatID != oldID || r.migrateToChatID != newID {
		t.Fatalf("bounce = {result %d, chat %d, migrateTo %d}, want {%d, %d, %d}",
			r.result, r.chatID, r.migrateToChatID, messageMigrate, oldID, newID)
	}
	// Second result: the resend, delivered to the new id.
	if r := receiveResult(t, w.sendResults); r.result != messageSent || r.chatID != newID {
		t.Fatalf("resend = {result %d, chat %d}, want {messageSent, %d}", r.result, r.chatID, newID)
	}
	if got := msg.chatID(); got != newID {
		t.Errorf("message not retargeted: chatID = %d, want %d", got, newID)
	}
}

// TestDeliverReleasesOriginalIDOnMigrate checks that after a resend
// to the new supergroup id, deliver frees exactly the id trySend cooled,
// the original one.
// The new id is deliberately never cooled, so releasing it would be stray.
func TestDeliverReleasesOriginalIDOnMigrate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const oldID, newID = int64(-100), int64(-1001234)
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledChats: make(chan int64, 16),
		}
		go w.deliver(&queuedMessage{
			chatID:   oldID,
			endpoint: "ep",
			message:  &migrateThenOK{id: oldID, target: int(newID)},
			priority: db.PriorityHigh,
			kind:     db.MessagePacket,
		})
		// Drain the bounce and the resend so deliver reaches its release.
		receiveResult(t, w.sendResults)
		receiveResult(t, w.sendResults)

		// The migrated chat is a supergroup, released after groupCooldown;
		// synctest's fake clock makes that wait instant.
		if id := <-w.cooledChats; id != oldID {
			t.Errorf("released id = %d, want %d", id, oldID)
		}
		// An extra release would follow at once; settle all goroutines
		// and confirm none arrives.
		synctest.Wait()
		select {
		case id := <-w.cooledChats:
			t.Errorf("unexpected extra release of %d", id)
		default:
		}
	})
}
