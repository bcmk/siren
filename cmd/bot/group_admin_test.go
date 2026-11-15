package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bcmk/siren/v7/internal/botconfig"
	"github.com/bcmk/siren/v7/lib/cmdlib"
	"github.com/go-telegram/bot"
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
			w.shuttingDown.Store(tc.shuttingDown)
			looked := false
			w.chatMember = func(string, int64, int64) (*models.ChatMember, error) {
				looked = true
				return tc.member, tc.memberErr
			}
			m := testMessage(w, tc.chatID, "disable_images", 0)
			snd := sender{from: tc.from, senderChat: tc.senderChat}
			w.processIncomingCommand(m, snd, "disable_images", "", false)
			if applied := !w.mustChat(m).ShowImages; applied != tc.wantApplied {
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

func TestBuyTapNeedsAnAdminInAGroupOrChannel(t *testing.T) {
	t.Parallel()
	const channelID = int64(-1001234567890)
	tests := []struct {
		name         string
		chatID       int64
		member       models.ChatMemberType
		memberErr    error
		buyingOff    bool
		shuttingDown bool
		entersFunnel bool
		refused      bool
	}{
		{name: "private chat", chatID: 10, member: models.ChatMemberTypeMember, entersFunnel: true},
		{name: "group admin", chatID: -10, member: models.ChatMemberTypeAdministrator, entersFunnel: true},
		{name: "group owner", chatID: -10, member: models.ChatMemberTypeOwner, entersFunnel: true},
		{name: "group member", chatID: -10, member: models.ChatMemberTypeMember, refused: true},
		{name: "channel admin", chatID: channelID, member: models.ChatMemberTypeAdministrator, entersFunnel: true},
		{name: "channel subscriber", chatID: channelID, member: models.ChatMemberTypeMember, refused: true},
		{
			name:         "private chat during shutdown",
			chatID:       10,
			member:       models.ChatMemberTypeMember,
			shuttingDown: true,
			entersFunnel: true,
		},
		{
			name:         "group admin during shutdown",
			chatID:       -10,
			member:       models.ChatMemberTypeAdministrator,
			shuttingDown: true,
		},
		{name: "group member with buying off", chatID: -10, member: models.ChatMemberTypeMember, buyingOff: true},
		{
			name:      "group admin whose lookup fails",
			chatID:    -10,
			member:    models.ChatMemberTypeAdministrator,
			memberErr: errors.New("timeout"),
		},
		{name: "group admin whose lookup returns null", chatID: -10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			// Every worker shares &testConfig, so copy before enabling the tiers.
			cfg := *w.cfg
			if !tc.buyingOff {
				cfg.SubsTiers = []botconfig.SubsTier{{Count: 5, Cost: 100}}
			}
			w.cfg = &cfg
			w.shuttingDown.Store(tc.shuttingDown)
			// Only the tapper in the tapped chat has a role, so a lookup of anyone else fails.
			w.chatMember = func(_ string, chatID, userID int64) (*models.ChatMember, error) {
				if chatID != tc.chatID || userID != 99 {
					return nil, errors.New("not the tapper in the tapped chat")
				}
				if tc.member == "" {
					return nil, tc.memberErr
				}
				return &models.ChatMember{Type: tc.member}, tc.memberErr
			}
			// A stub server takes the callback answer and hands over its text.
			answers := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				if strings.HasSuffix(req.URL.Path, "/answerCallbackQuery") {
					answers <- req.FormValue("text")
				}
				_, _ = rw.Write([]byte(`{"ok":true,"result":true}`))
			}))
			defer srv.Close()
			stub, err := bot.New("token", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
			if err != nil {
				t.Fatalf("cannot build the stub bot: %v", err)
			}
			w.bots = map[string]*bot.Bot{"test": stub}
			// A stale tier enters the funnel without sending an invoice.
			w.processTGUpdate(incomingPacket{endpoint: "test", message: &models.Update{
				CallbackQuery: &models.CallbackQuery{
					Data: "buy:stars:999",
					From: models.User{ID: 99},
					Message: models.MaybeInaccessibleMessage{
						Type:    models.MaybeInaccessibleMessageTypeMessage,
						Message: &models.Message{Chat: models.Chat{ID: tc.chatID}},
					},
				},
			}})
			var wantFunnel []string
			if tc.entersFunnel {
				wantFunnel = []string{fmt.Sprintf("test %d", tc.chatID)}
			}
			if got := w.mockDB.receivedMessageChats(buyCallbackCommand); !slices.Equal(got, wantFunnel) {
				t.Errorf("the tap entered the funnel for %q, want %q", got, wantFunnel)
			}
			want := ""
			if tc.refused {
				want = "BuyAdminsOnly"
			}
			select {
			case got := <-answers:
				if got != want {
					t.Errorf("callback answer = %q, want %q", got, want)
				}
			default:
				t.Error("the tap was not answered")
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
		{"help", 10},
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
				w.initCache()
				w.zoneNames = loadedZoneNames()
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

// TestAdsNameNoDisabledCommand renders every ad the bot may send
// on a bot that offers no command the table may disable.
func TestAdsNameNoDisabledCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	cfg := *w.cfg
	cfg.OfflineNotifications = false
	w.cfg = &cfg
	base := filepath.Join("..", "..", "res", "translations")
	files := map[string][]string{}
	for _, site := range []string{"chaturbate", "stripchat"} {
		for _, lang := range realLangs {
			files[site+" "+lang] = []string{
				filepath.Join(base, "ads."+lang+".yaml"),
				filepath.Join(base, site+"-ads."+lang+".yaml"),
			}
		}
	}
	w.trAds, w.tplAds = cmdlib.LoadAllAds(files)
	for endpoint := range files {
		for _, ad := range w.trAdsSlice(endpoint) {
			params := &renderParams{templates: w.tplAds[endpoint], key: ad.Key}
			assertNoDisabledCommand(t, w, params.render("@bot"))
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
