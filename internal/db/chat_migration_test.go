package db

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func (d *Database) ensureStreamer(nickname string) {
	d.MustExec(
		"insert into streamers (nickname) values ($1) on conflict (nickname) do nothing",
		nickname)
}

// addTestChat inserts a chat directly, with the given limit
func (d *Database) addTestChat(chatID int64, endpoint string, maxSubs int) {
	d.MustExec(`
		insert into chats (user_id, endpoint, max_subs)
		select u.id, $2, $3 from users u where u.tg_chat_id = $1
		on conflict do nothing`,
		chatID, endpoint, maxSubs)
}

// addSub inserts a subscription directly, creating the streamer and the chat if needed.
func (d *Database) addSub(chatID int64, nickname, endpoint string) {
	d.ensureStreamer(nickname)
	d.addTestChat(chatID, endpoint, 3)
	d.MustExec(`
		insert into subscriptions (chat_id, streamer_id)
		select c.id, s.id
		from users u
		join chats c on c.user_id = u.id
		cross join streamers s
		where u.tg_chat_id = $1 and c.endpoint = $3 and s.nickname = $2`,
		chatID, nickname, endpoint)
}

func (d *Database) addPending(chatID int64, nickname, endpoint string) {
	d.addTestChat(chatID, endpoint, 3)
	d.MustExec(`
		insert into pending_subscriptions (chat_id, nickname)
		select c.id, $2
		from users u
		join chats c on c.user_id = u.id
		where u.tg_chat_id = $1 and c.endpoint = $3`,
		chatID, nickname, endpoint)
}

func (d *Database) setBlock(chatID int64, endpoint string, count int) {
	d.addTestChat(chatID, endpoint, 3)
	d.MustExec(`
		update chats c set block = $3
		from users u
		where u.id = c.user_id and u.tg_chat_id = $1 and c.endpoint = $2`,
		chatID, endpoint, count)
}

func (d *Database) addNotification(chatID int64, nickname, endpoint string) {
	d.ensureStreamer(nickname)
	d.addTestChat(chatID, endpoint, 3)
	d.MustExec(`
		insert into notification_queue (chat_id, status, streamer_id)
		select c.id, 0, s.id
		from users u
		join chats c on c.user_id = u.id
		cross join streamers s
		where u.tg_chat_id = $1 and c.endpoint = $3 and s.nickname = $2`,
		chatID, nickname, endpoint)
}

// markNotificationSending flags a chat's queued notification as in-flight.
func (d *Database) markNotificationSending(chatID int64, nickname string) {
	d.MustExec(`
		update notification_queue nq set sending = 1
		from chats c, users u, streamers s
		where c.id = nq.chat_id and u.id = c.user_id and nq.streamer_id = s.id
		and u.tg_chat_id = $1 and s.nickname = $2`,
		chatID, nickname)
}

func (d *Database) subNicknames(chatID int64, endpoint string) []string {
	return d.MustStrings(`
		select s.nickname
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		join chats c on c.id = sub.chat_id
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1 and c.endpoint = $2
		order by s.nickname`,
		chatID, endpoint)
}

func (d *Database) pendingNicknames(chatID int64, endpoint string) []string {
	return d.MustStrings(`
		select ps.nickname
		from pending_subscriptions ps
		join chats c on c.id = ps.chat_id
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1 and c.endpoint = $2
		order by ps.nickname`,
		chatID, endpoint)
}

func (d *Database) notificationCount(chatID int64) int {
	return d.MustInt(`
		select count(*) from notification_queue nq
		join chats c on c.id = nq.chat_id
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1`,
		chatID)
}

func (d *Database) setReports(chatID int64, endpoint string, reports int) {
	d.MustExec(`
		update chats c set reports = $3
		from users u
		where u.id = c.user_id and u.tg_chat_id = $1 and c.endpoint = $2`,
		chatID, endpoint, reports)
}

func (d *Database) chatOf(chatID int64, endpoint string) (Chat, bool) {
	return d.Chat(endpoint, d.userID(chatID))
}

func (d *Database) greeted(chatID int64, endpoint string) bool {
	return d.MustBool(`
		select c.greeted from chats c
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1 and c.endpoint = $2`,
		chatID, endpoint)
}

// userID returns a Telegram chat's user id without creating the user.
func (d *Database) userID(chatID int64) UserID {
	var id int64
	d.MaybeRecord("select id from users where tg_chat_id = $1", QueryParams{chatID}, ScanTo{&id})
	return UserID(id)
}

func (d *Database) feedbackCount(chatID int64) int {
	return d.MustInt(`
		select count(*) from feedback f
		join chats c on c.id = f.chat_id
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1`,
		chatID)
}

func (d *Database) paymentCount(chatID int64) int {
	return d.MustInt(`
		select count(*) from star_payments p
		join chats c on c.id = p.chat_id
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1`,
		chatID)
}

func (d *Database) referrerEventCount(chatID int64) int {
	return d.MustInt(`
		select count(*) from referral_events r
		join users u on u.id = r.referrer_user_id
		where u.tg_chat_id = $1`,
		chatID)
}

// migratedToChat returns the tg_chat_id of the user that chatID's user merged into.
func (d *Database) migratedToChat(chatID int64) (int64, bool) {
	var c int64
	found := d.MaybeRecord(`
		select d.tg_chat_id from users s
		join users d on d.id = s.migrated_to
		where s.tg_chat_id = $1`,
		QueryParams{chatID},
		ScanTo{&c})
	return c, found
}

func (d *Database) followerEventCount(chatID int64) int {
	return d.MustInt(`
		select count(*) from referral_events r
		join users u on u.id = r.follower_user_id
		where u.tg_chat_id = $1`,
		chatID)
}

// feedbackCountOn counts the feedback on chatID's chat on endpoint
func (d *Database) feedbackCountOn(chatID int64, endpoint string) int {
	return d.MustInt(`
		select count(*) from feedback f
		join chats c on c.id = f.chat_id
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1 and c.endpoint = $2`,
		chatID, endpoint)
}

// paymentCountOn counts the payments on chatID's chat on endpoint
func (d *Database) paymentCountOn(chatID int64, endpoint string) int {
	return d.MustInt(`
		select count(*) from star_payments p
		join chats c on c.id = p.chat_id
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1 and c.endpoint = $2`,
		chatID, endpoint)
}

// reportsOn reads the reports count of chatID's chat on endpoint
func (d *Database) reportsOn(chatID int64, endpoint string) int {
	return d.MustInt(`
		select c.reports from chats c
		join users u on u.id = c.user_id
		where u.tg_chat_id = $1 and c.endpoint = $2`,
		chatID, endpoint)
}

func TestMigrateChat(t *testing.T) {
	t.Parallel()
	const ep = "ep"

	t.Run("destination absent renames the user", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-100), int64(-100500)
		d.insertUser(d.db, oldID, 1000, "group")
		d.addTestChat(oldID, ep, 5)
		d.addSub(oldID, "alice", ep)
		d.addSub(oldID, "bob", ep)
		d.addPending(oldID, "pending1", ep)
		d.setBlock(oldID, ep, 3)
		d.addNotification(oldID, "alice", ep)
		d.addNotification(oldID, "bob", ep)
		d.SetReferralID(ep, d.userID(oldID), "refOld")

		d.MigrateChat(oldID, newID)

		if _, found := d.UserByChatID(oldID); found {
			t.Error("source user still present after migration")
		}
		user, found := d.UserByChatID(newID)
		if !found {
			t.Fatal("destination user missing after migration")
		}
		if chat, _ := d.chatOf(newID, ep); chat.MaxSubs != 5 {
			t.Errorf("max_subs = %d, want 5", chat.MaxSubs)
		}
		if user.ChatType == nil || *user.ChatType != "supergroup" {
			t.Errorf("chat_type = %v, want supergroup", user.ChatType)
		}

		if got, want := d.subNicknames(newID, ep), []string{"alice", "bob"}; !slices.Equal(got, want) {
			t.Errorf("destination subscriptions = %v, want %v", got, want)
		}
		if got := d.subNicknames(oldID, ep); len(got) != 0 {
			t.Errorf("source still has subscriptions %v", got)
		}

		if got, want := d.pendingNicknames(newID, ep), []string{"pending1"}; !slices.Equal(got, want) {
			t.Errorf("destination pending = %v, want %v", got, want)
		}
		if got := d.pendingNicknames(oldID, ep); len(got) != 0 {
			t.Errorf("source still has pending %v", got)
		}

		if chat, _ := d.chatOf(newID, ep); chat.Blocked != 3 {
			t.Errorf("destination block = %d, want 3", chat.Blocked)
		}

		if got := d.notificationCount(newID); got != 2 {
			t.Errorf("destination notifications = %d, want 2", got)
		}
		if got := d.notificationCount(oldID); got != 0 {
			t.Errorf("source still has %d notifications", got)
		}

		// The referral id follows the user, so its link now credits the new ID.
		if chat, _ := d.chatOf(newID, ep); chat.ReferralID == nil || *chat.ReferralID != "refOld" {
			t.Errorf("destination referral_id = %s, want refOld", asJSON(chat.ReferralID))
		}
		if owner := d.UserByReferralID(ep, "refOld"); owner == nil || *owner != d.userID(newID) {
			t.Errorf("refOld resolves to %v, want the user for %d", owner, newID)
		}
	})

	t.Run("destination present merges the source's rows", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-200), int64(-200500)
		// The source's subscriptions, pending ones and idle alerts are dropped.
		// The destination's limit is larger on ep, the source's on ep3; the merge takes the larger.
		d.insertUser(d.db, oldID, 1000, "group")
		d.addTestChat(oldID, ep, 5)
		d.setReports(oldID, ep, 4)
		d.BlacklistUser(d.userID(oldID))
		d.addSub(oldID, "alice", ep)
		d.addSub(oldID, "carol", ep)
		d.addPending(oldID, "pendingOld", ep)
		d.addPending(oldID, "pendingNew", ep)
		d.setBlock(oldID, "ep2", 4)
		d.setReports(oldID, "ep2", 6)
		d.addTestChat(oldID, "ep3", 50)
		d.addNotification(oldID, "alice", ep)
		d.addNotification(oldID, "carol", ep)
		d.SetReferralID(ep, d.userID(oldID), "refOld")
		d.SetReferralID("ep2", d.userID(oldID), "refOld2")

		d.insertUser(d.db, newID, 2000, "supergroup")
		d.addTestChat(newID, ep, 20)
		d.setReports(newID, ep, 1)
		d.addSub(newID, "carol", ep)
		d.addPending(newID, "pendingNew", ep)
		d.setBlock(newID, ep, 7)
		d.addTestChat(newID, "ep3", 3)
		d.addNotification(newID, "carol", ep)
		d.SetReferralID(ep, d.userID(newID), "refNew")

		d.MigrateChat(oldID, newID)

		// The source is kept as a tombstone, linked to the destination.
		if _, found := d.UserByChatID(oldID); !found {
			t.Error("source tombstone removed")
		}
		if to, found := d.migratedToChat(oldID); !found || to != newID {
			t.Errorf("source migrated_to chat = (%d, %v), want (%d, true)", to, found, newID)
		}
		user, found := d.UserByChatID(newID)
		if !found {
			t.Fatal("destination user missing")
		}
		// The limit is the larger of the two and never regresses to the source's.
		chat, _ := d.chatOf(newID, ep)
		if chat.MaxSubs != 20 {
			t.Errorf("max_subs = %d, want 20 (max of 5 and 20)", chat.MaxSubs)
		}
		if got := d.reportsOn(newID, ep); got != 1 {
			t.Errorf("reports = %d, want 1 (destination's)", got)
		}
		if chat, _ := d.chatOf(newID, "ep3"); chat.MaxSubs != 50 {
			t.Errorf("max_subs on ep3 = %d, want 50 (the source's, the larger)", chat.MaxSubs)
		}
		if user.Blacklist {
			t.Error("blacklist = true, want false (destination's)")
		}

		if got, want := d.subNicknames(newID, ep), []string{"carol"}; !slices.Equal(got, want) {
			t.Errorf("subscriptions = %v, want %v (the destination's)", got, want)
		}
		if got, want := d.pendingNicknames(newID, ep), []string{"pendingNew"}; !slices.Equal(got, want) {
			t.Errorf("pending = %v, want %v (the destination's)", got, want)
		}

		// The destination's block survives; a chat created for it takes the source's counts.
		if chat.Blocked != 7 {
			t.Errorf("destination block on %q = %d, want 7", ep, chat.Blocked)
		}
		if chat, found := d.chatOf(newID, "ep2"); !found || chat.Blocked != 4 || d.reportsOn(newID, "ep2") != 6 {
			t.Errorf("destination chat on ep2 = (%+v, %v), want a new one with block 4 and reports 6", chat, found)
		}
		if _, found := d.chatOf(oldID, "ep2"); !found {
			t.Error("the source lost its ep2 chat, which holds its message logs")
		}

		if got := d.notificationCount(newID); got != 1 {
			t.Errorf("notifications = %d, want 1 (the destination's)", got)
		}

		// The destination's chat keeps its own referral id, and the source's is dropped.
		if chat.ReferralID == nil || *chat.ReferralID != "refNew" {
			t.Errorf("destination referral id = %s, want refNew", asJSON(chat.ReferralID))
		}
		if owner := d.UserByReferralID(ep, "refOld"); owner != nil {
			t.Errorf("dropped link refOld still resolves to %d", *owner)
		}
		// A chat created for the destination takes the source's id.
		if chat, _ := d.chatOf(newID, "ep2"); chat.ReferralID == nil || *chat.ReferralID != "refOld2" {
			t.Errorf("destination referral id on ep2 = %s, want refOld2", asJSON(chat.ReferralID))
		}

		// The source's operational rows are gone; the tombstone keeps its chats.
		for _, endpoint := range []string{ep, "ep2", "ep3"} {
			if chat, _ := d.chatOf(oldID, endpoint); chat.ReferralID != nil {
				t.Errorf("source still has referral id %s on %s", *chat.ReferralID, endpoint)
			}
		}
		if got := d.subNicknames(oldID, ep); len(got) != 0 {
			t.Errorf("source still has subscriptions %v", got)
		}
		if got := d.pendingNicknames(oldID, ep); len(got) != 0 {
			t.Errorf("source still has pending %v", got)
		}
		if got := d.notificationCount(oldID); got != 0 {
			t.Errorf("source still has %d notifications", got)
		}
	})

	t.Run("destination present keeps the more permissive setup", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-220), int64(-220500)
		// The setup an admin left on the source, which the upgrade must not silently undo.
		d.insertUser(d.db, oldID, 1000, "group")
		d.addTestChat(oldID, ep, 3)
		d.addTestChat(oldID, "ep2", 3)
		d.SetMemberSubscriptions(ep, d.userID(oldID), true)
		d.SetAffiliateParams(ep, d.userID(oldID), map[string]string{"campaign": "old"})
		d.SetMemberSubscriptions("ep2", d.userID(oldID), true)
		d.MarkGreeted(ep, d.userID(oldID))
		d.MarkGreeted("ep2", d.userID(oldID))
		d.SetTimezone(d.userID(oldID), "Europe/Berlin")
		d.insertUser(d.db, newID, 2000, "supergroup")
		d.addTestChat(newID, ep, 3)

		d.MigrateChat(oldID, newID)

		user, found := d.UserByChatID(newID)
		if !found {
			t.Fatal("destination user missing")
		}
		// ep merges into the destination's chat, ep2 becomes a new one
		for _, endpoint := range []string{ep, "ep2"} {
			chat, found := d.chatOf(newID, endpoint)
			if !found {
				t.Fatalf("destination chat on %s missing", endpoint)
			}
			if !chat.MemberSubscriptions {
				t.Errorf("member_subscriptions on %s = false, want true (the source's)", endpoint)
			}
			if !d.greeted(newID, endpoint) {
				t.Errorf("greeted on %s = false, want true (the source's)", endpoint)
			}
		}
		if chat, _ := d.chatOf(newID, ep); chat.AffiliateParams["campaign"] != "old" {
			t.Errorf("affiliate campaign = %q, want old (the source's)", chat.AffiliateParams["campaign"])
		}
		if user.Timezone == nil || *user.Timezone != "Europe/Berlin" {
			t.Errorf("timezone = %v, want Europe/Berlin (the source's)", user.Timezone)
		}
	})

	t.Run("destination present keeps its own setup over the source's defaults", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-230), int64(-230500)
		d.insertUser(d.db, oldID, 1000, "group")
		d.addTestChat(oldID, ep, 3)
		d.insertUser(d.db, newID, 2000, "supergroup")
		d.addTestChat(newID, ep, 3)
		dst := d.userID(newID)
		d.SetMemberSubscriptions(ep, dst, true)
		d.SetAffiliateParams(ep, dst, map[string]string{"campaign": "new"})
		d.MarkGreeted(ep, dst)
		d.SetTimezone(dst, "Asia/Tokyo")

		d.MigrateChat(oldID, newID)

		chat, _ := d.chatOf(newID, ep)
		if !chat.MemberSubscriptions {
			t.Error("member_subscriptions = false, want true (the destination's)")
		}
		if chat.AffiliateParams["campaign"] != "new" {
			t.Errorf("affiliate campaign = %q, want new (the destination's)", chat.AffiliateParams["campaign"])
		}
		if !d.greeted(newID, ep) {
			t.Error("greeted = false, want true (the destination's)")
		}
		if user, _ := d.UserByChatID(newID); user.Timezone == nil || *user.Timezone != "Asia/Tokyo" {
			t.Errorf("timezone = %v, want Asia/Tokyo (the destination's)", user.Timezone)
		}
	})

	t.Run("destination present keeps an in-flight notification", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-250), int64(-250500)
		d.insertUser(d.db, oldID, 1000, "group")
		d.insertUser(d.db, newID, 2000, "supergroup")
		d.addSub(newID, "alice", ep)
		d.addSub(newID, "bob", ep)
		// alice is mid-delivery (sending = 1), bob is idle (sending = 0),
		// and the destination gets both streamers' alerts itself.
		d.addNotification(oldID, "alice", ep)
		d.markNotificationSending(oldID, "alice")
		d.addNotification(oldID, "bob", ep)

		d.MigrateChat(oldID, newID)

		// The in-flight row survives on the tombstone so a crash can re-arm it; the idle one is dropped.
		if got := d.notificationCount(oldID); got != 1 {
			t.Errorf("source notifications = %d, want 1 (alice's)", got)
		}
		if got := d.notificationCount(newID); got != 0 {
			t.Errorf("destination notifications = %d, want 0", got)
		}
	})

	t.Run("destination present without a referral inherits the source's", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-400), int64(-400500)
		d.insertUser(d.db, oldID, 1000, "group")
		d.addTestChat(oldID, ep, 5)
		d.SetReferralID(ep, d.userID(oldID), "refKeep")
		// The destination's chat is present but never generated a referral id of its own.
		d.insertUser(d.db, newID, 2000, "supergroup")
		d.addTestChat(newID, ep, 5)

		d.MigrateChat(oldID, newID)

		// The source's link migrates, so it keeps crediting the merged chat.
		if chat, _ := d.chatOf(newID, ep); chat.ReferralID == nil || *chat.ReferralID != "refKeep" {
			t.Errorf("destination referral_id = %s, want refKeep", asJSON(chat.ReferralID))
		}
		if owner := d.UserByReferralID(ep, "refKeep"); owner == nil || *owner != d.userID(newID) {
			t.Errorf("refKeep resolves to %v, want the user for %d", owner, newID)
		}
		if chat, _ := d.chatOf(oldID, ep); chat.ReferralID != nil {
			t.Errorf("source still has referral_id %s", asJSON(chat.ReferralID))
		}
	})

	t.Run("redelivered or sourceless migration is harmless", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-300), int64(-300500)

		// Both absent: nothing is created.
		d.MigrateChat(oldID, newID)
		if _, found := d.UserByChatID(newID); found {
			t.Error("destination created from an absent source")
		}

		// A real migration, then a redelivery: the second call is a no-op.
		d.insertUser(d.db, oldID, 1000, "group")
		d.addTestChat(oldID, ep, 5)
		d.addSub(oldID, "alice", ep)
		d.MigrateChat(oldID, newID)
		d.MigrateChat(oldID, newID)

		if _, found := d.UserByChatID(newID); !found {
			t.Fatal("destination missing after migration")
		}
		if chat, _ := d.chatOf(newID, ep); chat.MaxSubs != 5 {
			t.Errorf("max_subs = %d, want 5", chat.MaxSubs)
		}
		if got, want := d.subNicknames(newID, ep), []string{"alice"}; !slices.Equal(got, want) {
			t.Errorf("subscriptions = %v, want %v", got, want)
		}
		if _, found := d.UserByChatID(oldID); found {
			t.Error("source reappeared after redelivery")
		}
	})

	t.Run("zero is an ordinary id", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const realID = int64(-500)
		d.insertUser(d.db, realID, 1000, "group")
		d.addSub(realID, "alice", ep)

		// Telegram's zero-means-omitted is the edge's wire format, decoded there;
		// storage moves whatever pair it is handed.
		if d.MigrateChat(realID, 0) == nil {
			t.Fatal("a migration to chat 0 did not apply")
		}
		if _, found := d.UserByChatID(0); !found {
			t.Error("rows did not move to tg_chat_id = 0")
		}
		if got, want := d.subNicknames(0, ep), []string{"alice"}; !slices.Equal(got, want) {
			t.Errorf("subscriptions = %v, want %v", got, want)
		}
	})

	t.Run("destination present moves the source's history", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID, followerID = int64(-700), int64(-700500), int64(900)
		d.insertUser(d.db, oldID, 1000, "group")
		d.insertUser(d.db, newID, 2000, "supergroup")
		d.insertUser(d.db, followerID, 1500, "private")
		d.addTestChat(newID, ep, 3)

		// The source referred a surviving user, left feedback,
		// and paid on an endpoint both have and on one only it has.
		ref := d.userID(oldID)
		d.AddReferralEvent(2000, &ref, d.userID(followerID), nil)
		d.GrantStarPaymentSubs(oldID, ep, "charge-shared", 100, "subs", 3, "payload", 2000)
		d.GrantStarPaymentSubs(oldID, "ep2", "charge-own", 100, "subs", 3, "payload", 2000)
		d.AddFeedback(ep, d.userID(oldID), "hi", 2000)

		m := d.MigrateChat(oldID, newID)

		// The source stays as a tombstone,
		// but its payment ledger, feedback and referral attribution move
		// to the destination for consolidated reporting.
		if m == nil || m.Feedback != 1 || m.Payments != 2 || m.Referrals != 1 {
			t.Errorf("migration = %+v, want 1 feedback, 2 payments and 1 referral moved", m)
		}
		if _, found := d.UserByChatID(oldID); !found {
			t.Fatal("source tombstone removed")
		}
		if to, found := d.migratedToChat(oldID); !found || to != newID {
			t.Errorf("source migrated_to chat = (%d, %v), want (%d, true)", to, found, newID)
		}
		if got := d.paymentCount(newID); got != 2 {
			t.Errorf("destination star_payments = %d, want 2 (moved)", got)
		}
		if got := d.feedbackCount(newID); got != 1 {
			t.Errorf("destination feedback = %d, want 1 (moved)", got)
		}
		if got := d.referrerEventCount(newID); got != 1 {
			t.Errorf("destination referrer events = %d, want 1 (moved)", got)
		}
		if got := d.paymentCount(oldID) + d.feedbackCount(oldID); got != 0 {
			t.Errorf("source history rows = %d, want 0 (moved away)", got)
		}
	})

	t.Run("tombstoned source resolves to the live user; redelivery is a no-op", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-800), int64(-800500)
		// First, so a chain not seeded by the given id returns this user
		const otherChat = int64(-5)
		otherID, _ := d.insertUser(d.db, otherChat, 1000, "group")
		srcID, _ := d.insertUser(d.db, oldID, 1000, "group")
		dstID, _ := d.insertUser(d.db, newID, 2000, "supergroup")
		d.addTestChat(oldID, ep, 5)
		d.addTestChat(newID, ep, 7)

		// Destination exists, so the first apply tombstones the source.
		if m := d.MigrateChat(oldID, newID); m == nil || m.Renamed {
			t.Fatalf("first migration = %+v, want a non-nil tombstone outcome", m)
		}

		// A live user resolves to its own Telegram chat id.
		if got, ok := d.ChatIDForUser(dstID); !ok || got != newID {
			t.Errorf("ChatIDForUser(live) = (%d, %v), want (%d, true)", got, ok, newID)
		}
		// A send queued for the tombstoned source
		// must reach the live destination chat, not the dead old id.
		if got, ok := d.ChatIDForUser(srcID); !ok || got != newID {
			t.Errorf("ChatIDForUser(tombstone) = (%d, %v), want (%d, true)", got, ok, newID)
		}
		if got, ok := d.ChatIDForUser(otherID); !ok || got != otherChat {
			t.Errorf("ChatIDForUser(other live) = (%d, %v), want (%d, true)", got, ok, otherChat)
		}
		// An update from the tombstoned user reaches the live user as well.
		if user, found := d.UserByChatID(oldID); !found || user.UserID != dstID {
			t.Errorf("UserByChatID(tombstoned user) = (%d, %v), want (%d, true)", user.UserID, found, dstID)
		}
		if id, userCreated := d.AddUserAndChat(ep, oldID, 1000, "group"); userCreated || id != dstID {
			t.Errorf("AddUserAndChat(tombstoned user) = (%d, %v), want (%d, false)", id, userCreated, dstID)
		}

		// A redelivery re-runs MigrateChat;
		// the tombstone keeps its tg_chat_id, so migrated_to is what marks it already applied.
		// It must be a no-op.
		if m := d.MigrateChat(oldID, newID); m != nil {
			t.Errorf("redelivered migration = %+v, want nil (no-op)", m)
		}
		if chat, found := d.chatOf(newID, ep); !found || chat.MaxSubs != 7 {
			t.Errorf("destination after redelivery: found=%v max_subs=%d, want 7", found, chat.MaxSubs)
		}
	})

	t.Run("leaves other users' rows alone", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name      string
			dstExists bool
		}{
			{name: "a rename"},
			{name: "a merge", dstExists: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				tdb := newTestDB(t)
				d := tdb.Database

				const highLimitChat, lowLimitChat, srcChat, dstChat = int64(-1), int64(-2), int64(-3), int64(-4)
				// Updated before the others are added,
				// so its rows stay first and a merge not filtered by the source reads them
				highLimitUser, _ := d.insertUser(d.db, highLimitChat, 1000, "group")
				d.addTestChat(highLimitChat, ep, 99)
				d.SetTimezone(highLimitUser, "Asia/Tokyo")
				d.SetAffiliateParams(ep, highLimitUser, map[string]string{"campaign": "high"})
				d.GrantStarPaymentSubs(highLimitChat, ep, "charge-high", 100, "subs", 1, "payload", 1000)
				// A merge not filtered by the destination would raise this user's limit
				lowLimitUser, _ := d.insertUser(d.db, lowLimitChat, 1000, "group")
				d.addTestChat(lowLimitChat, ep, 1)
				src, _ := d.insertUser(d.db, srcChat, 1000, "group")
				d.addTestChat(srcChat, ep, 5)
				if tt.dstExists {
					d.insertUser(d.db, dstChat, 1000, "supergroup")
					d.addTestChat(dstChat, ep, 7)
				}
				for _, user := range []UserID{highLimitUser, src} {
					d.AddFeedback(ep, user, "text", 1000)
					d.SetReferralID(ep, user, fmt.Sprintf("referral-%d", user))
				}
				d.GrantStarPaymentSubs(srcChat, ep, "charge-src", 100, "subs", 1, "payload", 1000)
				d.AddReferralEvent(1000, &highLimitUser, lowLimitUser, nil)

				others := func() []string {
					return d.MustStrings(`
						select 'users ' || to_jsonb(u)::text from users u where id = any($1)
						union all
						select 'chat ' || to_jsonb(c)::text from chats c where user_id = any($1)
						union all
						select 'feedback ' || to_jsonb(f)::text
						from feedback f
						join chats c on c.id = f.chat_id
						where c.user_id = any($1)
						union all
						select 'payment ' || to_jsonb(p)::text
						from star_payments p
						join chats c on c.id = p.chat_id
						where c.user_id = any($1)
						union all
						select 'event ' || to_jsonb(e)::text
						from referral_events e
						where referrer_user_id = any($1) or follower_user_id = any($1)
						order by 1`,
						[]int64{int64(highLimitUser), int64(lowLimitUser)})
				}
				before := others()

				d.MigrateChat(srcChat, dstChat)

				if after := others(); !slices.Equal(after, before) {
					t.Errorf("other users' rows changed from %q to %q", before, after)
				}
				if !tt.dstExists {
					return
				}
				user, _ := d.UserByChatID(dstChat)
				chat, _ := d.chatOf(dstChat, ep)
				if chat.MaxSubs != 7 || user.Timezone != nil || chat.AffiliateParams != nil {
					t.Errorf(
						"destination = (%d, %s, %v), want its own limit and the source's empty settings",
						chat.MaxSubs,
						asJSON(user.Timezone),
						chat.AffiliateParams)
				}
			})
		}
	})

	t.Run("same id changes nothing", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const chat = int64(-300)
		id, _ := d.insertUser(d.db, chat, 1000, "group")
		tdb.addSub(chat, "a", ep)
		tdb.addPending(chat, "b", ep)

		if m := d.MigrateChat(chat, chat); m != nil {
			t.Errorf("migration = %+v, want nil", m)
		}
		if user, found := d.UserByChatID(chat); !found || user.UserID != id {
			t.Errorf("UserByChatID = (%d, %v), want (%d, true)", user.UserID, found, id)
		}
		if got, want := d.SubscribedOrPendingNicknames(ep, id), []string{"a", "b"}; !slices.Equal(got, want) {
			t.Errorf("subscribed or pending = %q, want %q", got, want)
		}
	})

	t.Run("ChatIDForUser breaks a migrated_to cycle instead of hanging", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		aID, _ := d.insertUser(d.db, -810, 1000, "group")
		bID, _ := d.insertUser(d.db, -820, 1000, "group")
		// Corrupt data MigrateChat never produces: a migrated_to cycle A -> B -> A.
		// ChatIDForUser must terminate on it, not loop forever.
		d.MustExec("update users set migrated_to = $1 where id = $2", int64(bID), int64(aID))
		d.MustExec("update users set migrated_to = $1 where id = $2", int64(aID), int64(bID))

		// Fails rather than hangs should the walk loop
		d.MustExec("set statement_timeout = '10s'")
		// No node in a cycle has migrated_to null,
		// so the walk finds no live user and drops the send rather than resolving one.
		if _, ok := d.ChatIDForUser(aID); ok {
			t.Error("cycle resolved to a live user, want the send dropped")
		}
	})

	t.Run("a tombstoned id resolves to the destination", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-900), int64(-900500)
		d.insertUser(d.db, oldID, 1000, "group")
		d.insertUser(d.db, newID, 2000, "supergroup")
		d.MigrateChat(oldID, newID)

		// An update still in flight for the old id, on an endpoint neither has a chat on
		if got, want := d.EnsureUserAndChat(ep, oldID), d.userID(newID); got != want {
			t.Errorf("EnsureUserAndChat(old id) = %d, want the destination's %d", got, want)
		}
		// The owner's /blacklist of the old id
		if got, want := d.EnsureUser(oldID), d.userID(newID); got != want {
			t.Errorf("EnsureUser(old id) = %d, want the destination's %d", got, want)
		}
		if _, found := d.chatOf(newID, ep); !found {
			t.Error("the destination has no chat on ep")
		}
	})

	t.Run("destination present pairs chats, feedback and payments by endpoint", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID = int64(-910), int64(-910500)
		// The merge creates the destination's chats on ep, ep2 and ep4 beside its own on ep3
		d.insertUser(d.db, oldID, 1000, "group")
		src := d.userID(oldID)
		d.insertUser(d.db, newID, 2000, "supergroup")
		d.addTestChat(newID, "ep3", 3)
		endpoints := []string{ep, "ep2", "ep4"}
		for i, endpoint := range endpoints {
			d.GrantStarPaymentSubs(oldID, endpoint, "charge-"+endpoint, 100, "subs", 3, "payload", 2000)
			d.AddFeedback(endpoint, src, "hi", 2000)
			d.SetLimit(endpoint, src, 10+i)
			d.setReports(oldID, endpoint, 10+i)
			d.setBlock(oldID, endpoint, 10+i)
			// Each flag has its own pattern across the chats, so swapping any two shows
			d.SetShowImages(endpoint, src, i == 0)
			d.SetOfflineNotifications(endpoint, src, i == 1)
			d.SetShowSubject(endpoint, src, i == 2)
			d.SetSilentMessages(endpoint, src, i != 2)
			d.SetMemberSubscriptions(endpoint, src, i != 1)
			d.SetAffiliateParams(endpoint, src, map[string]string{"campaign": endpoint})
		}

		d.MigrateChat(oldID, newID)

		dst := d.userID(newID)
		for _, endpoint := range endpoints {
			// The tombstone keeps its chats as they were
			got, want := chatRow(d, endpoint, dst), chatRow(d, endpoint, src)
			for _, row := range []map[string]any{got, want} {
				delete(row, "id")
				delete(row, "user_id")
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("destination chat on %s = %v, want the source's %v", endpoint, got, want)
			}
			if got := d.feedbackCountOn(newID, endpoint); got != 1 {
				t.Errorf("destination feedback on %s = %d, want 1", endpoint, got)
			}
			if got := d.paymentCountOn(newID, endpoint); got != 1 {
				t.Errorf("destination payments on %s = %d, want 1", endpoint, got)
			}
		}
	})

	t.Run("destination present takes the source's referral events", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		d := tdb.Database

		const oldID, newID, referrerID, followerID = int64(-920), int64(-920500), int64(921), int64(922)
		d.insertUser(d.db, oldID, 1000, "group")
		d.insertUser(d.db, newID, 2000, "supergroup")
		d.insertUser(d.db, referrerID, 1000, "private")
		d.insertUser(d.db, followerID, 1000, "private")
		// The source followed one user's link and referred another
		src, referrer := d.userID(oldID), d.userID(referrerID)
		d.AddReferralEvent(1500, &referrer, src, nil)
		d.AddReferralEvent(1500, &src, d.userID(followerID), nil)

		m := d.MigrateChat(oldID, newID)

		if m == nil || m.Referrals != 2 {
			t.Errorf("migration = %+v, want 2 referral events moved", m)
		}
		if got := d.followerEventCount(newID); got != 1 {
			t.Errorf("destination follower events = %d, want 1", got)
		}
		if got := d.referrerEventCount(newID); got != 1 {
			t.Errorf("destination referrer events = %d, want 1", got)
		}
		if got := d.followerEventCount(oldID) + d.referrerEventCount(oldID); got != 0 {
			t.Errorf("source referral events = %d, want 0", got)
		}
	})
}
