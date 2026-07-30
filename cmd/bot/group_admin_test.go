package main

import (
	"errors"
	"testing"

	"github.com/go-telegram/bot/models"
)

// TestGroupAdminGate drives a gated command through processIncomingCommand:
// in a group only an admin may run it, every failure fails closed and answers admins_only,
// and no branch that can answer without Telegram asks Telegram.
func TestGroupAdminGate(t *testing.T) {
	t.Parallel()
	admin := &models.ChatMember{Type: models.ChatMemberTypeAdministrator}
	tests := []struct {
		name         string
		chatID       int64
		from         *models.User
		senderChat   *models.Chat
		shuttingDown bool
		member       *models.ChatMember
		memberErr    error
		wantApplied  bool
		wantLookup   bool
	}{
		{
			name:   "group admin passes",
			chatID: -10, from: &models.User{ID: 99},
			member: admin, wantApplied: true, wantLookup: true,
		},
		{
			name:   "group owner passes",
			chatID: -10, from: &models.User{ID: 99},
			member: &models.ChatMember{Type: models.ChatMemberTypeOwner}, wantApplied: true, wantLookup: true,
		},
		{
			name:   "group member is refused",
			chatID: -10, from: &models.User{ID: 99},
			member: &models.ChatMember{Type: models.ChatMemberTypeMember}, wantLookup: true,
		},
		{
			// No member at all: a regression that consults the lookup here fails both ways.
			name:   "private chat skips the lookup",
			chatID: 10, from: &models.User{ID: 99},
			wantApplied: true,
		},
		{
			name:   "anonymous admin passes without a lookup",
			chatID: -10, from: &models.User{ID: 99},
			senderChat: &models.Chat{ID: -10}, wantApplied: true,
		},
		{
			name:   "channel identity is refused without a lookup",
			chatID: -10, from: &models.User{ID: 99},
			senderChat: &models.Chat{ID: -77}, member: admin,
		},
		{
			name:   "missing sender is refused without a lookup",
			chatID: -10, member: admin,
		},
		{
			name:   "shutdown fails closed without a lookup",
			chatID: -10, from: &models.User{ID: 99},
			shuttingDown: true, member: admin,
		},
		{
			name:   "lookup error fails closed",
			chatID: -10, from: &models.User{ID: 99},
			memberErr: errors.New("timeout"), wantLookup: true,
		},
		{
			name:   "null member fails closed rather than crashing",
			chatID: -10, from: &models.User{ID: 99},
			wantLookup: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.shuttingDown.Store(tc.shuttingDown)
			looked := false
			w.chatMember = func(string, int64, int64) (*models.ChatMember, error) {
				looked = true
				return tc.member, tc.memberErr
			}
			m := testMessage(w, tc.chatID, "disable_images", 0)
			snd := sender{from: tc.from, senderChat: tc.senderChat}
			w.processIncomingCommand(m, snd, "disable_images", "", false)
			showImages := w.db.MustBool("select show_images from users where chat_id = $1", tc.chatID)
			if applied := !showImages; applied != tc.wantApplied {
				t.Errorf("command applied = %v, want %v", applied, tc.wantApplied)
			}
			if looked != tc.wantLookup {
				t.Errorf("member lookup made = %v, want %v", looked, tc.wantLookup)
			}
			// The pass and the refusal are both spoken: OK from the handler, admins_only from the gate.
			want := "AdminsOnly"
			if tc.wantApplied {
				want = "OK"
			}
			if n := w.sendQueue.Len(); n != 1 {
				t.Fatalf("queued replies = %d, want 1", n)
			}
			q := w.sendQueue.pop()
			q.message.render("")
			if got := q.message.(*messageParams).Text; got != want {
				t.Errorf("reply = %q, want %q", got, want)
			}
		})
	}
}
