package db

import "testing"

func (d *Database) ensureStreamer(nickname string) {
	d.MustExec(
		"insert into streamers (nickname) values ($1) on conflict (nickname) do nothing",
		nickname)
}

// addSub inserts a subscription directly, creating the streamer if needed.
func (d *Database) addSub(chatID int64, nickname, endpoint string) {
	d.ensureStreamer(nickname)
	d.MustExec(`
		insert into subscriptions (chat_id, streamer_id, endpoint)
		select $1, id, $3 from streamers where nickname = $2`,
		chatID, nickname, endpoint)
}

func (d *Database) addPending(chatID int64, nickname, endpoint string) {
	d.MustExec(
		"insert into pending_subscriptions (chat_id, nickname, endpoint) values ($1, $2, $3)",
		chatID, nickname, endpoint)
}

func (d *Database) addBlock(chatID int64, endpoint string, count int) {
	d.MustExec(
		"insert into block (chat_id, endpoint, block) values ($1, $2, $3)",
		chatID, endpoint, count)
}

func (d *Database) addNotification(chatID int64, nickname, endpoint string) {
	d.ensureStreamer(nickname)
	d.MustExec(`
		insert into notification_queue (endpoint, chat_id, status, streamer_id)
		select $3, $1, 0, id from streamers where nickname = $2`,
		chatID, nickname, endpoint)
}

func (d *Database) subNicknames(chatID int64, endpoint string) []string {
	return d.MustStrings(`
		select s.nickname
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		where sub.chat_id = $1 and sub.endpoint = $2
		order by s.nickname`,
		chatID, endpoint)
}

func (d *Database) pendingNicknames(chatID int64, endpoint string) []string {
	return d.MustStrings(
		"select nickname from pending_subscriptions where chat_id = $1 and endpoint = $2 order by nickname",
		chatID, endpoint)
}

func (d *Database) blockCount(chatID int64, endpoint string) (int, bool) {
	var count int
	found := d.MaybeRecord(
		"select block from block where chat_id = $1 and endpoint = $2",
		QueryParams{chatID, endpoint},
		ScanTo{&count})
	return count, found
}

func (d *Database) notificationCount(chatID int64) int {
	return d.MustInt("select count(*) from notification_queue where chat_id = $1", chatID)
}

func (d *Database) setReports(chatID int64, reports int) {
	d.MustExec("update users set reports = $1 where chat_id = $2", reports, chatID)
}

func (d *Database) addReferral(chatID int64, referralID string, referredUsers int) {
	d.MustExec(
		"insert into referrals (chat_id, referral_id, referred_users) values ($1, $2, $3)",
		chatID, referralID, referredUsers)
}

func (d *Database) referredUsers(chatID int64) (int, bool) {
	var n int
	found := d.MaybeRecord(
		"select referred_users from referrals where chat_id = $1",
		QueryParams{chatID},
		ScanTo{&n})
	return n, found
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
	const ep = "ep"

	t.Run("destination absent renames the chat", func(t *testing.T) {
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
		if id := d.ReferralID(newID); id == nil || *id != "refOld" {
			t.Errorf("destination referral_id = %v, want refOld", id)
		}
		if id := d.ReferralID(oldID); id != nil {
			t.Errorf("source still has referral_id %v", *id)
		}
		if owner := d.ChatForReferralID("refOld"); owner == nil || *owner != newID {
			t.Errorf("refOld resolves to %v, want %d", owner, newID)
		}
		if n, found := d.referredUsers(newID); !found || n != 2 {
			t.Errorf("destination referred_users = (%d, %v), want (2, true)", n, found)
		}
	})

	t.Run("destination present drops the source", func(t *testing.T) {
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID = int64(-200), int64(-200500)
		// The destination has the larger limit and keeps its own data;
		// the source is dropped and must not drag that limit down.
		d.AddUser(oldID, 5, 1000, "group")
		d.setReports(oldID, 4)
		d.BlacklistUser(oldID)
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

		if _, found := d.User(oldID); found {
			t.Error("source user still present after drop")
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
		if id := d.ReferralID(newID); id == nil || *id != "refNew" {
			t.Errorf("destination referral_id = %v, want refNew", id)
		}
		if n, found := d.referredUsers(newID); !found || n != 3 {
			t.Errorf("referred_users = (%d, %v), want (3, true)", n, found)
		}
		if owner := d.ChatForReferralID("refOld"); owner != nil {
			t.Errorf("dropped link refOld still resolves to %d", *owner)
		}

		// The source is gone everywhere.
		if id := d.ReferralID(oldID); id != nil {
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

	t.Run("orphan child row at destination does not collide", func(t *testing.T) {
		tdb := newTestDB(t)
		defer tdb.terminate()
		d := tdb.Database

		const oldID, newID = int64(-400), int64(-400500)
		d.AddUser(oldID, 5, 1000, "group")
		d.addSub(oldID, "alice", ep)
		d.addBlock(oldID, ep, 3)
		d.addPending(oldID, "pendingX", ep)
		d.addReferral(oldID, "refSource", 1)
		// newID has a dangling row of every key-bearing kind but no users row,
		// so a blind move would collide on a primary key.
		d.addBlock(newID, ep, 9)
		d.addSub(newID, "alice", ep)
		d.addPending(newID, "pendingX", ep)
		d.addReferral(newID, "refOrphan", 7)

		d.MigrateChat(oldID, newID)

		user, found := d.User(newID)
		if !found {
			t.Fatal("destination user missing after migration")
		}
		if user.MaxSubs != 5 {
			t.Errorf("max_subs = %d, want 5", user.MaxSubs)
		}
		if got, want := d.subNicknames(newID, ep), []string{"alice"}; !equalStrings(got, want) {
			t.Errorf("subscriptions = %v, want %v", got, want)
		}
		// Each dangling row is cleared, then the source's row moves in.
		if count, found := d.blockCount(newID, ep); !found || count != 3 {
			t.Errorf("block on %q = (%d, %v), want (3, true)", ep, count, found)
		}
		if got, want := d.pendingNicknames(newID, ep), []string{"pendingX"}; !equalStrings(got, want) {
			t.Errorf("pending = %v, want %v", got, want)
		}
		if id := d.ReferralID(newID); id == nil || *id != "refSource" {
			t.Errorf("referral_id = %v, want refSource", id)
		}
		if owner := d.ChatForReferralID("refOrphan"); owner != nil {
			t.Errorf("orphan referral refOrphan still resolves to %d", *owner)
		}
		if _, found := d.User(oldID); found {
			t.Error("source still present after migration")
		}
	})

	t.Run("redelivered or sourceless migration is harmless", func(t *testing.T) {
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
}
