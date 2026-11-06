package main

import (
	"reflect"
	"slices"
	"testing"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
	"github.com/go-telegram/bot/models"
)

func checkUnconfirmedOnlineStreamers(w *testWorker, t *testing.T) {
	dbOnline := w.mockDB.OnlineStreamers()
	if len(w.unconfirmedOnlineStreamers) != len(dbOnline) {
		t.Errorf("unconfirmedOnlineStreamers size %d != DB size %d", len(w.unconfirmedOnlineStreamers), len(dbOnline))
	}
	for ch := range dbOnline {
		if _, ok := w.unconfirmedOnlineStreamers[ch]; !ok {
			t.Errorf("streamer %s in DB but not in unconfirmedOnlineStreamers", ch)
		}
	}
	for ch := range w.unconfirmedOnlineStreamers {
		if !dbOnline[ch] {
			t.Errorf("streamer %s in unconfirmedOnlineStreamers but not in DB", ch)
		}
	}
}

func TestUpdateNotifications(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	w.initCache()

	for _, n := range []string{"a", "b", "c", "d"} {
		w.mockDB.insertStreamer(db.Streamer{Nickname: n})
	}
	insertSubscription(w.mockDB, "ep1", 1, "a")
	insertSubscription(w.mockDB, "ep1", 2, "b")
	insertSubscription(w.mockDB, "ep1", 3, "a")
	insertSubscription(w.mockDB, "ep1", 3, "c")
	insertSubscription(w.mockDB, "ep1", 4, "d")
	insertSubscription(w.mockDB, "ep2", 4, "d")

	// All subscribed streamers for this test
	allStreamers := map[string]bool{"a": true, "b": true, "c": true, "d": true}

	// Use fixed list checker mode for these tests
	// Streamer "a" goes online — 2 notifications (chat 1 and chat 3)
	result := &cmdlib.FixedListOnlineResults{
		RequestedStreamers: allStreamers,
		Streamers:          map[string]cmdlib.StreamerInfo{"a": {}},
	}
	if r := w.handleCheckerResults(result, 2); len(r.notifications) != 2 {
		t.Errorf("expected 2 notifications for streamer 'a' online, got %d", len(r.notifications))
	}
	if got := w.mockDB.statusChanges[len(w.mockDB.statusChanges)-1].timestamp; got != 2 {
		t.Errorf("the status change is stamped %d, want the checker's time 2", got)
	}

	// Streamer "a" goes offline — no notifications yet (needs 5s confirmation)
	result.Streamers = map[string]cmdlib.StreamerInfo{}
	if r := w.handleCheckerResults(result, 3); len(r.notifications) != 0 {
		t.Errorf("expected 0 notifications before offline confirmation, got %d", len(r.notifications))
	}

	// Trigger confirmation check at t=8 — offline confirmed, 2 notifications
	result.Streamers = map[string]cmdlib.StreamerInfo{}
	if r := w.handleCheckerResults(result, 8); len(r.notifications) != 2 {
		t.Errorf("expected 2 notifications after offline confirmation, got %d", len(r.notifications))
	}

	// Streamer "d" goes online — 2 notifications (chat 4 on ep1 and ep2)
	result.Streamers = map[string]cmdlib.StreamerInfo{
		"d": {},
	}
	if r := w.handleCheckerResults(result, 9); len(r.notifications) != 2 {
		t.Errorf("expected 2 notifications for streamer 'd' online, got %d", len(r.notifications))
	}
}

// A same-id migrate result re-arms the notification
// instead of finalizing it as delivered: nothing was actually sent.
func TestCompleteSendResultMigrateReArms(t *testing.T) {
	t.Parallel()
	w := newTestWorker()

	w.db.AddUser(1, 3, 0, "private")
	streamerID := w.mockDB.insertStreamer(db.Streamer{Nickname: "a"}).ID
	w.db.StoreNotifications([]db.Notification{{
		Endpoint:   "test",
		UserID:     w.db.EnsureUser(1),
		StreamerID: &streamerID,
		Nickname:   "a",
		Status:     cmdlib.StatusOnline,
		Priority:   db.PriorityHigh,
		Kind:       db.NotificationPacket,
	}})
	not := w.db.NewNotifications()[0]

	w.completeSendResult(msgSendResult{
		result:         messageMigrate,
		endpoint:       "test",
		userID:         not.UserID,
		notificationID: not.ID,
		tag:            unprompted(db.NotificationPacket),
	})

	// Re-armed, not finalized: it returns on the next fetch.
	if requeued := w.db.NewNotifications(); len(requeued) != 1 || requeued[0].ID != not.ID {
		t.Errorf("migrate finalized the notification instead of re-arming it: %v", requeued)
	}
}

func TestCommandParser(t *testing.T) {
	t.Parallel()
	chatID, command, args := getCommandAndArgs(&models.Update{}, "", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{}}, "", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: "command", Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: "   ", Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: "/command", Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: " command", Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: " /command", Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: "command args", Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "args" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: "command  args", Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "args" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: "command arg1 arg2", Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "arg1 arg2" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{Text: "command@bot arg1 arg2", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "arg1 arg2" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{NewChatMembers: []models.User{}, Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{NewChatMembers: []models.User{{ID: 2}}, Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{NewChatMembers: []models.User{{ID: 2}}, Chat: models.Chat{ID: 1}}}, "", []int64{2})
	if chatID != 1 || command != "start" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{ChannelPost: &models.Message{Text: "command", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{ChannelPost: &models.Message{Text: "command@bot", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{ChannelPost: &models.Message{Text: "command @bot", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{ChannelPost: &models.Message{Text: " /command@bot", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	// A chat is a group or a channel by the sign of its id, whatever type Telegram names.
	group := models.Chat{ID: -1}
	addressed := []struct {
		name    string
		text    string
		chatID  int64
		command string
		args    string
	}{
		{"a command for another bot", "/command@otherbot", -1, "", ""},
		{"our name trailing another", "/command@otherbot@bot", -1, "", ""},
		{"our name, with arguments", "/command@bot args", -1, "command", "args"},
		{"a bare command", "/command", -1, "command", ""},
		{"chatter carrying an at", "see you@work", -1, "", ""},
		{"chatter", "hello", -1, "", ""},
	}
	for _, tc := range addressed {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			update := &models.Update{Message: &models.Message{Text: tc.text, Chat: group}}
			chatID, command, args := getCommandAndArgs(update, "@bot", nil)
			if chatID != tc.chatID || command != tc.command || args != tc.args {
				t.Errorf(
					"got (%d, %q, %q), want (%d, %q, %q)",
					chatID, command, args,
					tc.chatID, tc.command, tc.args)
			}
		})
	}
	autoForward := &models.Update{Message: &models.Message{
		Text: "/command@bot", Chat: group, IsAutomaticForward: true,
	}}
	chatID, command, args = getCommandAndArgs(autoForward, "@bot", nil)
	if chatID != -1 || command != "" || args != "" {
		t.Error("an auto-forwarded channel post must drop with its real chat and no command")
	}
}

// A send the dice skip is logged at the checker's time.
func TestCheckerResultsStampSkippedSends(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	w.initCache()
	w.mockDB.insertStreamer(db.Streamer{Nickname: "a"})
	insertSubscription(w.mockDB, "test", 100, "a")
	w.mockDB.setMaxBlock("test", 100)

	w.handleCheckerResults(&cmdlib.OnlineListResults{Streamers: map[string]cmdlib.StreamerInfo{"a": {}}}, 1000)

	if rows := w.mockDB.sentMessageLog; len(rows) != 1 || rows[0].timestamp != 1000 {
		t.Errorf("sent message log = %+v, want a row at 1000", rows)
	}
}

func TestAddStreamer(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	w.db.AddUser(1, 3, 0, "private")

	// Add streamer that doesn't exist — should insert into pending_subscriptions and return nil
	if w.addStreamer(testMessage(w, 1, "add", 100), "newmodel", false) != nil {
		t.Error("expected addStreamer to return nil when streamer is not in the database and subscription is pending verification")
	}
	if w.mockDB.pendingSubscriptionCount("newmodel") != 1 {
		t.Error("expected pending subscription for new streamer")
	}
	// Drain the "checking streamer" message
	w.nextOutgoing()

	// Add streamer that exists with online status — should return non-nil
	w.mockDB.insertStreamer(db.Streamer{Nickname: "onlinemodel", ConfirmedStatus: cmdlib.StatusOnline})
	if w.addStreamer(testMessage(w, 1, "add", 100), "onlinemodel", false) == nil {
		t.Error("expected addStreamer to return non-nil for existing streamer")
	}
	if w.mockDB.subscriptionCount("onlinemodel") != 1 {
		t.Error("expected subscription for existing streamer")
	}
	// Drain messages
	w.nextOutgoing()
	nots := w.db.NewNotifications()
	if len(nots) != 1 || nots[0].Status != cmdlib.StatusOnline {
		t.Errorf("expected online notification, got %+v", nots)
	}

	// Add streamer that exists with offline status — should return non-nil
	w.mockDB.insertStreamer(db.Streamer{Nickname: "offlinemodel", ConfirmedStatus: cmdlib.StatusOffline})
	if w.addStreamer(testMessage(w, 1, "add", 100), "offlinemodel", false) == nil {
		t.Error("expected addStreamer to return non-nil for existing offline streamer")
	}
	nots = w.db.NewNotifications()
	if len(nots) != 1 || nots[0].Status != cmdlib.StatusOffline {
		t.Errorf("expected offline notification, got %+v", nots)
	}
}

func TestProcessSubsConfirmations(t *testing.T) {
	t.Parallel()
	w := newTestWorker()

	// Insert pending subscriptions in checking state
	for chatID := int64(1); chatID <= 9; chatID++ {
		w.db.AddUser(chatID, 0, 0, "")
	}
	insertPendingSubscriptionForChat(w.mockDB, 1, "online_model", true)
	insertPendingSubscriptionForChat(w.mockDB, 2, "offline_model", true)
	insertPendingSubscriptionForChat(w.mockDB, 3, "notfound_model", true)
	insertPendingSubscriptionForChat(w.mockDB, 4, "denied_model", true)
	insertPendingSubscriptionForChat(w.mockDB, 5, "notfound_denied_model", true)
	insertPendingSubscriptionForChat(w.mockDB, 6, "online_offline_model", true)
	insertPendingSubscriptionForChat(w.mockDB, 7, "unknown_model", true)
	// Referral pending subscription — should create referral event on confirmation
	w.mockDB.insertPendingSubscription(mockPendingSubscription{
		PendingSubscription: db.PendingSubscription{
			UserID:   w.mockDB.userByChat(8).UserID,
			Nickname: "referral_model",
			Endpoint: "test",
			Referral: true,
		},
		checking: true,
	})
	// Non-referral pending subscription — should NOT create referral event
	insertPendingSubscriptionForChat(w.mockDB, 9, "nonreferral_model", true)

	// Process confirmations with checker results
	w.processSubsConfirmations(&cmdlib.ExistenceListResults{
		Streamers: map[string]cmdlib.StreamerInfoWithStatus{
			"online_model":          {Status: cmdlib.StatusOnline},
			"offline_model":         {Status: cmdlib.StatusOffline},
			"notfound_model":        {Status: cmdlib.StatusNotFound},
			"denied_model":          {Status: cmdlib.StatusDenied},
			"notfound_denied_model": {Status: cmdlib.StatusNotFound | cmdlib.StatusDenied},
			"online_offline_model":  {Status: cmdlib.StatusOnline | cmdlib.StatusOffline},
			"unknown_model":         {Status: cmdlib.StatusUnknown},
			"referral_model":        {Status: cmdlib.StatusOnline},
			"nonreferral_model":     {Status: cmdlib.StatusOnline},
		},
	})

	subCount := w.mockDB.subscriptionCount
	pendingCount := w.mockDB.pendingSubscriptionCount

	// Online model should be confirmed (in subscriptions, not in pending)
	if subCount("online_model") != 1 {
		t.Error("expected online_model to be in subscriptions")
	}
	if pendingCount("online_model") != 0 {
		t.Error("expected online_model to be removed from pending")
	}

	// Offline model should be confirmed
	if subCount("offline_model") != 1 {
		t.Error("expected offline_model to be in subscriptions")
	}

	// NotFound model should be denied (deleted from pending)
	if pendingCount("notfound_model") != 0 {
		t.Error("expected notfound_model to be deleted")
	}
	if subCount("notfound_model") != 0 {
		t.Error("expected notfound_model not in subscriptions")
	}

	// Denied model should be confirmed (StatusDenied is a valid status)
	if subCount("denied_model") != 1 {
		t.Error("expected denied_model to be in subscriptions")
	}

	// NotFound|Denied model should be confirmed (StatusDenied bit is set)
	if subCount("notfound_denied_model") != 1 {
		t.Error("expected notfound_denied_model to be in subscriptions")
	}

	// Online|Offline model should be confirmed (found but status uncertain)
	if subCount("online_offline_model") != 1 {
		t.Error("expected online_offline_model to be in subscriptions")
	}

	// Unknown model should be denied (deleted)
	if pendingCount("unknown_model") != 0 {
		t.Error("expected unknown_model to be deleted")
	}
	if subCount("unknown_model") != 0 {
		t.Error("expected unknown_model not in subscriptions")
	}

	// Referral model should be confirmed and have a referral event
	if subCount("referral_model") != 1 {
		t.Error("expected referral_model to be in subscriptions")
	}
	referralCount := w.mockDB.streamerReferralEventCount("referral_model", 8)
	if referralCount != 1 {
		t.Error("expected referral event for referral_model")
	}

	// Non-referral model should be confirmed but have no referral event
	if subCount("nonreferral_model") != 1 {
		t.Error("expected nonreferral_model to be in subscriptions")
	}
	nonReferralCount := w.mockDB.streamerReferralEventCount("nonreferral_model", 9)
	if nonReferralCount != 0 {
		t.Error("expected no referral event for nonreferral_model")
	}
}

// Mirrors the SingleStatusRequest fan-out shape: the bot pushes one
// request per pending name, each result carries one entry, and the
// other still-checking rows must be left alone for their own future
// result instead of being denied prematurely.
func TestProcessSubsConfirmationsPartialList(t *testing.T) {
	t.Parallel()
	w := newTestWorker()

	for chatID := int64(1); chatID <= 3; chatID++ {
		w.db.AddUser(chatID, 0, 0, "")
	}
	insertPendingSubscriptionForChat(w.mockDB, 1, "answered_model", true)
	insertPendingSubscriptionForChat(w.mockDB, 2, "still_pending_model", true)
	insertPendingSubscriptionForChat(w.mockDB, 3, "also_still_pending_model", true)

	w.processSubsConfirmations(&cmdlib.ExistenceListResults{
		Streamers: map[string]cmdlib.StreamerInfoWithStatus{
			"answered_model": {Status: cmdlib.StatusOnline},
		},
	})

	subCount := w.mockDB.subscriptionCount
	pendingCheckingCount := func(nickname string) int {
		return count(w.mockDB.pendingSubscriptions, func(p *mockPendingSubscription) bool {
			return p.Nickname == nickname && p.checking
		})
	}

	if subCount("answered_model") != 1 {
		t.Error("expected answered_model to be confirmed into subscriptions")
	}
	if pendingCheckingCount("answered_model") != 0 {
		t.Error("expected answered_model to be removed from pending")
	}

	// The two unanswered rows must still be checking — they belong to
	// other in-flight SingleStatusRequests, not this result.
	if pendingCheckingCount("still_pending_model") != 1 {
		t.Error("expected still_pending_model to remain in pending and checking")
	}
	if subCount("still_pending_model") != 0 {
		t.Error("expected still_pending_model not to be confirmed yet")
	}
	if pendingCheckingCount("also_still_pending_model") != 1 {
		t.Error("expected also_still_pending_model to remain in pending and checking")
	}
	if subCount("also_still_pending_model") != 0 {
		t.Error("expected also_still_pending_model not to be confirmed yet")
	}
}

func TestUserReferral(t *testing.T) {
	t.Parallel()
	t.Run("valid referral creates event and bonuses", func(t *testing.T) {
		t.Parallel()
		w := newTestWorker()

		// Create referrer and their referral link
		w.db.AddUser(20, w.cfg.MaxSubs, 0, "private")
		w.db.AddReferral(w.db.EnsureUser(20), "ref-abc")

		// New user clicks referral link
		_, created := w.db.AddUser(21, w.cfg.MaxSubs, 100, "private")
		w.start(testMessage(w, 21, "start", 100), "ref-abc", created)

		// Drain outgoing messages
		for w.sendQueue.Len() > 0 {
			w.nextOutgoing()
		}

		// Verify referral event was created with referrer
		refCount := count(w.mockDB.referralEvents, func(e mockReferralEvent) bool {
			return e.referrer != nil && w.mockDB.chatID(*e.referrer) == 20 && w.mockDB.chatID(e.follower) == 21
		})
		if refCount != 1 {
			t.Error("expected referral event")
		}

		// Verify follower got bonus subs
		follower, exists := w.db.UserByChatID(21)
		if !exists {
			t.Fatal("expected follower user to exist")
		}
		if follower.MaxSubs != w.cfg.MaxSubs+w.cfg.FollowerBonus {
			t.Errorf("expected follower max_subs %d, got %d",
				w.cfg.MaxSubs+w.cfg.FollowerBonus, follower.MaxSubs)
		}

		// Verify referrer got bonus subs
		referrer, exists := w.db.UserByChatID(20)
		if !exists {
			t.Fatal("expected referrer user to exist")
		}
		if referrer.MaxSubs != w.cfg.MaxSubs+w.cfg.ReferralBonus {
			t.Errorf("expected referrer max_subs %d, got %d",
				w.cfg.MaxSubs+w.cfg.ReferralBonus, referrer.MaxSubs)
		}

		// Verify referred_users counter incremented
		referral := w.mockDB.referrals[slices.IndexFunc(w.mockDB.referrals, func(r *mockReferral) bool {
			return w.mockDB.chatID(r.userID) == 20
		})]
		if referredUsers := referral.referredUsers; referredUsers != 1 {
			t.Errorf("expected referred_users=1, got %d", referredUsers)
		}
	})

	t.Run("invalid referral ID", func(t *testing.T) {
		t.Parallel()
		w := newTestWorker()

		_, created := w.db.AddUser(30, w.cfg.MaxSubs, 100, "private")
		w.start(testMessage(w, 30, "start", 100), "nonexistent-ref", created)

		// Drain outgoing messages
		for w.sendQueue.Len() > 0 {
			w.nextOutgoing()
		}

		// Verify no referral event
		refCount := len(w.mockDB.referralEvents)
		if refCount != 0 {
			t.Error("expected no referral event for invalid referral ID")
		}
	})

	t.Run("existing user ignores referral", func(t *testing.T) {
		t.Parallel()
		w := newTestWorker()

		// Create referrer with referral link
		w.db.AddUser(40, w.cfg.MaxSubs, 0, "private")
		w.db.AddReferral(w.db.EnsureUser(40), "ref-xyz")

		// Create follower who already exists
		w.db.AddUser(41, w.cfg.MaxSubs, 0, "private")

		_, created := w.db.AddUser(41, w.cfg.MaxSubs, 100, "private")
		w.start(testMessage(w, 41, "start", 100), "ref-xyz", created)

		// Drain outgoing messages
		for w.sendQueue.Len() > 0 {
			w.nextOutgoing()
		}

		// Verify no referral event
		refCount := len(w.mockDB.referralEvents)
		if refCount != 0 {
			t.Error("expected no referral event for existing user")
		}

		// Verify referrer max_subs unchanged
		referrer, _ := w.db.UserByChatID(40)
		if referrer.MaxSubs != w.cfg.MaxSubs {
			t.Errorf("expected referrer max_subs unchanged at %d, got %d",
				w.cfg.MaxSubs, referrer.MaxSubs)
		}
	})

	t.Run("own referral link is rejected", func(t *testing.T) {
		t.Parallel()
		w := newTestWorker()

		// Create user with referral link
		w.db.AddUser(50, w.cfg.MaxSubs, 0, "private")
		w.db.AddReferral(w.db.EnsureUser(50), "ref-own")

		_, created := w.db.AddUser(50, w.cfg.MaxSubs, 100, "private")
		w.start(testMessage(w, 50, "start", 100), "ref-own", created)

		// Drain outgoing messages
		for w.sendQueue.Len() > 0 {
			w.nextOutgoing()
		}

		// Verify no referral event
		refCount := len(w.mockDB.referralEvents)
		if refCount != 0 {
			t.Error("expected no referral event for own link")
		}
	})
}

func TestStreamerReferral(t *testing.T) {
	t.Parallel()
	t.Run("known streamer creates referral event", func(t *testing.T) {
		t.Parallel()
		w := newTestWorker()

		// Pre-create a known streamer
		w.mockDB.insertStreamer(db.Streamer{Nickname: "known_model", ConfirmedStatus: cmdlib.StatusOnline})

		_, created := w.db.AddUser(10, w.cfg.MaxSubs, 100, "private")
		w.start(testMessage(w, 10, "start", 100), "m-known_model", created)

		// Drain outgoing messages
		for w.sendQueue.Len() > 0 {
			w.nextOutgoing()
		}

		// Verify subscription was created
		subCount := count(w.mockDB.subscriptions, func(s mockSubscription) bool {
			return w.mockDB.streamer(s.streamerID).Nickname == "known_model" && w.mockDB.chatID(s.userID) == 10
		})
		if subCount != 1 {
			t.Error("expected subscription for known_model")
		}

		// Verify referral event was created
		refCount := w.mockDB.streamerReferralEventCount("known_model", 10)
		if refCount != 1 {
			t.Error("expected referral event for known_model")
		}
	})

	t.Run("unknown streamer defers referral to pending", func(t *testing.T) {
		t.Parallel()
		w := newTestWorker()

		_, created := w.db.AddUser(11, w.cfg.MaxSubs, 100, "private")
		w.start(testMessage(w, 11, "start", 100), "m-unknown_model", created)

		// Drain outgoing messages
		for w.sendQueue.Len() > 0 {
			w.nextOutgoing()
		}

		// Verify pending subscription was created with referral flag
		pendingIndex := slices.IndexFunc(w.mockDB.pendingSubscriptions, func(p *mockPendingSubscription) bool {
			return p.Nickname == "unknown_model" && w.mockDB.chatID(p.UserID) == 11
		})
		pending := w.mockDB.pendingSubscriptions[pendingIndex]
		if !pending.Referral {
			t.Error("expected pending subscription with referral=true")
		}

		// Verify no referral event yet
		refCount := w.mockDB.streamerReferralEventCount("unknown_model", 11)
		if refCount != 0 {
			t.Error("expected no referral event for unknown streamer")
		}
	})
}

func TestHandleStatusUpdates(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	w.initCache()

	// Insert a subscription
	w.mockDB.insertStreamer(db.Streamer{Nickname: "a"})
	insertSubscription(w.mockDB, "test", 1, "a")

	// Test with OnlineListResults
	result := &cmdlib.OnlineListResults{
		Streamers: map[string]cmdlib.StreamerInfo{"a": {ImageURL: "http://a.jpg"}},
	}
	r := w.handleCheckerResults(result, 100)
	if r.unconfirmedChangesCount != 1 {
		t.Errorf("expected 1 change with OnlineListResults, got %d", r.unconfirmedChangesCount)
	}
	if w.unconfirmedOnlineStreamers["a"].ImageURL != "http://a.jpg" {
		t.Errorf("expected ImageURL to be set, got %s", w.unconfirmedOnlineStreamers["a"].ImageURL)
	}
	checkUnconfirmedOnlineStreamers(w, t)

	// Test ImageURL update for streamer that remains online
	result.Streamers["a"] = cmdlib.StreamerInfo{ImageURL: "http://a2.jpg"}
	w.handleCheckerResults(result, 101)
	if w.unconfirmedOnlineStreamers["a"].ImageURL != "http://a2.jpg" {
		t.Errorf("expected ImageURL to be updated, got %s", w.unconfirmedOnlineStreamers["a"].ImageURL)
	}
	checkUnconfirmedOnlineStreamers(w, t)

	// Test with FixedListOnlineResults — streamer goes offline (not in Streamers)
	result2 := &cmdlib.FixedListOnlineResults{
		RequestedStreamers: map[string]bool{"a": true},
		Streamers:          map[string]cmdlib.StreamerInfo{}, // empty = "a" is offline
	}
	r = w.handleCheckerResults(result2, 102)
	if r.unconfirmedChangesCount != 1 {
		t.Errorf("expected 1 change with FixedListOnlineResults, got %d", r.unconfirmedChangesCount)
	}
	if _, ok := w.unconfirmedOnlineStreamers["a"]; ok {
		t.Error("expected offline streamer to be removed from unconfirmedOnlineStreamers")
	}
	checkUnconfirmedOnlineStreamers(w, t)

	// Streamer comes back online (use new map to avoid aliasing with unconfirmedOnlineStreamers)
	result2.Streamers = map[string]cmdlib.StreamerInfo{"a": {ImageURL: "http://a3.jpg"}}
	w.handleCheckerResults(result2, 103)
	checkUnconfirmedOnlineStreamers(w, t)

	// Test error case (should return early with zero values)
	result3 := cmdlib.NewOnlineListResultsFailed()
	r = w.handleCheckerResults(result3, 105)
	if r.unconfirmedChangesCount != 0 || r.confirmedChangesCount != 0 || len(r.notifications) != 0 || r.elapsed != 0 {
		t.Errorf(
			"expected zero values on error, got changes=%d, confirmedChanges=%d, nots=%d, elapsed=%d",
			r.unconfirmedChangesCount, r.confirmedChangesCount, len(r.notifications), r.elapsed)
	}

	// Test error case with FixedListResults
	result4 := cmdlib.NewFixedListOnlineResultsFailed()
	r = w.handleCheckerResults(result4, 106)
	if r.unconfirmedChangesCount != 0 || r.confirmedChangesCount != 0 || len(r.notifications) != 0 || r.elapsed != 0 {
		t.Errorf(
			"expected zero values on error with FixedListResults, got changes=%d, confirmedChanges=%d, nots=%d, elapsed=%d",
			r.unconfirmedChangesCount, r.confirmedChangesCount, len(r.notifications), r.elapsed)
	}
}

func TestStatusTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		subscribed    bool
		dbBefore      *cmdlib.StatusKind // nil means streamer doesn't exist in DB
		fixedList     bool
		checkerStatus *cmdlib.StatusKind // nil means streamer not in checker result
		dbAfter       cmdlib.StatusKind
	}{
		// Fixed list checker tests
		{
			name:          "fixed list: unknown to online",
			subscribed:    true,
			dbBefore:      nil,
			fixedList:     true,
			checkerStatus: new(cmdlib.StatusOnline),
			dbAfter:       cmdlib.StatusOnline,
		},
		{
			name:          "fixed list: online to offline",
			subscribed:    true,
			dbBefore:      new(cmdlib.StatusOnline),
			fixedList:     true,
			checkerStatus: new(cmdlib.StatusOffline),
			dbAfter:       cmdlib.StatusOffline,
		},
		{
			name:          "fixed list: offline to online",
			subscribed:    true,
			dbBefore:      new(cmdlib.StatusOffline),
			fixedList:     true,
			checkerStatus: new(cmdlib.StatusOnline),
			dbAfter:       cmdlib.StatusOnline,
		},
		{
			name:          "fixed list: online to unknown (unsubscribe)",
			subscribed:    false,
			dbBefore:      new(cmdlib.StatusOnline),
			fixedList:     true,
			checkerStatus: nil, // not in result because unsubscribed
			dbAfter:       cmdlib.StatusUnknown,
		},
		{
			name:          "fixed list: unknown to offline",
			subscribed:    true,
			dbBefore:      nil,
			fixedList:     true,
			checkerStatus: new(cmdlib.StatusOffline),
			dbAfter:       cmdlib.StatusOffline,
		},
		{
			name:          "fixed list: offline to unknown (unsubscribe)",
			subscribed:    false,
			dbBefore:      new(cmdlib.StatusOffline),
			fixedList:     true,
			checkerStatus: nil, // not in result because unsubscribed
			dbAfter:       cmdlib.StatusUnknown,
		},
		// Online list checker tests
		{
			name:          "online list: unknown to online",
			subscribed:    true,
			dbBefore:      nil,
			fixedList:     false,
			checkerStatus: new(cmdlib.StatusOnline),
			dbAfter:       cmdlib.StatusOnline,
		},
		{
			name:          "online list: online to offline (missing from result)",
			subscribed:    true,
			dbBefore:      new(cmdlib.StatusOnline),
			fixedList:     false,
			checkerStatus: nil,
			dbAfter:       cmdlib.StatusOffline,
		},
		// Unsubscribed streamer tests
		{
			name:          "online list: unsubscribed streamer stays online",
			subscribed:    false,
			dbBefore:      nil,
			fixedList:     false,
			checkerStatus: new(cmdlib.StatusOnline),
			dbAfter:       cmdlib.StatusOnline,
		},
		{
			name:          "fixed list: unsubscribed streamer stays online",
			subscribed:    false,
			dbBefore:      nil,
			fixedList:     true,
			checkerStatus: new(cmdlib.StatusOnline),
			dbAfter:       cmdlib.StatusOnline,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			// Set up background streamers that should remain unchanged
			w.mockDB.insertStreamer(db.Streamer{
				Nickname:             "always_online",
				UnconfirmedStatus:    cmdlib.StatusOnline,
				UnconfirmedTimestamp: 1,
			})
			w.mockDB.insertStreamer(db.Streamer{
				Nickname:             "always_offline",
				UnconfirmedStatus:    cmdlib.StatusOffline,
				UnconfirmedTimestamp: 1,
			})
			insertSubscription(w.mockDB, "ep", 1, "always_online")
			insertSubscription(w.mockDB, "ep", 1, "always_offline")

			// Initialize cache after setting up background streamers
			w.initCache()

			// Always subscribe during setup if we need to set initial state
			// (subscription is needed to track the streamer)
			if tt.dbBefore != nil || tt.subscribed {
				w.mockDB.insertStreamer(db.Streamer{Nickname: "ch"})
				insertSubscription(w.mockDB, "ep", 1, "ch")
			}

			// Include background streamers in RequestedStreamers to prevent them from being set to unknown
			bgStreamers := map[string]bool{"always_online": true, "always_offline": true}

			if tt.dbBefore != nil {
				if tt.fixedList {
					setupResult := &cmdlib.FixedListOnlineResults{
						RequestedStreamers: map[string]bool{"ch": true, "always_online": true, "always_offline": true},
						Streamers:          map[string]cmdlib.StreamerInfo{"always_online": {}},
					}
					if *tt.dbBefore == cmdlib.StatusOnline {
						setupResult.Streamers["ch"] = cmdlib.StreamerInfo{}
					}
					w.handleCheckerResults(setupResult, 100)
				} else {
					setupResult := &cmdlib.OnlineListResults{
						Streamers: map[string]cmdlib.StreamerInfo{"always_online": {}},
					}
					if *tt.dbBefore == cmdlib.StatusOnline {
						setupResult.Streamers["ch"] = cmdlib.StreamerInfo{}
					}
					w.handleCheckerResults(setupResult, 100)
				}
			}

			if tt.fixedList {
				result := &cmdlib.FixedListOnlineResults{
					RequestedStreamers: bgStreamers,
					Streamers:          map[string]cmdlib.StreamerInfo{"always_online": {}},
				}
				if tt.subscribed {
					result.RequestedStreamers["ch"] = true
				}
				if tt.checkerStatus != nil && *tt.checkerStatus == cmdlib.StatusOnline {
					result.Streamers["ch"] = cmdlib.StreamerInfo{}
				}
				w.handleCheckerResults(result, 101)
			} else {
				result := &cmdlib.OnlineListResults{
					Streamers: map[string]cmdlib.StreamerInfo{"always_online": {}},
				}
				if tt.checkerStatus != nil && *tt.checkerStatus == cmdlib.StatusOnline {
					result.Streamers["ch"] = cmdlib.StreamerInfo{}
				}
				w.handleCheckerResults(result, 101)
			}

			streamer := w.db.MaybeStreamer("ch")
			if streamer == nil {
				t.Errorf("expected streamer in DB with status %v, got nil", tt.dbAfter)
			} else if streamer.UnconfirmedStatus != tt.dbAfter {
				t.Errorf("expected status %v, got %v", tt.dbAfter, streamer.UnconfirmedStatus)
			}

			// Verify background streamers were not affected
			if ch := w.db.MaybeStreamer("always_online"); ch == nil || ch.UnconfirmedStatus != cmdlib.StatusOnline {
				t.Errorf("always_online was affected, got %v", ch)
			}
			if ch := w.db.MaybeStreamer("always_offline"); ch == nil || ch.UnconfirmedStatus != cmdlib.StatusOffline {
				t.Errorf("always_offline was affected, got %v", ch)
			}
		})
	}
}

func TestNotifyOfStatuses(t *testing.T) {
	t.Parallel()
	w := newTestWorker()

	nots := []db.Notification{
		{UserID: 1, Endpoint: "test", Nickname: "a", Status: cmdlib.StatusOnline, Priority: db.PriorityLow},
		{UserID: 2, Endpoint: "test", Nickname: "b", Status: cmdlib.StatusOnline, Priority: db.PriorityHigh},
		{UserID: 3, Endpoint: "test", Nickname: "c", Status: cmdlib.StatusOnline, Priority: db.PriorityLow},
		{UserID: 4, Endpoint: "test", Nickname: "d", Status: cmdlib.StatusOnline, Priority: db.PriorityHigh},
	}

	w.enqueueNotifications(nots)

	if w.sendQueue.Len() != 4 {
		t.Errorf("expected 4 outgoing messages, got %d", w.sendQueue.Len())
	}
	lowCount := 0
	highCount := 0
	for range w.sendQueue.Len() {
		p := w.nextOutgoing()
		if p.order.priority == db.PriorityLow {
			lowCount++
		} else {
			highCount++
		}
	}
	if lowCount != 2 {
		t.Errorf("expected 2 low priority messages, got %d", lowCount)
	}
	if highCount != 2 {
		t.Errorf("expected 2 high priority messages, got %d", highCount)
	}
}

func TestDeliverOffMainGoroutine(t *testing.T) {
	t.Parallel()
	w := newTestWorker()

	w.db.AddUser(100, 3, 0, "private")
	userID := w.db.EnsureUser(100)

	// deliver runs off the main goroutine and must not touch the database.
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		msg := &migrateThenOK{id: 100, target: 200}
		w.deliver(&queuedMessage{
			userID:   userID,
			endpoint: "test",
			message:  msg,
			order:    queueOrder{priority: db.PriorityHigh},
			tag:      unprompted(db.MessagePacket),
		}, msg)
	}()
	if r := <-done; r != nil {
		t.Fatalf("deliver touched the database off the main goroutine: %v", r)
	}
}

// TestAttemptBlockedSend checks the dice gate on sends to a blocking chat:
// certainty up to the threshold, both outcomes past it.
func TestAttemptBlockedSend(t *testing.T) {
	t.Parallel()
	const threshold = 15
	tests := []struct {
		name    string
		blocked int
		// wantAlways asserts every roll attempts; otherwise both outcomes must occur.
		wantAlways bool
	}{
		{name: "zero", blocked: 0, wantAlways: true},
		{name: "below threshold", blocked: threshold - 1, wantAlways: true},
		{name: "at threshold", blocked: threshold, wantAlways: true},
		{name: "just past threshold", blocked: threshold + 1},
		{name: "far past threshold", blocked: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var sent, skipped int
			for range 5000 {
				if attemptBlockedSend(tt.blocked, threshold) {
					sent++
				} else {
					skipped++
				}
			}
			if tt.wantAlways {
				if skipped != 0 {
					t.Errorf("blocked = %d skipped %d of 5000 sends, want none", tt.blocked, skipped)
				}
			} else if sent == 0 || skipped == 0 {
				t.Errorf("blocked = %d gave sent = %d, skipped = %d, want both outcomes", tt.blocked, sent, skipped)
			}
		})
	}
}

// TestBuildNotificationsDiceGate checks notification creation rolls the dice:
// a subscriber past the threshold is dropped and logged as dice-skipped,
// the rest get their notifications,
// and an orphaned endpoint's subscriber neither rolls nor logs,
// since their notification is dropped unsent later anyway.
func TestBuildNotificationsDiceGate(t *testing.T) {
	t.Parallel()
	w := newTestWorker()

	streamerID := w.mockDB.insertStreamer(db.Streamer{Nickname: "s1"}).ID
	insertSubscription(w.mockDB, "test", 100, "s1")
	insertSubscription(w.mockDB, "test", 101, "s1")
	// An endpoint absent from the config.
	insertSubscription(w.mockDB, "ghost", 102, "s1")
	w.mockDB.setMaxBlock("test", 100)
	w.mockDB.setMaxBlock("ghost", 102)

	nots := w.buildNotifications([]db.ConfirmedStatusChange{
		{StreamerID: streamerID, Nickname: "s1", Status: cmdlib.StatusOnline},
	}, 1000)

	got := map[db.UserID]bool{}
	for _, n := range nots {
		got[n.UserID] = true
	}
	want := map[db.UserID]bool{w.db.EnsureUser(101): true, w.db.EnsureUser(102): true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notified users = %v, want %v (no roll for the orphaned endpoint)", got, want)
	}
	skipRows := func(chatID int64) []mockSentMessageLog {
		var rows []mockSentMessageLog
		for _, l := range w.mockDB.sentMessageLog {
			if l.result == messageDiceSkipped && w.mockDB.chatID(l.userID) == chatID {
				rows = append(rows, l)
			}
		}
		return rows
	}
	if n := len(skipRows(100)); n != 1 {
		t.Errorf("dice-skip log rows for the blocked subscriber = %d, want 1", n)
	}
	if n := len(skipRows(102)); n != 0 {
		t.Errorf("dice-skip log rows for the orphaned endpoint = %d, want 0", n)
	}
	stamp := skipRows(100)[0].timestamp
	if stamp != 1000 {
		t.Errorf("dice-skip log timestamp = %d, want the batch's 1000", stamp)
	}
}

// TestBroadcastDiceGate checks broadcast rolls the dice per recipient:
// a lost roll is logged as a dice-skipped MessagePacket and nothing is enqueued for it.
func TestBroadcastDiceGate(t *testing.T) {
	t.Parallel()
	w := newTestWorker()

	w.mockDB.insertStreamer(db.Streamer{Nickname: "s1"})
	insertSubscription(w.mockDB, "test", 100, "s1")
	insertSubscription(w.mockDB, "test", 101, "s1")
	w.ownerUserID = w.db.EnsureUser(999)
	w.mockDB.setMaxBlock("test", 100)

	w.broadcast("test", "hi")

	// The slot is held (newTestWorker), so the queue keeps what broadcast enqueued:
	// the winner's copy and the owner's ack, never the loser's.
	if got := w.sendQueue.Len(); got != 2 {
		t.Errorf("queued messages = %d, want the winner's copy and the ack", got)
	}
	skipLogged := count(w.mockDB.sentMessageLog, func(l mockSentMessageLog) bool {
		return l.result == messageDiceSkipped && l.kind == db.MessagePacket && w.mockDB.chatID(l.userID) == 100
	})
	if skipLogged != 1 {
		t.Errorf("dice-skip log rows = %d, want 1", skipLogged)
	}
}
