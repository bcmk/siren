package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	texttemplate "text/template"
	"time"

	"github.com/bcmk/siren/v3/internal/botconfig"
	"github.com/bcmk/siren/v3/internal/db"
	"github.com/bcmk/siren/v3/lib/cmdlib"
	"github.com/go-telegram/bot/models"
)

// nullCommand marks a message log row storing a sql null.
const nullCommand = "<null>"

// completeQueuedSends puts every queued message
// through the real send-result path, as the main loop does.
func completeQueuedSends(t *testing.T, w *testWorker) {
	t.Helper()
	// Drain before completing any result:
	// completeSendResult frees the send slot,
	// and a message still queued would dispatch against a nil bot.
	var queued []*queuedMessage
	for {
		q := w.sendQueue.pop()
		if q == nil {
			break
		}
		queued = append(queued, q)
	}
	for _, q := range queued {
		// trySend resolves the chat id at dispatch, which popping bypasses.
		chatID, ok := w.db.ChatIDForUser(q.userID)
		if !ok {
			t.Fatalf("no chat for user %d", q.userID)
		}
		// completeSendResult refreshes the member count of a group or channel,
		// which calls the endpoint's bot, nil in the test worker.
		if chatID <= 0 && w.bots[q.endpoint] == nil {
			t.Fatalf("completing a send to chat %d needs a bot for endpoint %s",
				chatID, q.endpoint)
		}
		w.completeSendResult(msgSendResult{
			priority:       q.priority,
			timestamp:      100,
			result:         messageSent,
			endpoint:       q.endpoint,
			chatID:         chatID,
			userID:         q.userID,
			tag:            q.tag,
			notificationID: q.notificationID,
		})
	}
	// completeSendResult freed the slot;
	// hold it again so the next phase's messages stay queued for inspection.
	w.commonCooling = true
}

// commandsInLog runs a query selecting one command column,
// reporting a stored null as nullCommand.
// The rows come back sorted,
// neither log carrying a serial id to order by.
func commandsInLog(w *testWorker, query string, params db.QueryParams) []string {
	var command *string
	var commands []string
	w.db.MustQuery(
		query,
		params,
		db.ScanTo{&command},
		func() {
			if command == nil {
				commands = append(commands, nullCommand)
				return
			}
			commands = append(commands, *command)
		})
	slices.Sort(commands)
	return commands
}

// drainSendQueueToLog completes every queued message
// and returns the commands recorded in sent_message_log.
// It clears the log on the way out,
// so a later drain reports only the sends that followed it.
func drainSendQueueToLog(t *testing.T, w *testWorker) []string {
	t.Helper()
	completeQueuedSends(t, w)
	commands := commandsInLog(w, "select command from sent_message_log", nil)
	w.db.MustExec("delete from sent_message_log")
	return commands
}

// A pending subscription's reply arrives
// long after the command that asked for it,
// so the command has to survive pending_subscriptions.
func TestDeferredAddResultLogsItsCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	// The streamer is unknown, so this parks a pending subscription.
	w.addStreamer(testMessage(w, 1, "add", 100), "pending_model", false)

	stored := w.db.MustInt(
		"select count(*) from pending_subscriptions where nickname = $1 and command = $2",
		"pending_model", "add")
	if stored != 1 {
		t.Fatalf("pending_subscriptions did not keep the command, got %d rows", stored)
	}

	// Drop the immediate reply, so the next drain sees the deferred one alone.
	drainSendQueueToLog(t, w)

	w.db.MarkUnconfirmedAsChecking()
	w.processSubsConfirmations(&cmdlib.ExistenceListResults{
		Streamers: map[string]cmdlib.StreamerInfoWithStatus{
			"pending_model": {Status: cmdlib.StatusOnline},
		},
	})

	commands := drainSendQueueToLog(t, w)
	t.Logf("deferred add result logged: %q", commands)
	if !slices.Equal(commands, []string{"add"}) {
		t.Errorf("the deferred add result lost its command, got %q", commands)
	}
}

// Adding an already known streamer queues its current status
// as a deferred reply,
// so that notification has to carry the command too.
func TestDeferredAddedStreamerStatusLogsItsCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	insertTestStreamer(&w.db, db.Streamer{
		Nickname:        "known_model",
		ConfirmedStatus: cmdlib.StatusOnline,
	})
	w.addStreamer(testMessage(w, 1, "add", 100), "known_model", false)

	stored := w.db.MustInt(
		"select count(*) from notification_queue where command = $1", "add")
	if stored != 1 {
		t.Fatalf("notification_queue did not keep the command, got %d rows", stored)
	}

	// Drop the immediate reply, so the next drain sees the deferred one alone.
	drainSendQueueToLog(t, w)

	nots := w.db.NewNotifications()
	if len(nots) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(nots))
	}
	w.enqueueNotifications(notificationBatch{notifications: nots, images: map[string][]byte{}})

	commands := drainSendQueueToLog(t, w)
	t.Logf("deferred status logged: %q", commands)
	if !slices.Equal(commands, []string{"add"}) {
		t.Errorf("the deferred status lost its command, got %q", commands)
	}
}

// The pictures /pics asks for are queued as notifications and sent later,
// so the command has to survive notification_queue.
func TestDeferredOnlinePicsLogItsCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	insertTestStreamer(&w.db, db.Streamer{
		Nickname:          "online_model",
		UnconfirmedStatus: cmdlib.StatusOnline,
	})
	insertSubscription(&w.db, "test", 1, "online_model")
	w.listOnlineStreamers(testMessage(w, 1, "pics", 100))

	stored := w.db.MustInt(
		"select count(*) from notification_queue where command = $1", "pics")
	if stored != 1 {
		t.Fatalf("notification_queue did not keep the command, got %d rows", stored)
	}

	// Drop the immediate hint, so the next drain sees the queued picture alone.
	drainSendQueueToLog(t, w)

	nots := w.db.NewNotifications()
	if len(nots) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(nots))
	}
	if nots[0].Command != "pics" {
		t.Errorf("the command did not survive the queue, got %q", nots[0].Command)
	}
	w.enqueueNotifications(notificationBatch{notifications: nots, images: map[string][]byte{}})

	commands := drainSendQueueToLog(t, w)
	t.Logf("deferred picture logged: %q", commands)
	if !slices.Equal(commands, []string{"pics"}) {
		t.Errorf("the deferred picture lost its command, got %q", commands)
	}
}

// A command the bot does not track is still counted,
// but with no name against it,
// so received_message_log stores a null rather than an empty string.
func TestReceivedCommandIsNullWhenUntracked(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.botNames = map[string]string{"test": "bot"}

	for _, text := range []string{"/start", "/not_a_command"} {
		w.processTGUpdate(incomingPacket{
			endpoint: "test",
			message: &models.Update{Message: &models.Message{
				Text: text,
				Chat: models.Chat{ID: 1, Type: "private"},
			}},
		})
	}

	commands := commandsInLog(w, "select command from received_message_log", nil)
	t.Logf("received_message_log commands: %q", commands)
	if !slices.Equal(commands, []string{nullCommand, "start"}) {
		t.Errorf("untracked command was not stored as a null, got %q", commands)
	}
}

// A capitalized command is the same command,
// so both logs must name it rather than counting it unnamed.
func TestCapitalizedCommandIsTracked(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.botNames = map[string]string{"test": "bot"}

	w.processTGUpdate(incomingPacket{
		endpoint: "test",
		message: &models.Update{Message: &models.Message{
			Text: "/START",
			Chat: models.Chat{ID: 1, Type: "private"},
		}},
	})

	received := commandsInLog(w, "select command from received_message_log", nil)
	sent := drainSendQueueToLog(t, w)
	t.Logf("received %q, sent %q", received, sent)
	if !slices.Equal(received, []string{"start"}) {
		t.Errorf("a capitalized command was counted unnamed, got %q", received)
	}
	if !slices.Equal(sent, []string{"start"}) {
		t.Errorf("the reply did not name the command, got %q", sent)
	}
}

// The copy of a user's feedback goes to the admin, who issued no command,
// so it must not be logged as a reply to one.
func TestFeedbackCopyToAdminLogsNoCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	w.adminUserID = w.db.EnsureUser(99)
	userID := w.db.EnsureUser(1)
	w.feedback(testMessage(w, 1, "feedback", 100), "something is broken")

	completeQueuedSends(t, w)
	forUser := "select command from sent_message_log where user_id = $1"
	adminCommands := commandsInLog(w, forUser, db.QueryParams{int64(w.adminUserID)})
	userCommands := commandsInLog(w, forUser, db.QueryParams{int64(userID)})
	t.Logf("admin %q, user %q", adminCommands, userCommands)
	if !slices.Equal(adminCommands, []string{nullCommand}) {
		t.Errorf("the admin copy was logged as a reply, got %q", adminCommands)
	}
	if !slices.Equal(userCommands, []string{"feedback"}) {
		t.Errorf("the user's reply lost its command, got %q", userCommands)
	}
}

// An admin command is absent from loggedCommands, so the received log never
// names it; its replies must not name it either, or the logs cannot be joined.
func TestAdminReplyLogsNoCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	w.botNames = map[string]string{"test": "bot"}
	w.adminUserID = w.db.EnsureUser(w.cfg.AdminID)
	// Through processTGUpdate, so the received log is written for real.
	w.processTGUpdate(incomingPacket{
		endpoint: "test",
		message: &models.Update{Message: &models.Message{
			Text: "/blacklist 5",
			Chat: models.Chat{ID: w.cfg.AdminID, Type: "private"},
		}},
	})

	received := commandsInLog(w, "select command from received_message_log", nil)
	sent := drainSendQueueToLog(t, w)
	t.Logf("received %q, sent %q", received, sent)
	if !slices.Equal(received, []string{nullCommand}) {
		t.Errorf("the received log named an admin command, got %q", received)
	}
	if !slices.Equal(sent, []string{nullCommand}) {
		t.Errorf("an admin reply named a command the received log lacks, got %q", sent)
	}
}

// The search web app is an inbound path like any other,
// so a chat outside the whitelist must not reach the database through it.
func TestWebAppAddIsWhitelistGated(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		whitelist []int64
		chatID    int64
	}{
		{"outsider", []int64{1}, 999},
		// The production default admits every chat, so nothing but the zero
		// guard stands between chat 0 and EnsureUser.
		{"no chat, no whitelist", nil, 0},
	} {
		// A worker apiece: t.Run runs on its own goroutine,
		// and the database refuses calls from a second one.
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.initCache()
			// Every worker shares &testConfig, so copy before changing it.
			cfg := testConfig
			cfg.WhitelistChats = c.whitelist
			w.cfg = &cfg

			admitted := make(chan bool, 1)
			w.performWebAppAdd(webAppAddRequest{
				endpoint:   "test",
				chatID:     c.chatID,
				nickname:   "some_model",
				admittedCh: admitted,
			})
			if <-admitted {
				t.Error("an unvetted web app add reported success")
			}

			received := commandsInLog(w, "select command from received_message_log", nil)
			users := w.db.MustInt("select count(*) from users")
			sent := drainSendQueueToLog(t, w)
			t.Logf("received %q, sent %q, users %d", received, sent, users)
			if len(received) != 0 || len(sent) != 0 || users != 0 {
				t.Errorf("an unvetted web app add reached the database: "+
					"received %q, sent %q, users %d", received, sent, users)
			}
		})
	}
}

// A duplicate payment replies before the received row is written,
// so that reply must name no command.
func TestDuplicatePaymentReplyLogsNoCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	payment := &models.SuccessfulPayment{
		InvoicePayload:          "stars:subs:1:5",
		TotalAmount:             100,
		TelegramPaymentChargeID: "charge-1",
	}
	w.handleSuccessfulPayment("test", 1, payment, 100)
	// The credit is recorded, so its reply names the payment.
	if got := drainSendQueueToLog(t, w); !slices.Equal(got, []string{paymentCommand}) {
		t.Fatalf("the credited reply did not name the payment, got %q", got)
	}

	// The same charge again: rejected before anything is recorded.
	w.handleSuccessfulPayment("test", 1, payment, 100)
	got := drainSendQueueToLog(t, w)
	t.Logf("duplicate payment reply logged: %q", got)
	if !slices.Equal(got, []string{nullCommand}) {
		t.Errorf("the duplicate reply named an unreceived command, got %q", got)
	}
}

// A pending subscription outlives an endpoint dropped from the config,
// nothing purging that table by endpoint,
// so its add result must be dropped rather than panic the main goroutine.
func TestAddResultForUnknownEndpointIsDropped(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	userID := w.db.EnsureUser(1)
	w.notifyOfAddResults(db.PriorityHigh, []db.Notification{{
		Endpoint: "removed_from_config",
		UserID:   userID,
		Nickname: "some_model",
		Status:   cmdlib.StatusOnline,
		Kind:     db.ReplyPacket,
		Command:  "add",
	}})

	sent := drainSendQueueToLog(t, w)
	t.Logf("sends for an unknown endpoint: %q", sent)
	if len(sent) != 0 {
		t.Errorf("an add result for an unknown endpoint was sent, got %q", sent)
	}
}

// The broadcast ack queues behind the broadcast itself,
// so "OK" tells the admin it was sent rather than merely accepted.
func TestBroadcastAckQueuesBehindTheBroadcast(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	w.adminUserID = w.db.EnsureUser(99)
	insertTestStreamer(&w.db, db.Streamer{Nickname: "some_model"})
	for _, chatID := range []int64{1, 2, 3} {
		insertSubscription(&w.db, "test", chatID, "some_model")
	}

	w.broadcast("test", "hello")

	var last *queuedMessage
	count := 0
	for {
		q := w.sendQueue.pop()
		if q == nil {
			break
		}
		count++
		last = q
	}
	// Three subscribers plus the ack: without the broadcast itself,
	// the ack would trivially be last and prove nothing.
	if count != 4 {
		t.Fatalf("expected 3 broadcast messages and an ack, got %d queued", count)
	}
	t.Logf("queued %d, last to user %d at priority %d", count, last.userID, last.priority)
	if last.userID != w.adminUserID {
		t.Errorf("the ack did not land last, it went to user %d", last.userID)
	}
	if last.priority != db.PriorityLow {
		t.Errorf("the ack jumped the broadcast at priority %d", last.priority)
	}
}

// A refused add is still admitted:
// the user is told why in the chat,
// unlike a non-whitelisted request, which is dropped in silence.
func TestWebAppAddAdmitsARefusedAdd(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	insertTestStreamer(&w.db, db.Streamer{Nickname: "known_model"})
	insertSubscription(&w.db, "test", 1, "known_model")

	admitted := make(chan bool, 1)
	w.performWebAppAdd(webAppAddRequest{
		endpoint:   "test",
		chatID:     1,
		nickname:   "known_model",
		admittedCh: admitted,
	})
	if !<-admitted {
		t.Error("an already subscribed add was reported as not admitted")
	}

	// It refused, so it subscribed nothing new but did answer.
	subs := w.db.MustInt("select count(*) from subscriptions")
	sent := drainSendQueueToLog(t, w)
	t.Logf("subscriptions %d, sent %q", subs, sent)
	if subs != 1 {
		t.Errorf("the refused add changed the subscriptions, got %d", subs)
	}
	if !slices.Equal(sent, []string{webAppAddCommand}) {
		t.Errorf("the refused add did not answer in the chat, got %q", sent)
	}
}

// An ad the /ad command asked for answers it,
// so it is logged against that command like any other reply.
func TestAdCommandNamesItsCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.botNames = map[string]string{"test": "bot"}

	adTpl := texttemplate.New("")
	texttemplate.Must(adTpl.New("some_ad").Parse("SomeAd"))
	w.tplAds = map[string]*texttemplate.Template{"test": adTpl}
	w.trAds = map[string]map[string]*cmdlib.Translation{
		"test": {"some_ad": {Key: "some_ad", Str: "SomeAd", Parse: cmdlib.ParseRaw}},
	}

	w.processTGUpdate(incomingPacket{
		endpoint: "test",
		message: &models.Update{Message: &models.Message{
			Text: "/ad",
			Chat: models.Chat{ID: 1, Type: "private"},
		}},
	})

	commands := drainSendQueueToLog(t, w)
	t.Logf("ad send logged: %q", commands)
	if !slices.Equal(commands, []string{"ad"}) {
		t.Errorf("the ad did not name the command that asked for it, got %q", commands)
	}
}

// A buy menu tap is a command-like event,
// so both logs name it and the tap, invoice and payment steps join up.
func TestBuyCallbackIsLoggedAsReceived(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	// Every worker shares &testConfig, so copy before enabling the tiers.
	cfg := *w.cfg
	cfg.SubsTiers = []botconfig.SubsTier{{Count: 5, Cost: 100}}
	w.cfg = &cfg

	// A tier that no longer exists, so it re-shows the menu rather than
	// sending an invoice, which would need a bot the test worker has not got.
	// The tap still counts: the user meant to buy and could not.
	w.handleBuyCallback("test", 1, "buy:stars:999")

	received := commandsInLog(w, "select command from received_message_log", nil)
	sent := drainSendQueueToLog(t, w)
	t.Logf("stale tier: received %q, sent %q", received, sent)
	if !slices.Equal(received, []string{buyCallbackCommand}) {
		t.Errorf("the tap was not recorded as received, got %q", received)
	}
	if !slices.Equal(sent, []string{buyCallbackCommand}) {
		t.Errorf("the menu did not name the tap that asked for it, got %q", sent)
	}

	// A payload we cannot read is no funnel entry, so neither log names it.
	w.handleBuyCallback("test", 1, "buy:")
	received = commandsInLog(w, "select command from received_message_log", nil)
	sent = drainSendQueueToLog(t, w)
	t.Logf("unreadable: received %q, sent %q", received, sent)
	if !slices.Equal(received, []string{buyCallbackCommand}) {
		t.Errorf("an unreadable payload was counted as a tap, got %q", received)
	}
	if !slices.Equal(sent, []string{nullCommand}) {
		t.Errorf("an unreadable payload named a command, got %q", sent)
	}
}

// The web app vets its caller before the search term,
// so the endpoint answers one way whatever was typed.
func TestWebAppSearchVetsItsCaller(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		whitelist  []int64
		user       string
		wantChatID int64
		wantStatus int
	}{
		{"whitelisted", []int64{1}, `{"id":1}`, 1, 0},
		{"outsider", []int64{1}, `{"id":999}`, 999, http.StatusForbidden},
		{"no user id", []int64{1}, `{"id":0}`, 0, http.StatusBadRequest},
		{"unparseable", []int64{1}, `not json`, 0, http.StatusBadRequest},
		// The production default: an empty whitelist admits every chat.
		{"no whitelist", nil, `{"id":999}`, 999, 0},
		{"no whitelist, no user id", nil, `{"id":0}`, 0, http.StatusBadRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig
			cfg.WhitelistChats = c.whitelist
			w := &worker{cfg: &cfg}
			chatID, status := w.searchCaller(url.Values{"user": {c.user}})
			if chatID != c.wantChatID || status != c.wantStatus {
				t.Errorf("got chat %d status %d, want chat %d status %d",
					chatID, status, c.wantChatID, c.wantStatus)
			}
		})
	}
}

// A vetted search is recorded, so web app usage can be counted;
// an unvetted one must reach neither the users table nor the log,
// EnsureUser having no zero check of its own.
func TestWebAppSearchWritesOnlyForAVettedChat(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.fuzzySearchDB = w.db
	// Every worker shares &testConfig, so copy before narrowing the whitelist.
	cfg := *w.cfg
	cfg.WhitelistChats = []int64{1}
	w.cfg = &cfg

	search := func(chatID int64) searchResult {
		req := searchRequest{
			endpoint: "test",
			chatID:   chatID,
			term:     "someone",
			resultCh: make(chan searchResult, 1),
		}
		w.handleSearchRequest(req)
		return <-req.resultCh
	}

	for _, chatID := range []int64{999, 0} {
		res := search(chatID)
		received := commandsInLog(w, "select command from received_message_log", nil)
		users := w.db.MustInt("select count(*) from users")
		t.Logf("chat %d: allowed %v, received %q, users %d",
			chatID, res.allowed, received, users)
		if res.allowed || res.streamers != nil || len(received) != 0 || users != 0 {
			t.Errorf("an unvetted chat %d was served: allowed %v, received %q, users %d",
				chatID, res.allowed, received, users)
		}
	}

	search(1)
	received := commandsInLog(w, "select command from received_message_log", nil)
	t.Logf("vetted: received %q", received)
	if !slices.Equal(received, []string{searchCommand}) {
		t.Errorf("the search was not recorded, got %q", received)
	}
}

// The /buy_subs command reaches the same menu as a tap,
// so its reply names the command rather than the tap.
func TestBuySubsCommandNamesItself(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.botNames = map[string]string{"test": "bot"}

	// Every worker shares &testConfig, so copy before enabling the tiers.
	cfg := *w.cfg
	cfg.SubsTiers = []botconfig.SubsTier{{Count: 5, Cost: 100}}
	w.cfg = &cfg

	w.processTGUpdate(incomingPacket{
		endpoint: "test",
		message: &models.Update{Message: &models.Message{
			Text: "/buy_subs",
			Chat: models.Chat{ID: 1, Type: "private"},
		}},
	})

	commands := drainSendQueueToLog(t, w)
	t.Logf("buy_subs menu logged: %q", commands)
	if !slices.Equal(commands, []string{"buy_subs"}) {
		t.Errorf("the menu did not name the command that asked for it, got %q", commands)
	}
}

// The main loop is the only reader of the web app's requests,
// so a submit arriving after it stops must be answered, not parked.
func TestWebAppAddGivesUpOnShutdown(t *testing.T) {
	t.Parallel()
	newReq := func() webAppAddRequest {
		return webAppAddRequest{
			endpoint:   "test",
			chatID:     1,
			nickname:   "some_model",
			admittedCh: make(chan bool, 1),
		}
	}

	// No reader, and the loop has gone: the submit reports it rather than blocks.
	gone := &worker{
		webAppAddRequests: make(chan webAppAddRequest),
		shutdownCh:        make(chan struct{}),
	}
	close(gone.shutdownCh)
	// The verdict comes back on a channel, never through t:
	// a parked submit outlives the test, and touching t then panics
	// over the very failure the timeout is reporting.
	type outcome struct{ admitted, alive bool }
	got := make(chan outcome, 1)
	go func() {
		admitted, alive := gone.submitWebAppAdd(newReq())
		got <- outcome{admitted: admitted, alive: alive}
	}()
	select {
	case o := <-got:
		if o.alive || o.admitted {
			t.Errorf("a submit after shutdown reported alive=%v admitted=%v", o.alive, o.admitted)
		}
	case <-time.After(time.Second):
		t.Fatal("the submit parked instead of giving up")
	}

	// A running loop answers, and the verdict comes back.
	running := &worker{
		webAppAddRequests: make(chan webAppAddRequest),
		shutdownCh:        make(chan struct{}),
	}
	go func() {
		req := <-running.webAppAddRequests
		req.admittedCh <- true
	}()
	if admitted, alive := running.submitWebAppAdd(newReq()); !alive || !admitted {
		t.Errorf("a live loop reported alive=%v admitted=%v", alive, admitted)
	}
}

// A send nothing asked for must not borrow a command.
func TestUnpromptedSendLogsNoCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	userID := w.db.EnsureUser(1)
	w.sendText(db.PriorityLow, "test", userID, false, true,
		cmdlib.ParseRaw, "broadcast", unprompted(db.MessagePacket))

	commands := drainSendQueueToLog(t, w)
	t.Logf("unprompted send logged: %q", commands)
	if !slices.Equal(commands, []string{nullCommand}) {
		t.Errorf("an unprompted send recorded a command, got %q", commands)
	}
}

// An unrecognized word is not a command and must not reach the log.
func TestUnknownCommandLogsNoCommand(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()

	w.botNames = map[string]string{"test": "bot"}
	w.processTGUpdate(incomingPacket{
		endpoint: "test",
		message: &models.Update{Message: &models.Message{
			Text: "/not_a_command",
			Chat: models.Chat{ID: 1, Type: "private"},
		}},
	})

	commands := drainSendQueueToLog(t, w)
	t.Logf("unknown command logged: %q", commands)
	if !slices.Equal(commands, []string{nullCommand}) {
		t.Errorf("arbitrary user text reached the log, got %q", commands)
	}
}

// searchConfig writes a minimal valid config and reads it back,
// the only way to obtain a populated Endpoints map: its value type
// is unexported, so a test cannot build one by hand.
func searchConfig(t *testing.T, botToken string, whitelist []int64) *botconfig.Config {
	t.Helper()
	cfg := map[string]any{
		"listen_address":                     ":0",
		"admin_endpoint":                     "test",
		"period_seconds":                     1,
		"max_subs":                           3,
		"admin_id":                           1,
		"db_connection_string":               "postgres://",
		"website":                            "siren",
		"website_link":                       "https://example.invalid",
		"heavy_user_remainder":               1,
		"referral_bonus":                     1,
		"follower_bonus":                     1,
		"telegram_timeout_seconds":           10,
		"max_subscriptions_for_pics":         10,
		"subs_confirmation_period_seconds":   1,
		"notifications_ready_period_seconds": 1,
		"whitelist_chats":                    whitelist,
		"endpoints": map[string]any{"test": map[string]any{
			"listen_path":          "/x",
			"bot_token":            botToken,
			"translation":          []string{"t.yaml"},
			"images":               "img",
			"maintenance_response": "later",
		}},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("cannot encode test config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bot.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("cannot write test config: %v", err)
	}
	return botconfig.ReadConfig(path)
}

// initDataFor signs web app init data the way Telegram does,
// so parseInitData accepts it.
func initDataFor(botToken string, userID int64) string {
	values := url.Values{
		"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)},
		"user":      {fmt.Sprintf(`{"id":%d}`, userID)},
	}
	keys := slices.Sorted(maps.Keys(values))
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+values.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	h := hmac.New(sha256.New, secret.Sum(nil))
	h.Write([]byte(strings.Join(parts, "\n")))
	values.Set("hash", hex.EncodeToString(h.Sum(nil)))
	return values.Encode()
}

// handleSearch must act on the status searchCaller returns,
// so a refused caller never reaches the search at all.
func TestHandleSearchActsOnTheStatus(t *testing.T) {
	t.Parallel()
	const botToken = "123:test-token"
	w := &worker{
		cfg:            searchConfig(t, botToken, []int64{1}),
		searchRequests: make(chan searchRequest, 1),
	}

	get := func(userID int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/apps/add/api/search?endpoint=test&term=some", nil)
		r.Header.Set("X-Init-Data", initDataFor(botToken, userID))
		rw := httptest.NewRecorder()
		w.handleSearch(rw, r)
		return rw
	}

	if rw := get(999); rw.Code != http.StatusForbidden {
		t.Errorf("an outsider got %d, want %d", rw.Code, http.StatusForbidden)
	}
	if queued := len(w.searchRequests); queued != 0 {
		t.Errorf("a refused caller still queued %d searches", queued)
	}

	// No daemon is running, so the test answers in its place.
	serve := func(answer searchResult) *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- get(1) }()
		select {
		case req := <-w.searchRequests:
			if req.chatID != 1 {
				t.Errorf("queued chat %d, want 1", req.chatID)
			}
			req.resultCh <- answer
		case <-time.After(time.Second):
			t.Fatal("a vetted caller never reached the daemon")
		}
		select {
		case rw := <-done:
			return rw
		case <-time.After(time.Second):
			t.Fatal("the handler never answered")
			return nil
		}
	}

	if rw := serve(searchResult{allowed: true}); rw.Code != http.StatusOK {
		t.Errorf("a vetted caller got %d, want %d", rw.Code, http.StatusOK)
	}
	// The daemon's own guard must answer as searchCaller would, not with 200.
	if rw := serve(searchResult{}); rw.Code != http.StatusForbidden {
		t.Errorf("a daemon refusal got %d, want %d", rw.Code, http.StatusForbidden)
	}
}
