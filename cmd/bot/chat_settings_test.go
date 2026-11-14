package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/bcmk/siren/v7/internal/checkers"
	"github.com/bcmk/siren/v7/internal/db"
	"github.com/bcmk/siren/v7/lib/cmdlib"
)

const (
	settingsGroupID  = int64(-1001234567890)
	settingsAdminID  = int64(1)
	settingsMemberID = int64(2)
)

var (
	settingsAdmin  = sender{from: &models.User{ID: settingsAdminID}}
	settingsMember = sender{from: &models.User{ID: settingsMemberID}}
)

// chatSettingsFixture is a group with chats on two endpoints, both subscribed to the streamer.
type chatSettingsFixture struct {
	t          *testing.T
	w          *testWorker
	userID     db.UserID
	streamerID int
}

func newChatSettingsFixture(t *testing.T) *chatSettingsFixture {
	w := newTestWorker()
	cfg := testConfig
	cfg.AffiliateBase = "https://siren.chat/out/cb"
	cfg.EnableCustomAffiliateLink = true
	cfg.MaxSubscriptionsForPics = 1
	cfg.EnableWeek = true
	cfg.EnableMonth = true
	w.cfg = &cfg
	w.checker = &checkers.ChaturbateChecker{}
	files := realTranslations("en")["test"]
	w.loadTranslations(map[string][]string{"test": files, "other": files})
	w.sender("other").cooling = true
	w.ownerUserID = w.db.EnsureUser(cfg.OwnerID)
	w.chatMember = func(_ string, _ int64, userID int64) (*models.ChatMember, error) {
		if userID == settingsAdminID {
			return &models.ChatMember{Type: models.ChatMemberTypeAdministrator}, nil
		}
		return &models.ChatMember{Type: models.ChatMemberTypeMember}, nil
	}
	w.unconfirmedOnlineStreamers = map[string]cmdlib.StreamerInfo{
		"known": {ImageURL: "https://example.com/known.jpg", Subject: "a subject"},
	}
	f := &chatSettingsFixture{t: t, w: w}
	f.streamerID = w.mockDB.insertStreamer(db.Streamer{Nickname: "known", UnconfirmedStatus: cmdlib.StatusOnline}).ID
	for _, endpoint := range []string{"test", "other"} {
		f.userID, _ = w.db.AddUserAndChat(endpoint, settingsGroupID, 0, string(models.ChatTypeSupergroup))
		insertSubscription(w.mockDB, endpoint, settingsGroupID, "known")
	}
	return f
}

// run sends a command line to the group's chat on endpoint, or an owner command from the owner,
// returning what it queued there.
func (f *chatSettingsFixture) run(endpoint string, snd sender, line string) []*queuedMessage {
	command, arguments, _ := strings.Cut(line, " ")
	chatID := settingsGroupID
	if _, ok := ownerCommands[command]; ok {
		chatID = f.w.cfg.OwnerID
	}
	m, userCreated := f.w.newReceivedMessage(100, endpoint, chatID, "", command)
	f.w.processIncomingCommand(m, snd, command, arguments, userCreated)
	return f.drain(endpoint)
}

func (f *chatSettingsFixture) drain(endpoint string) (queued []*queuedMessage) {
	q := &f.w.sender(endpoint).queue
	for m := q.pop(); m != nil; m = q.pop() {
		queued = append(queued, m)
	}
	return
}

func (f *chatSettingsFixture) only(queued []*queuedMessage) *messageParams {
	f.t.Helper()
	if len(queued) != 1 {
		f.t.Fatalf("queued %d messages, want 1", len(queued))
	}
	return queued[0].message.(*messageParams)
}

// replyData is what the one reply to a command line on endpoint hands its template.
func (f *chatSettingsFixture) replyData(endpoint string, line string) tplData {
	return f.only(f.run(endpoint, settingsAdmin, line)).renderParams.data
}

// subscribedByAdd reports whether /add from snd subscribes the chat on endpoint.
func (f *chatSettingsFixture) subscribedByAdd(endpoint string, snd sender) bool {
	f.run(endpoint, snd, "add newbie")
	return f.w.db.SubscribedOrPending(endpoint, f.userID, "newbie")
}

// hasOfflineAlert reports whether the streamer going offline alerts the chat on endpoint.
func (f *chatSettingsFixture) hasOfflineAlert(endpoint string) bool {
	for _, n := range f.build(cmdlib.StatusOffline, cmdlib.StatusOnline) {
		if n.Endpoint == endpoint {
			return true
		}
	}
	return false
}

// onlineAlert is the alert the streamer going online builds for the chat on endpoint.
func (f *chatSettingsFixture) onlineAlert(endpoint string) db.Notification {
	f.t.Helper()
	for _, n := range f.build(cmdlib.StatusOnline, cmdlib.StatusOffline) {
		if n.Endpoint == endpoint {
			return n
		}
	}
	f.t.Fatalf("no online alert for %s", endpoint)
	return db.Notification{}
}

func (f *chatSettingsFixture) build(status, prev cmdlib.StatusKind) []db.Notification {
	return f.w.buildNotifications([]db.ConfirmedStatusChange{{
		StreamerID: f.streamerID,
		Nickname:   "known",
		Status:     status,
		PrevStatus: prev,
	}}, 100)
}

// onlineReply is what /online queues of the streamer on endpoint.
func (f *chatSettingsFixture) onlineReply(endpoint string) db.Notification {
	f.t.Helper()
	f.run(endpoint, settingsAdmin, "online")
	nots := f.w.db.NewNotifications()
	if len(nots) != 1 {
		f.t.Fatalf("/online queued %d notifications, want 1", len(nots))
	}
	return nots[0]
}

// composedAlert is what the streamer's queued online alert composes for the chat on endpoint.
func (f *chatSettingsFixture) composedAlert(endpoint string) *messageParams {
	f.w.storeNotifications([]db.Notification{{
		Endpoint:   endpoint,
		UserID:     f.userID,
		StreamerID: &f.streamerID,
		Nickname:   "known",
		Status:     cmdlib.StatusOnline,
		Priority:   db.PriorityLow,
		Kind:       db.NotificationPacket,
	}})
	f.w.enqueueNotifications(f.w.db.NewNotifications())
	return f.only(f.drain(endpoint))
}

// listLink is the link /list gives the streamer on endpoint.
func (f *chatSettingsFixture) listLink(endpoint string) string {
	data := f.replyData(endpoint, "list")
	entries := append(data["online"].([]streamerListEntry), data["offline"].([]streamerListEntry)...)
	if len(entries) != 1 {
		f.t.Fatalf("listed %d streamers, want 1", len(entries))
	}
	return entries[0].Link
}

// weekOrMonthListLink is the link a /week or /month list reply gives the streamer on endpoint.
func (f *chatSettingsFixture) weekOrMonthListLink(endpoint string, command string) any {
	return f.replyData(endpoint, command)["rows"].([]tplData)[0]["streamer_link"]
}

// usageLimit is the limit the usage reply to an /add over the limit shows on endpoint,
// nil if none came.
func (f *chatSettingsFixture) usageLimit(endpoint string) any {
	for _, m := range f.run(endpoint, settingsAdmin, "add newbie") {
		if limit, ok := m.message.(*messageParams).renderParams.data["total_subscriptions"]; ok {
			return limit
		}
	}
	return nil
}

// settingOutcome is what /settings shows of a setting and the effect the bot gives it.
type settingOutcome struct {
	shown  any
	effect any
}

// TestChatSettingsApplyToOneChat sets each setting through its command on one endpoint
// and checks /settings and where the bot reads it on both.
func TestChatSettingsApplyToOneChat(t *testing.T) {
	t.Parallel()
	const affiliateLink = "https://chaturbate.com/in/?tour=t1&campaign=c1&track=default"
	affiliate := map[string]string{"campaign": "c1", "tour": "t1", "track": "default"}
	const plainLink = "<a href='https://siren.chat/out/cb/known'>known</a>"
	const affiliateStreamerLink = "<a href='https://siren.chat/out/cb/known?campaign=c1&amp;tour=t1&amp;track=default'>known</a>"
	tests := []struct {
		name string
		// before runs on both endpoints, giving the command something to undo.
		before string
		// command runs on the test endpoint.
		command     string
		settingsKey string
		// observe reads the setting's effect on endpoint.
		observe func(f *chatSettingsFixture, endpoint string) any
		// want is seen on the test endpoint, wantOther on the other.
		want, wantOther settingOutcome
	}{
		{
			name:        "the limit caps /add",
			command:     fmt.Sprintf("set_max_subs %d 1", settingsGroupID),
			settingsKey: "total_subscriptions",
			observe:     func(f *chatSettingsFixture, e string) any { return f.subscribedByAdd(e, settingsAdmin) },
			want:        settingOutcome{shown: 1, effect: false},
			wantOther:   settingOutcome{shown: testConfig.MaxSubs, effect: true},
		},
		{
			name:        "the limit reaches the usage reply",
			command:     fmt.Sprintf("set_max_subs %d 1", settingsGroupID),
			settingsKey: "total_subscriptions",
			observe:     func(f *chatSettingsFixture, e string) any { return f.usageLimit(e) },
			want:        settingOutcome{shown: 1, effect: 1},
			wantOther:   settingOutcome{shown: testConfig.MaxSubs, effect: nil},
		},
		{
			name:        "the limit reaches /referral",
			command:     fmt.Sprintf("set_max_subs %d 1", settingsGroupID),
			settingsKey: "total_subscriptions",
			observe: func(f *chatSettingsFixture, e string) any {
				return f.replyData(e, "referral")["total_subscriptions"]
			},
			want:      settingOutcome{shown: 1, effect: 1},
			wantOther: settingOutcome{shown: testConfig.MaxSubs, effect: testConfig.MaxSubs},
		},
		{
			name:        "images leave the alert",
			command:     "disable_images",
			settingsKey: "show_images",
			observe:     func(f *chatSettingsFixture, e string) any { return f.onlineAlert(e).ImageURL },
			want:        settingOutcome{shown: false, effect: ""},
			wantOther:   settingOutcome{shown: true, effect: "https://example.com/known.jpg"},
		},
		{
			name:        "offline alerts stop",
			command:     "disable_offline_notifications",
			settingsKey: "offline_notifications",
			observe:     func(f *chatSettingsFixture, e string) any { return f.hasOfflineAlert(e) },
			want:        settingOutcome{shown: false, effect: false},
			wantOther:   settingOutcome{shown: true, effect: true},
		},
		{
			name:        "the subject leaves the alert",
			command:     "disable_subject",
			settingsKey: "show_subject",
			observe:     func(f *chatSettingsFixture, e string) any { return f.onlineAlert(e).Subject },
			want:        settingOutcome{shown: false, effect: ""},
			wantOther:   settingOutcome{shown: true, effect: "a subject"},
		},
		{
			name:        "the subject leaves /online",
			command:     "disable_subject",
			settingsKey: "show_subject",
			observe:     func(f *chatSettingsFixture, e string) any { return f.onlineReply(e).Subject },
			want:        settingOutcome{shown: false, effect: ""},
			wantOther:   settingOutcome{shown: true, effect: "a subject"},
		},
		{
			name:        "alerts go silent",
			command:     "enable_silent_messages",
			settingsKey: "silent_messages",
			observe:     func(f *chatSettingsFixture, e string) any { return f.composedAlert(e).DisableNotification },
			want:        settingOutcome{shown: true, effect: true},
			wantOther:   settingOutcome{shown: false, effect: false},
		},
		{
			name:        "members may /add",
			command:     "enable_member_subscriptions",
			settingsKey: "member_subscriptions",
			observe:     func(f *chatSettingsFixture, e string) any { return f.subscribedByAdd(e, settingsMember) },
			want:        settingOutcome{shown: true, effect: true},
			wantOther:   settingOutcome{shown: false, effect: false},
		},
		{
			name:        "the affiliate link reaches alerts",
			command:     "affiliate " + affiliateLink,
			settingsKey: "affiliate_params",
			observe: func(f *chatSettingsFixture, e string) any {
				return f.composedAlert(e).renderParams.data["streamer_link"]
			},
			want:      settingOutcome{shown: affiliate, effect: affiliateStreamerLink},
			wantOther: settingOutcome{shown: map[string]string(nil), effect: plainLink},
		},
		{
			name:        "the affiliate link reaches /list",
			command:     "affiliate " + affiliateLink,
			settingsKey: "affiliate_params",
			observe:     func(f *chatSettingsFixture, e string) any { return f.listLink(e) },
			want:        settingOutcome{shown: affiliate, effect: affiliateStreamerLink},
			wantOther:   settingOutcome{shown: map[string]string(nil), effect: plainLink},
		},
		{
			name:        "the affiliate link reaches /week of a streamer",
			command:     "affiliate " + affiliateLink,
			settingsKey: "affiliate_params",
			observe: func(f *chatSettingsFixture, e string) any {
				return f.replyData(e, "week known")["streamer_link"]
			},
			want:      settingOutcome{shown: affiliate, effect: affiliateStreamerLink},
			wantOther: settingOutcome{shown: map[string]string(nil), effect: plainLink},
		},
		{
			name:        "the affiliate link reaches the /week list",
			command:     "affiliate " + affiliateLink,
			settingsKey: "affiliate_params",
			observe:     func(f *chatSettingsFixture, e string) any { return f.weekOrMonthListLink(e, "week") },
			want:        settingOutcome{shown: affiliate, effect: affiliateStreamerLink},
			wantOther:   settingOutcome{shown: map[string]string(nil), effect: plainLink},
		},
		{
			name:        "the affiliate link reaches /month of a streamer",
			command:     "affiliate " + affiliateLink,
			settingsKey: "affiliate_params",
			observe: func(f *chatSettingsFixture, e string) any {
				return f.replyData(e, "month known")["streamer_link"]
			},
			want:      settingOutcome{shown: affiliate, effect: affiliateStreamerLink},
			wantOther: settingOutcome{shown: map[string]string(nil), effect: plainLink},
		},
		{
			name:        "the affiliate link reaches the /month list",
			command:     "affiliate " + affiliateLink,
			settingsKey: "affiliate_params",
			observe:     func(f *chatSettingsFixture, e string) any { return f.weekOrMonthListLink(e, "month") },
			want:        settingOutcome{shown: affiliate, effect: affiliateStreamerLink},
			wantOther:   settingOutcome{shown: map[string]string(nil), effect: plainLink},
		},
		{
			name:        "the affiliate reset leaves alerts",
			before:      "affiliate " + affiliateLink,
			command:     "reset_affiliate",
			settingsKey: "affiliate_params",
			observe: func(f *chatSettingsFixture, e string) any {
				return f.composedAlert(e).renderParams.data["streamer_link"]
			},
			want:      settingOutcome{shown: map[string]string(nil), effect: plainLink},
			wantOther: settingOutcome{shown: affiliate, effect: affiliateStreamerLink},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newChatSettingsFixture(t)
			if tc.before != "" {
				f.run("test", settingsAdmin, tc.before)
				f.run("other", settingsAdmin, tc.before)
			}
			f.run("test", settingsAdmin, tc.command)
			for _, chat := range []struct {
				endpoint string
				want     settingOutcome
			}{{"test", tc.want}, {"other", tc.wantOther}} {
				got := f.replyData(chat.endpoint, "settings")[tc.settingsKey]
				if fmt.Sprint(got) != fmt.Sprint(chat.want.shown) {
					t.Errorf("/settings on %s shows %s = %v, want %v",
						chat.endpoint, tc.settingsKey, got, chat.want.shown)
				}
				if got := tc.observe(f, chat.endpoint); fmt.Sprint(got) != fmt.Sprint(chat.want.effect) {
					t.Errorf("on %s the effect is %v, want %v", chat.endpoint, got, chat.want.effect)
				}
			}
		})
	}
}

// /set_max_subs sets the limit of the user's chat on the endpoint the owner wrote to.
func TestSetMaxSubsSetsOneChat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// chatOn is the endpoint the user has a chat on, empty for no user.
		chatOn string
		reply  string
		// limits are the user's limits afterwards, by the endpoints it has chats on.
		limits map[string]int
	}{
		{name: "a chat on the endpoint", chatOn: "test", reply: "OK", limits: map[string]int{"test": 7}},
		{
			name:   "a user with no chat there",
			chatOn: "other",
			reply:  "no such chat",
			limits: map[string]int{"other": testConfig.MaxSubs},
		},
		{name: "an unknown user", reply: "no such user"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			w.ownerUserID = w.db.EnsureUser(w.cfg.OwnerID)
			if tc.chatOn != "" {
				w.db.EnsureUserAndChat(tc.chatOn, 500)
			}

			m := testMessage(w, w.cfg.OwnerID, "set_max_subs", 100)
			w.processIncomingCommand(m, sender{}, "set_max_subs", "500 7", false)

			if n := w.sendQueue.Len(); n != 1 {
				t.Fatalf("queued replies = %d, want 1", n)
			}
			if got := w.sendQueue.pop().message.(*messageParams).Text; got != tc.reply {
				t.Errorf("reply = %q, want %q", got, tc.reply)
			}
			user, found := w.db.UserByChatID(500)
			if !found {
				return
			}
			for _, endpoint := range []string{"test", "other"} {
				chat, found := w.db.Chat(endpoint, user.UserID)
				want, wantFound := tc.limits[endpoint]
				if found != wantFound || chat.MaxSubs != want {
					t.Errorf("the chat on %s: found = %v, limit = %d, want %v, %d",
						endpoint, found, chat.MaxSubs, wantFound, want)
				}
			}
		})
	}
}

// /sure_remove_all empties the chat it came from and no other.
func TestSureRemoveAllEmptiesOneChat(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	w.loadTranslations(realTranslations("en"))
	w.mockDB.insertStreamer(db.Streamer{Nickname: "subscribed"})
	tests := []struct {
		name     string
		endpoint string
		chatID   int64
		want     int
	}{
		{"the chat it came from", "test", 10, 0},
		{"the user's chat on another endpoint", "other", 10, 1},
		{"another user's chat", "test", 20, 1},
	}
	for _, tc := range tests {
		insertSubscription(w.mockDB, tc.endpoint, tc.chatID, "subscribed")
		insertPendingSubscriptionForChat(w.mockDB, tc.endpoint, tc.chatID, "pending", false)
	}

	m := testMessage(w, 10, "sure_remove_all", 100)
	w.processIncomingCommand(m, sender{}, "sure_remove_all", "", false)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subscriptions := count(w.mockDB.subscriptions, func(s mockSubscription) bool {
				return s.endpoint == tc.endpoint && w.mockDB.chatID(s.userID) == tc.chatID
			})
			pending := count(w.mockDB.pendingSubscriptions, func(p *mockPendingSubscription) bool {
				return p.Endpoint == tc.endpoint && w.mockDB.chatID(p.UserID) == tc.chatID
			})
			if subscriptions != tc.want {
				t.Errorf("subscriptions = %d, want %d", subscriptions, tc.want)
			}
			if pending != tc.want {
				t.Errorf("pending subscriptions = %d, want %d", pending, tc.want)
			}
		})
	}
}

// A send result counts against the blocked-send count of its own bot's chat alone
func TestSendResultBlockCountsOnItsEndpoint(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	// other first, so a count landing on the user's first chat misses test
	userID := w.db.EnsureUserAndChat("other", 1)
	w.db.EnsureUserAndChat("test", 1)

	// The steps share the counts, and the database takes queries from one goroutine,
	// so they run in order here rather than as subtests
	for _, step := range []struct {
		name      string
		result    int
		wantBlock int
	}{
		{"a blocked send", messageBlocked, 1},
		{"a delivered send", messageSent, 0},
	} {
		w.handleSendResult(msgSendResult{
			result:   step.result,
			endpoint: "test",
			userID:   userID,
			tag:      unprompted(db.MessagePacket),
		})
		if chat, _ := w.db.Chat("test", userID); chat.Blocked != step.wantBlock {
			t.Errorf("after %s, block on test = %d, want %d", step.name, chat.Blocked, step.wantBlock)
		}
		if chat, _ := w.db.Chat("other", userID); chat.Blocked != 0 {
			t.Errorf("after %s, block on other = %d, want 0", step.name, chat.Blocked)
		}
		if got := w.mockDB.sentMessageLog[len(w.mockDB.sentMessageLog)-1].endpoint; got != "test" {
			t.Errorf("after %s, the send was logged on %s, want test", step.name, got)
		}
	}
}
