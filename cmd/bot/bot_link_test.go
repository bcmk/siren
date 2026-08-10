package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bcmk/siren/v4/internal/botconfig"
	"github.com/bcmk/siren/v4/internal/db"
	"github.com/bcmk/siren/v4/lib/cmdlib"
)

// testBotLinkPeriod is bot_link_period under test, the same twelve the config defaults to.
const testBotLinkPeriod = 12

// botLinkAnchor is what a notification carries, spelled with the test worker's bot name.
const botLinkAnchor = `<a href="https://t.me/bot">@bot</a>`

// TestChannelBotLinkGates pins who meets the bot, and how often:
// only a channel, whose posts carry no bot to click, and there only every bot_link_period-th.
func TestChannelBotLinkGates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		chatType *string
		period   int
		want     []int
	}{
		{"a private chat carries none", ptr("private"), testBotLinkPeriod, nil},
		{"a supergroup carries none", ptr("supergroup"), testBotLinkPeriod, nil},
		{"a chat of unknown type carries none", nil, testBotLinkPeriod, nil},
		{"a zero period carries none", ptr("channel"), 0, nil},
		{"a channel carries every twelfth", ptr("channel"), testBotLinkPeriod, []int{12, 24, 36}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &worker{
				cfg:      &botconfig.Config{BotLinkPeriod: tc.period},
				botNames: map[string]string{"test": "bot"},
			}
			n := db.Notification{Endpoint: "test", ChatType: tc.chatType}
			var linked []int
			// The chat's running total, as notifyOfStatus counts it up one notification at a time.
			for reports := 1; reports <= 3*testBotLinkPeriod; reports++ {
				if got := w.channelBotLink(n, reports); got != "" {
					if got != botLinkAnchor {
						t.Fatalf("link = %q, want %q", got, botLinkAnchor)
					}
					linked = append(linked, reports)
				}
			}
			if !slices.Equal(linked, tc.want) {
				t.Errorf("the link landed on notifications %v, want %v", linked, tc.want)
			}
		})
	}
}

// storeNotification queues one notification of a kind and status, as its producer leaves it.
func storeNotification(
	w *testWorker,
	userID db.UserID,
	kind db.PacketKind,
	status cmdlib.StatusKind,
	nickname string,
) {
	streamerID := insertTestStreamer(&w.db, db.Streamer{Nickname: nickname})
	w.storeNotifications([]db.Notification{{
		Endpoint:   "test",
		UserID:     userID,
		StreamerID: &streamerID,
		Nickname:   nickname,
		Status:     status,
		Priority:   db.PriorityHigh,
		Kind:       kind,
	}})
}

// storeStatusNotifications queues one online alert per streamer for a chat.
// The caller fetches, so several chats can share one batch, as a status change gives them.
func storeStatusNotifications(w *testWorker, userID db.UserID, nicknames ...string) {
	for _, nickname := range nicknames {
		storeNotification(w, userID, db.NotificationPacket, cmdlib.StatusOnline, nickname)
	}
}

// TestQueuingAnAlertCountsIt pins the count to the queue and not to the send:
// a row composed again after a requeue must not count again.
func TestQueuingAnAlertCountsIt(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()

	userID, _ := w.db.AddUser(-1001234567890, 3, 0, "channel")
	storeStatusNotifications(w, userID, "a")

	user, _ := w.db.UserByID(userID)
	if user.Reports != 1 {
		t.Errorf("the chat counts %d queued alerts, want 1", user.Reports)
	}
	// Composed twice over, as a requeue makes the fetch do.
	w.enqueueNotifications(notificationBatch{notifications: w.db.NewNotifications()})
	w.db.RequeueNotification(1)
	w.enqueueNotifications(notificationBatch{notifications: w.db.NewNotifications()})

	user, _ = w.db.UserByID(userID)
	if user.Reports != 1 {
		t.Errorf("composing twice left the chat counting %d, want 1", user.Reports)
	}
}

// TestBatchNumbersEveryNotification covers a chat with several notifications in one fetch,
// which one shared number would give the link to all of them at once or to none.
// The fetch carries one count for the chat, so the numbers are read back off its pending run.
func TestBatchNumbersEveryNotification(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()

	userID, _ := w.db.AddUser(-1001234567890, 3, 0, "channel")
	otherID, _ := w.db.AddUser(-1001234567891, 3, 0, "channel")
	storeStatusNotifications(w, userID, "a", "b", "c")
	storeStatusNotifications(w, otherID, "d")
	nots := w.db.NewNotifications()

	plans := w.planNotifications(nots)
	w.numberNotifications(plans)
	var numbers []int
	for _, p := range plans[:3] {
		numbers = append(numbers, p.reports)
	}
	if want := []int{1, 2, 3}; !slices.Equal(numbers, want) {
		t.Errorf("the chat's three notifications took numbers %v, want %v", numbers, want)
	}
	if got := plans[3].reports; got != 1 {
		t.Errorf("a chat of its own took number %d, want 1", got)
	}
	user, _ := w.db.UserByID(userID)
	if user.Reports != 3 {
		t.Errorf("the chat counts %d notifications, want 3", user.Reports)
	}
}

// TestChannelNotificationReachesTheChatWithTheLink drives the whole seam on the real templates:
// a missing tplData key renders falsey and would leave every other test green
// while the chat never sees a link.
func TestChannelNotificationReachesTheChatWithTheLink(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	cfg := testConfig
	cfg.BotLinkPeriod = testBotLinkPeriod
	w.cfg = &cfg
	w.tpl["test"] = realTemplates(t, "en")

	userID, _ := w.db.AddUser(-1001234567890, testBotLinkPeriod, 0, "channel")
	nicknames := make([]string, testBotLinkPeriod)
	for i := range nicknames {
		nicknames[i] = fmt.Sprintf("streamer%d", i)
	}
	storeStatusNotifications(w, userID, nicknames...)
	w.enqueueNotifications(notificationBatch{notifications: w.db.NewNotifications()})

	var linked []int
	for i := 1; i <= testBotLinkPeriod; i++ {
		q := w.sendQueue.pop()
		if q == nil {
			t.Fatalf("the batch queued %d messages, want %d", i-1, testBotLinkPeriod)
		}
		text, _ := queuedText(t, q.message)
		if strings.Contains(text, botLinkAnchor) {
			linked = append(linked, i)
		}
	}
	if want := []int{testBotLinkPeriod}; !slices.Equal(linked, want) {
		t.Errorf("the link reached messages %v of the batch, want %v", linked, want)
	}
}

// TestOnlyAlertsTakeATurn keeps the every-nth count on messages that can show the link.
// A message that cannot would spend its chat's turn and show nothing,
// leaving the chat a whole period short.
func TestOnlyAlertsTakeATurn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		kind     db.PacketKind
		status   cmdlib.StatusKind
		wantTurn bool
	}{
		{"an online alert takes one", db.NotificationPacket, cmdlib.StatusOnline, true},
		{"an offline alert takes one", db.NotificationPacket, cmdlib.StatusOffline, true},
		{"a command answer takes none", db.ReplyPacket, cmdlib.StatusOnline, false},
		{"a denied status takes none", db.NotificationPacket, cmdlib.StatusDenied, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()

			userID, _ := w.db.AddUser(-1001234567890, 3, 0, "channel")
			storeNotification(w, userID, tc.kind, tc.status, "a")
			plans := w.planNotifications(w.db.NewNotifications())
			w.numberNotifications(plans)

			if got := plans[0].reports == 1; got != tc.wantTurn {
				t.Errorf("the notification took a number: %v, want %v", got, tc.wantTurn)
			}
			user, _ := w.db.UserByID(userID)
			want := 0
			if tc.wantTurn {
				want = 1
			}
			if user.Reports != want {
				t.Errorf("the chat counts %d notifications, want %d", user.Reports, want)
			}
		})
	}
}

// TestStatusTemplatesComposeTheBotLink pins the fragment both status messages carry:
// the link sits a blank line below the status, and only where the notification asks for one.
func TestStatusTemplatesComposeTheBotLink(t *testing.T) {
	t.Parallel()
	const link = `<a href="https://t.me/SirenBot">@SirenBot</a>`
	for _, lang := range realLangs {
		for _, key := range []string{"online", "offline"} {
			t.Run(lang+" "+key, func(t *testing.T) {
				t.Parallel()
				tpl := realTemplates(t, lang)
				render := func(botLink string) string {
					params := &renderParams{templates: tpl, key: key, data: tplData{
						"streamer_link": "alica_webcam",
						"bot_link":      botLink,
					}}
					return params.render("")
				}
				plain, linked := render(""), render(link)
				if strings.Contains(plain, "t.me") {
					t.Errorf("a status carries the bot link unasked: %q", plain)
				}
				if !strings.HasPrefix(linked, plain+"\n\n") {
					t.Errorf("the link did not follow the status a blank line below: %q", linked)
				}
				if note := strings.TrimPrefix(linked, plain+"\n\n"); !strings.Contains(note, link) {
					t.Errorf("the note lost the bot link: %q", note)
				}
			})
		}
	}
}

// TestNewNotificationsCarriesTheChatType pins the column that tells a channel from a group.
// A queued row holds no chat identity, so the fetch joins it.
func TestNewNotificationsCarriesTheChatType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		chatID   int64
		chatType string
	}{
		{"a channel", -1001234567890, "channel"},
		{"a supergroup", -1001234567891, "supergroup"},
		{"a private chat", 1, "private"},
	}
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()

	// One fetch for all three chats, as a real one carries every recipient of a status change.
	fetched := map[db.UserID]db.Notification{}
	userIDs := make([]db.UserID, len(tests))
	for i, tc := range tests {
		userIDs[i], _ = w.db.AddUser(tc.chatID, 3, 0, tc.chatType)
		storeStatusNotifications(w, userIDs[i], tc.chatType)
	}
	for _, n := range w.db.NewNotifications() {
		fetched[n.UserID] = n
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := fetched[userIDs[i]]
			if !ok {
				t.Fatal("the fetch dropped the chat's notification")
			}
			if n.ChatType == nil || *n.ChatType != tc.chatType {
				t.Errorf("chat type = %v, want %q", n.ChatType, tc.chatType)
			}
		})
	}
}
