package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/bcmk/siren/v6/internal/botconfig"
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

// TestDisabledCommandAnswersUnknown drives a command the bot does not offer
// through processIncomingCommand:
// it answers unknown command to everyone, without asking Telegram about the member.
func TestDisabledCommandAnswersUnknown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		chatID  int64
		command string
		// configure edits the test config, leaving the command off
		configure func(*botconfig.Config)
	}{
		{
			name:      "week with month on",
			chatID:    10,
			command:   "week",
			configure: func(c *botconfig.Config) { c.EnableMonth = true },
		},
		{
			name:      "month with week on",
			chatID:    10,
			command:   "month",
			configure: func(c *botconfig.Config) { c.EnableWeek = true },
		},
		{name: "buy_subs", chatID: 10, command: "buy_subs"},
		{name: "affiliate from a group member", chatID: -10, command: "affiliate"},
		{name: "reset_affiliate from a group member", chatID: -10, command: "reset_affiliate"},
		{name: "subject in a private chat", chatID: 10, command: "enable_subject"},
		{name: "subject from a group member", chatID: -10, command: "enable_subject"},
		{
			name:      "offline notifications turned off",
			chatID:    10,
			command:   "enable_offline_notifications",
			configure: func(c *botconfig.Config) { c.OfflineNotifications = false },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			if tc.configure != nil {
				// Every worker shares &testConfig, so copy before changing it.
				cfg := *w.cfg
				tc.configure(&cfg)
				w.cfg = &cfg
			}
			looked := false
			w.chatMember = func(string, int64, int64) (*models.ChatMember, error) {
				looked = true
				return &models.ChatMember{Type: models.ChatMemberTypeMember}, nil
			}
			m := testMessage(w, tc.chatID, tc.command, 0)
			w.processIncomingCommand(m, sender{from: &models.User{ID: 99}}, tc.command, "", false)
			if looked {
				t.Error("a command the bot does not offer looked up the member")
			}
			if n := w.sendQueue.Len(); n != 1 {
				t.Fatalf("queued replies = %d, want 1", n)
			}
			q := w.sendQueue.pop()
			q.message.render("")
			if got := q.message.(*messageParams).Text; got != "UnknownCommand" {
				t.Errorf("reply = %q, want UnknownCommand", got)
			}
		})
	}
}

// TestRepliesNameNoDisabledCommand renders each reply with a command_enabled guard
// on a bot that offers no command the table may disable.
func TestRepliesNameNoDisabledCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		command string
		chatID  int64
	}{
		// A group, which alone is offered the affiliate commands
		{"settings", -10},
		{"want_more", 10},
		{"faq", 10},
	}
	// Kick and Twitch override guarded entries with their own
	sets := []struct{ site, lang string }{
		{"chaturbate", "en"},
		{"chaturbate", "ru"},
		{"kick", "en"},
		{"twitch", "en"},
	}
	for _, set := range sets {
		for _, tc := range tests {
			t.Run(set.site+" "+set.lang+" "+tc.command, func(t *testing.T) {
				t.Parallel()
				w := newTestWorker()
				defer w.terminate()
				w.createDatabase()
				w.initCache()
				w.initTimezones()
				cfg := *w.cfg
				cfg.OfflineNotifications = false
				w.cfg = &cfg
				w.loadTranslations(siteTranslations(set.site, set.lang))
				w.chatMember = func(string, int64, int64) (*models.ChatMember, error) {
					return &models.ChatMember{Type: models.ChatMemberTypeAdministrator}, nil
				}
				m := testMessage(w, tc.chatID, tc.command, 0)
				w.processIncomingCommand(m, sender{from: &models.User{ID: 99}}, tc.command, "", false)
				if w.sendQueue.Len() == 0 {
					t.Fatal("no reply queued")
				}
				for q := w.sendQueue.pop(); q != nil; q = w.sendQueue.pop() {
					q.message.render("@bot")
					assertNoDisabledCommand(t, w, q.message.(*messageParams).Text)
				}
			})
		}
	}
}

// assertNoDisabledCommand fails the test where text names a command the bot does not offer
func assertNoDisabledCommand(t *testing.T, w *testWorker, text string) {
	t.Helper()
	for command, spec := range knownCommands {
		if spec.enabled == nil {
			continue
		}
		if w.commandEnabled(command) {
			t.Fatalf("/%s is offered, so it cannot be checked", command)
		}
		if strings.Contains(text, "/"+command+"@bot") {
			t.Errorf("names /%s, which the bot does not offer:\n%s", command, text)
		}
	}
}
