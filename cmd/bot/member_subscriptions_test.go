package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// TestMemberSubscriptionsGate pins what the setting opens and what it leaves shut:
// the subscription commands and the deep link that adds one, and nothing else.
func TestMemberSubscriptionsGate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		command   string
		arguments string
		setting   bool
		wantGated bool
	}{
		{name: "add is admin only by default", command: "add", wantGated: true},
		{name: "add opens to members", command: "add", setting: true},
		{name: "remove is admin only by default", command: "remove", wantGated: true},
		{name: "remove opens to members", command: "remove", setting: true},
		{
			name:    "a model deep link is admin only by default",
			command: "start", arguments: modelPayloadPrefix + "somemodel", wantGated: true,
		},
		{
			name:    "a model deep link opens to members",
			command: "start", arguments: modelPayloadPrefix + "somemodel", setting: true,
		},
		{name: "a bare start stays open", command: "start", setting: true},
		{
			// The payload names no model, so start answers nothing to add and the gate matches.
			name:    "a deep link naming no model stays open",
			command: "start", arguments: modelPayloadPrefix,
		},
		{name: "a wipe stays admin only", command: "remove_all", setting: true, wantGated: true},
		{name: "stop stays admin only", command: "stop", setting: true, wantGated: true},
		{name: "settings stays admin only", command: "settings", setting: true, wantGated: true},
		{
			name:    "the setting cannot open itself",
			command: "enable_member_subscriptions", setting: true, wantGated: true,
		},
		{
			name:    "the setting cannot open its own reversal",
			command: "disable_member_subscriptions", setting: true, wantGated: true,
		},
		{name: "an ungated command stays open", command: "list", setting: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			m := testMessage(w, -10, tc.command, 0)
			w.db.SetMemberSubscriptions(m.userID, tc.setting)
			if got := w.groupAdminOnly(m, tc.command, tc.arguments); got != tc.wantGated {
				t.Errorf("gated = %v, want %v", got, tc.wantGated)
			}
		})
	}
}

// TestMemberSubscriptionsToggle drives the toggle through processIncomingCommand:
// only an admin flips it, and a chat with no posting members is turned away.
func TestMemberSubscriptionsToggle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		chatID   int64
		chatType string
		command  string
		before   bool
		member   *models.ChatMember
		wantText string
		want     bool
	}{
		{
			name:   "an admin enables it",
			chatID: -10, command: "enable_member_subscriptions",
			member:   &models.ChatMember{Type: models.ChatMemberTypeAdministrator},
			wantText: "OK", want: true,
		},
		{
			name:   "an admin disables it again",
			chatID: -10, command: "disable_member_subscriptions", before: true,
			member:   &models.ChatMember{Type: models.ChatMemberTypeAdministrator},
			wantText: "OK",
		},
		{
			name:   "a member is refused",
			chatID: -10, command: "enable_member_subscriptions",
			member:   &models.ChatMember{Type: models.ChatMemberTypeMember},
			wantText: "AdminsOnly",
		},
		{
			name:   "a private chat is told where the setting works",
			chatID: 10, command: "enable_member_subscriptions",
			wantText: "GroupsOnly",
		},
		{
			// A channel post already carries admin rights, so the gate the setting drops never fires.
			name:   "a channel is told where the setting works",
			chatID: -10, chatType: "channel", command: "enable_member_subscriptions",
			member:   &models.ChatMember{Type: models.ChatMemberTypeAdministrator},
			wantText: "GroupsOnly",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.chatMember = func(string, int64, int64) (*models.ChatMember, error) {
				return tc.member, nil
			}
			m := testMessage(w, tc.chatID, tc.command, 0)
			setChatType(w, tc.chatID, tc.chatType)
			w.db.SetMemberSubscriptions(m.userID, tc.before)
			w.processIncomingCommand(m, sender{from: &models.User{ID: 99}}, tc.command, "", false)

			got := w.db.MustBool("select member_subscriptions from users where chat_id = $1", tc.chatID)
			if got != tc.want {
				t.Errorf("member_subscriptions = %v, want %v", got, tc.want)
			}
			if n := w.sendQueue.Len(); n != 1 {
				t.Fatalf("queued replies = %d, want 1", n)
			}
			q := w.sendQueue.pop()
			q.message.render("")
			if text := q.message.(*messageParams).Text; text != tc.wantText {
				t.Errorf("reply = %q, want %q", text, tc.wantText)
			}
		})
	}
}

// setChatType records the chat type an update would carry, empty leaving the row's own.
func setChatType(w *testWorker, chatID int64, chatType string) {
	if chatType == "" {
		return
	}
	w.db.MustExec("update users set chat_type = $1 where chat_id = $2", chatType, chatID)
}

// TestSettingsShowsMemberSubscriptions renders the real settings message,
// where the toggle belongs to a chat with members and the data keys must match the template.
func TestSettingsShowsMemberSubscriptions(t *testing.T) {
	t.Parallel()
	base := filepath.Join("..", "..", "res", "translations")
	tests := []struct {
		name     string
		lang     string
		chatID   int64
		chatType string
		want     bool
	}{
		{name: "a group is offered the toggle", lang: "en", chatID: -10, want: true},
		{name: "a private chat is not", lang: "en", chatID: 10},
		{name: "a channel is not", lang: "en", chatID: -10, chatType: "channel"},
		{name: "a group is offered the toggle in Russian", lang: "ru", chatID: -10, want: true},
		{name: "a private chat is not in Russian", lang: "ru", chatID: 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.tr, w.tpl = cmdlib.LoadAllTranslations(map[string][]string{"test": {
				filepath.Join(base, "common."+tc.lang+".yaml"),
				filepath.Join(base, "chaturbate."+tc.lang+".yaml"),
			}})
			m := testMessage(w, tc.chatID, "settings", 0)
			setChatType(w, tc.chatID, tc.chatType)
			w.settings(m)

			if n := w.sendQueue.Len(); n != 1 {
				t.Fatalf("queued replies = %d, want 1", n)
			}
			q := w.sendQueue.pop()
			q.message.render("")
			text := q.message.(*messageParams).Text
			if got := strings.Contains(text, "/enable_member_subscriptions"); got != tc.want {
				t.Errorf("settings offers the toggle = %v, want %v, in:\n%s", got, tc.want, text)
			}
		})
	}
}
