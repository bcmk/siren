package db

import (
	"testing"
	"time"
)

func (d *Database) ensureStreamer(nickname string) {
	d.MustExec(
		"insert into streamers (nickname) values ($1) on conflict (nickname) do nothing",
		nickname)
}

// addSub inserts a subscription directly, creating the streamer if needed.
func (d *Database) addSub(chatID int64, nickname, endpoint string) {
	d.ensureStreamer(nickname)
	d.MustExec(`
		insert into subscriptions (user_id, streamer_id, endpoint)
		select u.id, s.id, $3
		from users u, streamers s
		where u.chat_id = $1 and s.nickname = $2`,
		chatID, nickname, endpoint)
}

func (d *Database) addPending(chatID int64, nickname, endpoint string) {
	d.MustExec(`
		insert into pending_subscriptions (user_id, nickname, endpoint)
		select u.id, $2, $3 from users u where u.chat_id = $1`,
		chatID, nickname, endpoint)
}

func (d *Database) addBlock(chatID int64, endpoint string, count int) {
	d.MustExec(`
		insert into block (user_id, endpoint, block)
		select u.id, $2, $3 from users u where u.chat_id = $1`,
		chatID, endpoint, count)
}

func (d *Database) addNotification(chatID int64, nickname, endpoint string) {
	d.ensureStreamer(nickname)
	d.MustExec(`
		insert into notification_queue (endpoint, user_id, status, streamer_id)
		select $3, u.id, 0, s.id
		from users u, streamers s
		where u.chat_id = $1 and s.nickname = $2`,
		chatID, nickname, endpoint)
}

// markNotificationSending flags a chat's queued notification as in-flight.
func (d *Database) markNotificationSending(chatID int64, nickname string) {
	d.MustExec(`
		update notification_queue nq set sending = 1
		from users u, streamers s
		where nq.user_id = u.id and nq.streamer_id = s.id
		and u.chat_id = $1 and s.nickname = $2`,
		chatID, nickname)
}

func (d *Database) subNicknames(chatID int64, endpoint string) []string {
	return d.MustStrings(`
		select s.nickname
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		join users u on u.id = sub.user_id
		where u.chat_id = $1 and sub.endpoint = $2
		order by s.nickname`,
		chatID, endpoint)
}

func (d *Database) pendingNicknames(chatID int64, endpoint string) []string {
	return d.MustStrings(`
		select ps.nickname
		from pending_subscriptions ps
		join users u on u.id = ps.user_id
		where u.chat_id = $1 and ps.endpoint = $2
		order by ps.nickname`,
		chatID, endpoint)
}

func (d *Database) blockCount(chatID int64, endpoint string) (int, bool) {
	var count int
	found := d.MaybeRecord(
		"select b.block from block b join users u on u.id = b.user_id where u.chat_id = $1 and b.endpoint = $2",
		QueryParams{chatID, endpoint},
		ScanTo{&count})
	return count, found
}

func (d *Database) notificationCount(chatID int64) int {
	return d.MustInt(
		"select count(*) from notification_queue nq join users u on u.id = nq.user_id where u.chat_id = $1",
		chatID)
}

func (d *Database) setReports(chatID int64, reports int) {
	d.MustExec("update users set reports = $1 where chat_id = $2", reports, chatID)
}

// userID returns a chat's surrogate id without creating one.
func (d *Database) userID(chatID int64) UserID {
	var id int64
	d.MaybeRecord("select id from users where chat_id = $1", QueryParams{chatID}, ScanTo{&id})
	return UserID(id)
}

func (d *Database) addReferral(chatID int64, referralID string, referredUsers int) {
	d.MustExec(`
		insert into referrals (user_id, referral_id, referred_users)
		select u.id, $2, $3 from users u where u.chat_id = $1`,
		chatID, referralID, referredUsers)
}

func (d *Database) referredUsers(chatID int64) (int, bool) {
	var n int
	found := d.MaybeRecord(
		"select r.referred_users from referrals r join users u on u.id = r.user_id where u.chat_id = $1",
		QueryParams{chatID},
		ScanTo{&n})
	return n, found
}

func (d *Database) paymentCount(chatID int64) int {
	return d.MustInt(`
		select count(*) from star_payments p
		join users u on u.id = p.user_id
		where u.chat_id = $1`,
		chatID)
}

func (d *Database) referrerEventCount(chatID int64) int {
	return d.MustInt(`
		select count(*) from referral_events r
		join users u on u.id = r.referrer_user_id
		where u.chat_id = $1`,
		chatID)
}

// migratedToChat returns the chat_id the given chat was tombstoned into.
func (d *Database) migratedToChat(chatID int64) (int64, bool) {
	var c int64
	found := d.MaybeRecord(`
		select d.chat_id from users s
		join users d on d.id = s.migrated_to
		where s.chat_id = $1`,
		QueryParams{chatID},
		ScanTo{&c})
	return c, found
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMigrateChat(t *testing.T) {
	t.Parallel()
	const ep = "ep"

	t.Run("destination absent renames the chat", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID = int64(-100), int64(-100500)
		d.AddUser(oldID, 5, 1000, "group")
		d.addSub(oldID, "alice", ep)
		d.addSub(oldID, "bob", ep)
		d.addPending(oldID, "pending1", ep)
		d.addBlock(oldID, ep, 3)
		d.addNotification(oldID, "alice", ep)
		d.addNotification(oldID, "bob", ep)
		d.addReferral(oldID, "refOld", 2)

		d.MigrateChat(oldID, newID)

		if _, found := d.User(oldID); found {
			t.Error("source user still present after migration")
		}
		user, found := d.User(newID)
		if !found {
			t.Fatal("destination user missing after migration")
		}
		if user.MaxSubs != 5 {
			t.Errorf("max_subs = %d, want 5", user.MaxSubs)
		}
		if user.ChatType == nil || *user.ChatType != "supergroup" {
			t.Errorf("chat_type = %v, want supergroup", user.ChatType)
		}

		if got, want := d.subNicknames(newID, ep), []string{"alice", "bob"}; !equalStrings(got, want) {
			t.Errorf("destination subscriptions = %v, want %v", got, want)
		}
		if got := d.subNicknames(oldID, ep); len(got) != 0 {
			t.Errorf("source still has subscriptions %v", got)
		}

		if got, want := d.pendingNicknames(newID, ep), []string{"pending1"}; !equalStrings(got, want) {
			t.Errorf("destination pending = %v, want %v", got, want)
		}
		if got := d.pendingNicknames(oldID, ep); len(got) != 0 {
			t.Errorf("source still has pending %v", got)
		}

		if count, found := d.blockCount(newID, ep); !found || count != 3 {
			t.Errorf("destination block = (%d, %v), want (3, true)", count, found)
		}
		if _, found := d.blockCount(oldID, ep); found {
			t.Error("source still has a block row")
		}

		if got := d.notificationCount(newID); got != 2 {
			t.Errorf("destination notifications = %d, want 2", got)
		}
		if got := d.notificationCount(oldID); got != 0 {
			t.Errorf("source still has %d notifications", got)
		}

		// The referral follows the chat, so its link now credits the new ID.
		if id := d.ReferralID(d.userID(newID)); id == nil || *id != "refOld" {
			t.Errorf("destination referral_id = %v, want refOld", id)
		}
		if id := d.ReferralID(d.userID(oldID)); id != nil {
			t.Errorf("source still has referral_id %v", *id)
		}
		if owner := d.UserForReferralID("refOld"); owner == nil || *owner != d.userID(newID) {
			t.Errorf("refOld resolves to %v, want the user for %d", owner, newID)
		}
		if n, found := d.referredUsers(newID); !found || n != 2 {
			t.Errorf("destination referred_users = (%d, %v), want (2, true)", n, found)
		}
	})

	t.Run("destination present drops the source's operational rows", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID = int64(-200), int64(-200500)
		// The destination has the larger limit and keeps its own data;
		// the source is tombstoned (operational dropped) and must not drag it down.
		d.AddUser(oldID, 5, 1000, "group")
		d.setReports(oldID, 4)
		d.BlacklistUser(d.userID(oldID))
		d.addSub(oldID, "alice", ep)
		d.addPending(oldID, "pendingOld", ep)
		d.addBlock(oldID, "ep2", 4)
		d.addNotification(oldID, "alice", ep)
		d.addReferral(oldID, "refOld", 2)

		d.AddUser(newID, 20, 2000, "supergroup")
		d.setReports(newID, 1)
		d.addSub(newID, "carol", ep)
		d.addPending(newID, "pendingNew", ep)
		d.addBlock(newID, ep, 7)
		d.addNotification(newID, "carol", ep)
		d.addReferral(newID, "refNew", 3)

		d.MigrateChat(oldID, newID)

		// The source is kept as a tombstone, linked to the destination.
		if _, found := d.User(oldID); !found {
			t.Error("source tombstone removed")
		}
		if to, found := d.migratedToChat(oldID); !found || to != newID {
			t.Errorf("source migrated_to chat = (%d, %v), want (%d, true)", to, found, newID)
		}
		user, found := d.User(newID)
		if !found {
			t.Fatal("destination user missing")
		}
		// The limit is the larger of the two and never regresses to the source's.
		if user.MaxSubs != 20 {
			t.Errorf("max_subs = %d, want 20 (max of 5 and 20)", user.MaxSubs)
		}
		if user.Reports != 1 {
			t.Errorf("reports = %d, want 1 (destination's)", user.Reports)
		}
		if user.Blacklist {
			t.Error("blacklist = true, want false (destination's)")
		}

		if got, want := d.subNicknames(newID, ep), []string{"carol"}; !equalStrings(got, want) {
			t.Errorf("subscriptions = %v, want %v (destination's only)", got, want)
		}
		if got, want := d.pendingNicknames(newID, ep), []string{"pendingNew"}; !equalStrings(got, want) {
			t.Errorf("pending = %v, want %v (destination's only)", got, want)
		}

		// The destination's block survives; the source's is dropped, not carried.
		if count, found := d.blockCount(newID, ep); !found || count != 7 {
			t.Errorf("destination block on %q = (%d, %v), want (7, true)", ep, count, found)
		}
		if _, found := d.blockCount(newID, "ep2"); found {
			t.Error("source block on ep2 leaked to destination")
		}

		if got := d.notificationCount(newID); got != 1 {
			t.Errorf("notifications = %d, want 1 (destination's only)", got)
		}

		// The destination keeps its own referral; the source's link is dropped.
		if id := d.ReferralID(d.userID(newID)); id == nil || *id != "refNew" {
			t.Errorf("destination referral_id = %v, want refNew", id)
		}
		if n, found := d.referredUsers(newID); !found || n != 3 {
			t.Errorf("referred_users = (%d, %v), want (3, true)", n, found)
		}
		if owner := d.UserForReferralID("refOld"); owner != nil {
			t.Errorf("dropped link refOld still resolves to %d", *owner)
		}

		// The source's operational rows are all gone (the tombstone remains).
		if id := d.ReferralID(d.userID(oldID)); id != nil {
			t.Errorf("source still has referral_id %v", *id)
		}
		if got := d.subNicknames(oldID, ep); len(got) != 0 {
			t.Errorf("source still has subscriptions %v", got)
		}
		if got := d.pendingNicknames(oldID, ep); len(got) != 0 {
			t.Errorf("source still has pending %v", got)
		}
		if _, found := d.blockCount(oldID, "ep2"); found {
			t.Error("source still has a block row")
		}
		if got := d.notificationCount(oldID); got != 0 {
			t.Errorf("source still has %d notifications", got)
		}
	})

	t.Run("destination present keeps an in-flight notification", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID = int64(-250), int64(-250500)
		d.AddUser(oldID, 5, 1000, "group")
		d.AddUser(newID, 20, 2000, "supergroup")
		// alice is mid-delivery (sending = 1), bob is idle (sending = 0).
		d.addNotification(oldID, "alice", ep)
		d.markNotificationSending(oldID, "alice")
		d.addNotification(oldID, "bob", ep)

		d.MigrateChat(oldID, newID)

		// The in-flight row survives so a crash can re-arm it;
		// the idle one is dropped with the rest of the source's operational rows.
		if got := d.notificationCount(oldID); got != 1 {
			t.Errorf("source notifications = %d, want 1 (only the in-flight row survives)", got)
		}
	})

	t.Run("destination present without a referral inherits the source's", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID = int64(-400), int64(-400500)
		d.AddUser(oldID, 5, 1000, "group")
		d.addReferral(oldID, "refKeep", 4)
		// The destination is present but never generated a referral of its own.
		d.AddUser(newID, 5, 2000, "supergroup")

		d.MigrateChat(oldID, newID)

		// The source's link migrates, so it keeps crediting the merged user.
		if id := d.ReferralID(d.userID(newID)); id == nil || *id != "refKeep" {
			t.Errorf("destination referral_id = %v, want refKeep", id)
		}
		if n, found := d.referredUsers(newID); !found || n != 4 {
			t.Errorf("destination referred_users = (%d, %v), want (4, true)", n, found)
		}
		if owner := d.UserForReferralID("refKeep"); owner == nil || *owner != d.userID(newID) {
			t.Errorf("refKeep resolves to %v, want the user for %d", owner, newID)
		}
		if id := d.ReferralID(d.userID(oldID)); id != nil {
			t.Errorf("source still has referral_id %v", *id)
		}
	})

	t.Run("redelivered or sourceless migration is harmless", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID = int64(-300), int64(-300500)

		// Both absent: nothing is created.
		d.MigrateChat(oldID, newID)
		if _, found := d.User(newID); found {
			t.Error("destination created from an absent source")
		}

		// A real migration, then a redelivery: the second call is a no-op.
		d.AddUser(oldID, 5, 1000, "group")
		d.addSub(oldID, "alice", ep)
		d.MigrateChat(oldID, newID)
		d.MigrateChat(oldID, newID)

		user, found := d.User(newID)
		if !found {
			t.Fatal("destination missing after migration")
		}
		if user.MaxSubs != 5 {
			t.Errorf("max_subs = %d, want 5", user.MaxSubs)
		}
		if got, want := d.subNicknames(newID, ep), []string{"alice"}; !equalStrings(got, want) {
			t.Errorf("subscriptions = %v, want %v", got, want)
		}
		if _, found := d.User(oldID); found {
			t.Error("source reappeared after redelivery")
		}
	})

	t.Run("zero id is ignored", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const realID = int64(-500)
		d.AddUser(realID, 5, 1000, "group")
		d.addSub(realID, "alice", ep)

		// A malformed migration message can yield a zero id; it must be ignored,
		// never parking the chat's rows at chat_id = 0.
		d.MigrateChat(realID, 0)
		d.MigrateChat(0, realID)

		if _, found := d.User(0); found {
			t.Error("rows parked at chat_id = 0")
		}
		if user, found := d.User(realID); !found || user.MaxSubs != 5 {
			t.Errorf("real chat altered: found=%v max_subs=%d", found, user.MaxSubs)
		}
		if got, want := d.subNicknames(realID, ep), []string{"alice"}; !equalStrings(got, want) {
			t.Errorf("subscriptions = %v, want %v", got, want)
		}
	})

	t.Run("destination present moves the source's history", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID, followerID = int64(-700), int64(-700500), int64(900)
		d.AddUser(oldID, 5, 1000, "group")
		d.AddUser(newID, 5, 2000, "supergroup")
		d.AddUser(followerID, 5, 1500, "private")

		// The source referred a surviving user and made a payment.
		ref := d.userID(oldID)
		d.AddReferralEvent(2000, &ref, d.userID(followerID), nil)
		d.GrantStarPaymentSubs(oldID, ep, "charge-move", 100, "subs", 3, "payload", 2000)

		d.MigrateChat(oldID, newID)

		// The source stays as a tombstone,
		// but its payment ledger and referral attribution move
		// to the destination for consolidated reporting.
		if _, found := d.User(oldID); !found {
			t.Fatal("source tombstone removed")
		}
		if to, found := d.migratedToChat(oldID); !found || to != newID {
			t.Errorf("source migrated_to chat = (%d, %v), want (%d, true)", to, found, newID)
		}
		if got := d.paymentCount(newID); got != 1 {
			t.Errorf("destination star_payments = %d, want 1 (moved)", got)
		}
		if got := d.referrerEventCount(newID); got != 1 {
			t.Errorf("destination referrer events = %d, want 1 (moved)", got)
		}
		if got := d.paymentCount(oldID); got != 0 {
			t.Errorf("source star_payments = %d, want 0 (moved away)", got)
		}
	})

	t.Run("tombstoned source resolves to the live chat; redelivery is a no-op", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID = int64(-800), int64(-800500)
		srcID, _ := d.AddUser(oldID, 5, 1000, "group")
		dstID, _ := d.AddUser(newID, 7, 2000, "supergroup")

		// Destination exists, so the first apply tombstones the source.
		if m := d.MigrateChat(oldID, newID); m == nil || m.Renamed {
			t.Fatalf("first migration = %+v, want a non-nil tombstone outcome", m)
		}

		// A live user resolves to its own chat.
		if got, ok := d.ChatIDForUser(dstID); !ok || got != newID {
			t.Errorf("ChatIDForUser(live) = (%d, %v), want (%d, true)", got, ok, newID)
		}
		// A send queued for the tombstoned source
		// must reach the live destination chat, not the dead old id.
		if got, ok := d.ChatIDForUser(srcID); !ok || got != newID {
			t.Errorf("ChatIDForUser(tombstone) = (%d, %v), want (%d, true)", got, ok, newID)
		}

		// A redelivery re-runs MigrateChat; the tombstone keeps its chat_id,
		// so migrated_to is what marks it already applied. It must be a no-op.
		if m := d.MigrateChat(oldID, newID); m != nil {
			t.Errorf("redelivered migration = %+v, want nil (no-op)", m)
		}
		if user, found := d.User(newID); !found || user.MaxSubs != 7 {
			t.Errorf("destination after redelivery: found=%v max_subs=%d, want 7", found, user.MaxSubs)
		}
	})

	t.Run("ChatIDForUser breaks a migrated_to cycle instead of hanging", func(t *testing.T) {
		t.Parallel()
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		aID, _ := d.AddUser(-810, 5, 1000, "group")
		bID, _ := d.AddUser(-820, 5, 1000, "group")
		// Corrupt data MigrateChat never produces: a migrated_to cycle A -> B -> A.
		// ChatIDForUser must terminate on it, not loop forever.
		d.MustExec("update users set migrated_to = $1 where id = $2", int64(bID), int64(aID))
		d.MustExec("update users set migrated_to = $1 where id = $2", int64(aID), int64(bID))

		done := make(chan struct{})
		go func() {
			defer close(done)
			// No node in a cycle has migrated_to null, so the walk finds no live
			// chat and drops the send rather than resolving one.
			if _, ok := d.ChatIDForUser(aID); ok {
				t.Error("cycle resolved to a live chat, want the send dropped")
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("ChatIDForUser hung on a migrated_to cycle")
		}
	})
}
