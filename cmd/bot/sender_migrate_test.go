package main

import (
	"context"
	"testing"
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

// TestSenderResendsOnMigrate drives the sender loop through a migrate error
// and checks it reports the new id and resends there, not to the old one.
func TestSenderResendsOnMigrate(t *testing.T) {
	const oldID, newID = int64(-100), int64(-1001234)
	w := &worker{
		cfg:                &botconfig.Config{},
		bots:               map[string]*bot.Bot{"ep": nil},
		outgoingMsgCh:      make(chan outgoingPacket, 16),
		outgoingMsgResults: make(chan msgSendResult, 16),
	}
	go w.sender()

	msg := &migrateThenOK{id: oldID, target: int(newID)}
	w.enqueueMessage(db.PriorityHigh, "ep", msg, db.MessagePacket)

	// First result: the bounce off the old id, carrying the new id.
	if r := receiveResult(t, w.outgoingMsgResults); r.result != messageMigrate ||
		r.chatID != oldID || r.migrateToChatID != newID {
		t.Fatalf("bounce = {result %d, chat %d, migrateTo %d}, want {%d, %d, %d}",
			r.result, r.chatID, r.migrateToChatID, messageMigrate, oldID, newID)
	}
	// Second result: the resend, delivered to the new id.
	if r := receiveResult(t, w.outgoingMsgResults); r.result != messageSent || r.chatID != newID {
		t.Fatalf("resend = {result %d, chat %d}, want {messageSent, %d}", r.result, r.chatID, newID)
	}
	if got := msg.chatID(); got != newID {
		t.Errorf("message not retargeted: chatID = %d, want %d", got, newID)
	}
}
