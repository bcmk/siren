package db

import (
	"fmt"
	"testing"
)

// TestChatsHoldPerBotSettings checks a user's chats on different endpoints stay apart
func TestChatsHoldPerBotSettings(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	d := tdb.Database

	userID, _ := d.insertUser(d.db, 1, 1000, "private")
	d.addChat(d.db, "ru", userID)
	if !d.SetLimit("ru", userID, 8) {
		t.Error("the limit of an existing chat reported as missing")
	}
	if d.SetLimit("missing", userID, 8) {
		t.Error("the limit of a missing chat reported as set")
	}

	// A write on en changes en's chat and leaves ru's, seeded alike, untouched
	streamerID := d.MustInt(`insert into streamers (nickname) values ('s') returning id`)
	pay := func(userID UserID) {
		chatID, _ := d.ChatIDForUser(userID)
		d.GrantStarPaymentSubs(chatID, "en", fmt.Sprint("charge", chatID), 100, "subs", 10, "payload", 1000)
	}
	writes := []struct {
		name  string
		seed  func(userID UserID, endpoint string)
		write func(userID UserID)
		// stateQuery reads chat $1's state as an integer
		stateQuery string
	}{
		{
			name:       "add_subscription",
			write:      func(userID UserID) { d.AddSubscription(userID, streamerID, "en") },
			stateQuery: `select count(*) from subscriptions where chat_id = $1`,
		},
		{
			name:       "remove_subscription",
			seed:       func(userID UserID, endpoint string) { d.AddSubscription(userID, streamerID, endpoint) },
			write:      func(userID UserID) { d.RemoveSubscription(userID, "s", "en") },
			stateQuery: `select count(*) from subscriptions where chat_id = $1`,
		},
		{
			name:       "add_pending_subscription",
			write:      func(userID UserID) { d.AddPendingSubscription(userID, "p", "en", false, "", 0) },
			stateQuery: `select count(*) from pending_subscriptions where chat_id = $1`,
		},
		{
			name: "delete_pending_subscription",
			seed: func(userID UserID, endpoint string) {
				d.AddPendingSubscription(userID, "p", endpoint, false, "", 0)
			},
			write:      func(userID UserID) { d.RemoveSubscription(userID, "p", "en") },
			stateQuery: `select count(*) from pending_subscriptions where chat_id = $1`,
		},
		{
			name:       "grant_star_payment_subs_record_payment",
			write:      pay,
			stateQuery: `select count(*) from star_payments where chat_id = $1`,
		},
		{
			name:       "grant_star_payment_subs_add_subs",
			write:      pay,
			stateQuery: `select max_subs from chats where id = $1`,
		},
		{
			name:       "add_feedback",
			write:      func(userID UserID) { d.AddFeedback("en", userID, "hi", 1000) },
			stateQuery: `select count(*) from feedback where chat_id = $1`,
		},
		{
			name: "log_sent_message",
			write: func(userID UserID) {
				d.LogSentMessage(1000, userID, 0, "en", PriorityHigh, 0, ReplyPacket, "", 0, NoImage)
			},
			stateQuery: `select count(*) from sent_message_log where chat_id = $1`,
		},
		{
			name:       "log_received_message",
			write:      func(userID UserID) { d.LogReceivedMessage(1000, "en", userID, "") },
			stateQuery: `select count(*) from received_message_log where chat_id = $1`,
		},
		{
			name: "log_dice_skipped_sends",
			write: func(userID UserID) {
				d.LogDiceSkippedSends(
					1000,
					0,
					PriorityLow,
					NotificationPacket,
					[]SkippedSend{{UserID: userID, Endpoint: "en"}})
			},
			stateQuery: `select count(*) from sent_message_log where chat_id = $1`,
		},
		{
			name:       "increment_block",
			write:      func(userID UserID) { d.IncrementBlock("en", userID) },
			stateQuery: `select block from chats where id = $1`,
		},
	}
	for i, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			userID, _ := d.insertUser(d.db, int64(100+i), 1000, "private")
			// ru first, so a statement taking the user's first chat misses en
			d.addChat(d.db, "ru", userID)
			d.addChat(d.db, "en", userID)
			if w.seed != nil {
				w.seed(userID, "en")
				w.seed(userID, "ru")
			}
			state := func(endpoint string) int {
				id := d.MustInt(
					`select id from chats where user_id = $1 and endpoint = $2`,
					int64(userID),
					endpoint)
				return d.MustInt(w.stateQuery, id)
			}
			en, ru := state("en"), state("ru")
			w.write(userID)
			if got := state("en"); got == en {
				t.Errorf("en's chat stayed at %d", got)
			}
			if got := state("ru"); got != ru {
				t.Errorf("ru's chat went from %d to %d", ru, got)
			}
		})
	}
}

// A dice-skipped send for a user with no chat on its endpoint fails rather than vanishing
func TestLogDiceSkippedSendsNeedsTheChat(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	d := tdb.Database

	userID, _ := d.insertUser(d.db, 1, 1000, "private")
	defer func() {
		if recover() == nil {
			t.Error("a skipped send with no chat did not fail")
		}
	}()
	d.LogDiceSkippedSends(1000, 0, PriorityLow, NotificationPacket, []SkippedSend{{UserID: userID, Endpoint: "en"}})
}
