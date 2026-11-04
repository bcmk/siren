package db

import (
	"cmp"
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bcmk/siren/v6/lib/cmdlib"
	"github.com/jackc/pgx/v5"
)

func ptr[T any](v T) *T { return &v }

func insertStreamerRow(d *Database, s Streamer) int {
	d.MustExec("insert into nicknames (nickname) values ($1)", s.Nickname)
	return d.MustInt(`
		insert into streamers (
			nickname,
			confirmed_status,
			unconfirmed_status,
			unconfirmed_timestamp,
			prev_unconfirmed_status,
			prev_unconfirmed_timestamp)
		values ($1, $2, $3, $4, $5, $6)
		returning id`,
		s.Nickname,
		s.ConfirmedStatus,
		s.UnconfirmedStatus,
		s.UnconfirmedTimestamp,
		s.PrevUnconfirmedStatus,
		s.PrevUnconfirmedTimestamp)
}

func insertSubscription(d *Database, endpoint string, chatID int64, nickname string) {
	d.AddUser(chatID, 0, 0, "")
	d.MustExec(`
		insert into subscriptions (endpoint, user_id, streamer_id)
		select $1, u.id, s.id
		from users u, streamers s
		where u.chat_id = $2 and s.nickname = $3`,
		endpoint, chatID, nickname)
}

func insertPendingSubscription(d *Database, endpoint string, chatID int64, nickname string, checking bool) {
	d.AddUser(chatID, 0, 0, "")
	d.MustExec(`
		insert into pending_subscriptions (endpoint, user_id, nickname, checking)
		select $1, u.id, $3, $4
		from users u
		where u.chat_id = $2`,
		endpoint, chatID, nickname, checking)
}

func confirmedStatusesForChat(d *Database, endpoint string, chatID int64) (statuses []Streamer) {
	var iter Streamer
	d.MustQuery(`
		select s.nickname, s.confirmed_status
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		join users u on u.id = sub.user_id
		where u.chat_id = $1 and sub.endpoint = $2
		order by s.nickname`,
		QueryParams{chatID, endpoint},
		ScanTo{&iter.Nickname, &iter.ConfirmedStatus},
		func() { statuses = append(statuses, iter) })
	return
}

func subscribedChats(d *Database, nickname string) (chats []int64, endpoints []string) {
	var chatID int64
	var endpoint string
	d.MustQuery(`
		select u.chat_id, sub.endpoint
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		join users u on u.id = sub.user_id
		where s.nickname = $1
		order by u.chat_id`,
		QueryParams{nickname},
		ScanTo{&chatID, &endpoint},
		func() {
			chats = append(chats, chatID)
			endpoints = append(endpoints, endpoint)
		})
	return
}

// broadcastUserIDs projects broadcast recipients to their ids.
func broadcastUserIDs(users []User) (ids []UserID) {
	for _, u := range users {
		ids = append(ids, u.UserID)
	}
	return
}

func TestSql(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	for _, n := range []string{"a", "b", "c", "c2", "c3", "d", "e", "f", "g"} {
		insertStreamerRow(db.Database, Streamer{Nickname: n})
	}
	insertSubscription(db.Database, "ep1", 1, "a")
	insertSubscription(db.Database, "ep1", 2, "b")
	insertSubscription(db.Database, "ep1", 3, "c")
	insertSubscription(db.Database, "ep1", 3, "c2")
	insertSubscription(db.Database, "ep1", 3, "c3")
	insertSubscription(db.Database, "ep1", 4, "d")
	insertSubscription(db.Database, "ep1", 5, "d")
	insertSubscription(db.Database, "ep1", 6, "e")
	insertSubscription(db.Database, "ep1", 7, "f")
	insertSubscription(db.Database, "ep2", 6, "e")
	insertSubscription(db.Database, "ep2", 7, "f")
	insertSubscription(db.Database, "ep2", 8, "g")
	db.MustExec("insert into block (endpoint, user_id, block) select $1, u.id, $3 from users u where u.chat_id = $2", "ep1", 2, 0)
	db.MustExec("insert into block (endpoint, user_id, block) select $1, u.id, $3 from users u where u.chat_id = $2", "ep1", 3, 5)
	db.MustExec("update streamers set confirmed_status = $2 where nickname = $1", "a", cmdlib.StatusOnline)
	db.MustExec("update streamers set confirmed_status = $2 where nickname = $1", "b", cmdlib.StatusOnline)
	db.MustExec("update streamers set confirmed_status = $2 where nickname = $1", "c", cmdlib.StatusOnline)
	db.MustExec("update streamers set confirmed_status = $2 where nickname = $1", "c2", cmdlib.StatusOnline)
	broadcast1 := db.BroadcastUsers("ep1")
	if !reflect.DeepEqual(broadcastUserIDs(broadcast1), []UserID{1, 2, 3, 4, 5, 6, 7}) {
		t.Error("unexpected broadcast users result", broadcast1)
	}
	// Only user 3 has a nonzero block row; a broken join or coalesce would zero it.
	for _, u := range broadcast1 {
		want := 0
		if u.UserID == 3 {
			want = 5
		}
		if u.Blocked != want {
			t.Errorf("broadcast user %d blocked = %d, want %d", u.UserID, u.Blocked, want)
		}
	}
	broadcastUsers := broadcastUserIDs(db.BroadcastUsers("ep2"))
	if !reflect.DeepEqual(broadcastUsers, []UserID{6, 7, 8}) {
		t.Error("unexpected broadcast users result", broadcastUsers)
	}
	chatsForStreamer, endpoints := subscribedChats(db.Database, "a")
	if !reflect.DeepEqual(endpoints, []string{"ep1"}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	if !reflect.DeepEqual(chatsForStreamer, []int64{1}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = subscribedChats(db.Database, "b")
	if !reflect.DeepEqual(chatsForStreamer, []int64{2}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = subscribedChats(db.Database, "c")
	if !reflect.DeepEqual(chatsForStreamer, []int64{3}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = subscribedChats(db.Database, "d")
	if !reflect.DeepEqual(chatsForStreamer, []int64{4, 5}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = subscribedChats(db.Database, "e")
	if !reflect.DeepEqual(chatsForStreamer, []int64{6, 6}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = subscribedChats(db.Database, "f")
	if !reflect.DeepEqual(chatsForStreamer, []int64{7, 7}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	db.IncrementBlock("ep1", 2)
	db.IncrementBlock("ep1", 2)
	if db.MustInt("select b.block from block b join users u on u.id = b.user_id where u.chat_id = $1 and b.endpoint = $2", 2, "ep1") != 2 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	db.IncrementBlock("ep2", 2)
	if db.MustInt("select b.block from block b join users u on u.id = b.user_id where u.chat_id = $1 and b.endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	db.ResetBlock("ep1", 2)
	if db.MustInt("select b.block from block b join users u on u.id = b.user_id where u.chat_id = $1 and b.endpoint = $2", 2, "ep1") != 0 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	if db.MustInt("select b.block from block b join users u on u.id = b.user_id where u.chat_id = $1 and b.endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	db.IncrementBlock("ep1", 1)
	db.IncrementBlock("ep1", 1)
	if db.MustInt("select b.block from block b join users u on u.id = b.user_id where u.chat_id = $1", 1) != 2 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	statuses := confirmedStatusesForChat(db.Database, "ep1", 3)
	if !reflect.DeepEqual(statuses, []Streamer{
		{Nickname: "c", ConfirmedStatus: cmdlib.StatusOnline},
		{Nickname: "c2", ConfirmedStatus: cmdlib.StatusOnline},
		{Nickname: "c3"}}) {
		t.Error("unexpected statuses", statuses)
	}
}

func TestNotificationsStorage(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	db.AddUser(1, 3, 0, "private")
	db.AddUser(2, 3, 0, "private")
	db.AddUser(3, 3, 0, "private")

	idA := insertStreamerRow(db.Database, Streamer{Nickname: "a"})
	idB := insertStreamerRow(db.Database, Streamer{Nickname: "b"})
	idC := insertStreamerRow(db.Database, Streamer{Nickname: "c"})

	timeDiff := 2
	nots := []Notification{
		{
			Endpoint:   "endpoint_a",
			UserID:     db.EnsureUser(1),
			StreamerID: &idA,
			Nickname:   "a",
			Status:     cmdlib.StatusUnknown,
			TimeDiff:   nil,
			ImageURL:   "image_a",
			Social:     false,
			Priority:   PriorityHigh,
			Sound:      false,
			Kind:       NotificationPacket,
		},
		{
			Endpoint:   "endpoint_b",
			UserID:     db.EnsureUser(2),
			StreamerID: &idB,
			Nickname:   "b",
			Status:     cmdlib.StatusOffline,
			TimeDiff:   &timeDiff,
			ImageURL:   "image_b",
			Social:     true,
			Priority:   PriorityLow,
			Sound:      true,
			Kind:       ReplyPacket,
		},
	}

	db.StoreNotifications(nots)
	newNots := db.NewNotifications()
	nots[0].ID = 1
	nots[1].ID = 2
	// The read joins the recipient's chat, which the stored literal does not carry.
	nots[0].ChatID = 1
	nots[1].ChatID = 2
	nots[0].ChatType = ptr("private")
	nots[1].ChatType = ptr("private")
	if !reflect.DeepEqual(nots, newNots) {
		t.Errorf("unexpected notifications, expocted: %v, got: %v", nots, newNots)
	}
	nots = []Notification{
		{
			Endpoint:   "endpoint_c",
			UserID:     db.EnsureUser(3),
			StreamerID: &idC,
			Nickname:   "c",
			Status:     cmdlib.StatusOnline,
			TimeDiff:   nil,
			ImageURL:   "image_c",
			Social:     true,
			Priority:   PriorityLow,
		},
	}
	db.StoreNotifications(nots)
	newNots = db.NewNotifications()
	nots[0].ID = 3
	nots[0].ChatID = 3
	nots[0].ChatType = ptr("private")
	if !reflect.DeepEqual(nots, newNots) {
		t.Errorf("unexpected notifications, expocted: %v, got: %v", nots, newNots)
	}
	count := db.MustInt("select count(*) from notification_queue")
	if count != 3 {
		t.Errorf("unexpected notifications count %d", count)
	}
}

func TestStreamers(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	insertStreamerRow(db.Database, Streamer{Nickname: "a", ConfirmedStatus: cmdlib.StatusOffline})
	if db.MaybeStreamer("a") == nil {
		t.Error("unexpected result")
	}
	if db.MaybeStreamer("b") != nil {
		t.Error("unexpected result")
	}
}

func TestCopyFromAndBatchInTransaction(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Test that CopyFrom and SendBatch are in the same transaction
	// by making SendBatch fail and verifying CopyFrom data is rolled back

	// Insert a streamer to get an integer ID for the CopyFrom test
	streamerIntID := insertStreamerRow(db.Database, Streamer{Nickname: "a"})

	tx, err := db.begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// CopyFrom should succeed
	rows := [][]interface{}{
		{streamerIntID, 100, int16(cmdlib.StatusOnline), int16(cmdlib.StatusUnknown)},
	}
	_, err = tx.CopyFrom(
		context.Background(),
		pgx.Identifier{"status_changes"},
		[]string{"streamer_id", "timestamp", "status", "prev_status"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		t.Fatal(err)
	}

	// SendBatch with invalid status (violates check constraint) should fail
	batch := &pgx.Batch{}
	batch.Queue(`
		insert into streamers (nickname, unconfirmed_status)
		values ($1, $2)`,
		"test_streamer", 999) // 999 violates check constraint
	br := tx.SendBatch(context.Background(), batch)
	err = br.Close()

	// Batch should have failed
	if err == nil {
		t.Fatal("expected batch to fail due to constraint violation")
	}

	// Explicitly rollback the failed transaction
	_ = tx.Rollback(context.Background())

	// Verify status_changes has NO data (CopyFrom was rolled back)
	// Query using a new connection, not the failed transaction
	count := db.MustInt(
		"select count(*) from status_changes where streamer_id = $1",
		streamerIntID,
	)
	if count != 0 {
		t.Errorf("expected 0 status_changes after rollback, got %d", count)
	}
}

func TestUnconfirmedStatusConsistency(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Insert first status change for streamer "a"
	db.UpsertUnconfirmedStatusChanges([]StatusChange{
		{Nickname: "a", Status: cmdlib.StatusOnline},
	}, 100)

	streamer := db.MaybeStreamer("a")
	if streamer == nil {
		t.Fatal("streamer not found")
	}
	if streamer.UnconfirmedStatus != cmdlib.StatusOnline || streamer.UnconfirmedTimestamp != 100 {
		t.Errorf("unexpected unconfirmed status: %+v", streamer)
	}
	if streamer.PrevUnconfirmedStatus != cmdlib.StatusUnknown || streamer.PrevUnconfirmedTimestamp != 0 {
		t.Errorf("unexpected prev unconfirmed status: %+v", streamer)
	}

	// Insert second status change — prev should be updated
	db.UpsertUnconfirmedStatusChanges([]StatusChange{
		{Nickname: "a", Status: cmdlib.StatusOffline},
	}, 200)

	streamer = db.MaybeStreamer("a")
	if streamer.UnconfirmedStatus != cmdlib.StatusOffline || streamer.UnconfirmedTimestamp != 200 {
		t.Errorf("unexpected unconfirmed status: %+v", streamer)
	}
	if streamer.PrevUnconfirmedStatus != cmdlib.StatusOnline || streamer.PrevUnconfirmedTimestamp != 100 {
		t.Errorf("unexpected prev unconfirmed status: %+v", streamer)
	}

	// Insert third status change — prev should shift
	db.UpsertUnconfirmedStatusChanges([]StatusChange{
		{Nickname: "a", Status: cmdlib.StatusOnline},
	}, 300)

	streamer = db.MaybeStreamer("a")
	if streamer.UnconfirmedStatus != cmdlib.StatusOnline || streamer.UnconfirmedTimestamp != 300 {
		t.Errorf("unexpected unconfirmed status: %+v", streamer)
	}
	if streamer.PrevUnconfirmedStatus != cmdlib.StatusOffline || streamer.PrevUnconfirmedTimestamp != 200 {
		t.Errorf("unexpected prev unconfirmed status: %+v", streamer)
	}
}

func TestConfirmSub(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Insert pending subscription
	db.AddUser(1, 0, 0, "")
	insertPendingSubscription(db.Database, "test", 1, "a", false)

	// Confirm the subscription
	streamerID := db.ConfirmSub(PendingSubscription{Endpoint: "test", UserID: 1, Nickname: "a"})

	// Check returned streamer ID is valid
	if streamerID == 0 {
		t.Error("expected non-zero streamer ID from ConfirmSub")
	}

	// Check subscription was moved to subscriptions table
	if db.MustInt(`
		select count(*) from subscriptions
		where streamer_id = $1`, streamerID) != 1 {
		t.Error("expected subscription after ConfirmSub")
	}

	// Check pending subscription was deleted
	if db.MustInt("select count(*) from pending_subscriptions where nickname = $1", "a") != 0 {
		t.Error("expected pending subscription to be deleted after ConfirmSub")
	}

	// Check streamer was created
	if db.MaybeStreamer("a") == nil {
		t.Error("expected streamer to exist after ConfirmSub")
	}

	if db.MustInt("select count(*) from nicknames where nickname = $1", "a") != 1 {
		t.Error("expected the new streamer in nicknames")
	}
}

func TestDenySub(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Insert pending subscription
	db.AddUser(1, 0, 0, "")
	insertPendingSubscription(db.Database, "test", 1, "b", false)

	// Deny the subscription
	db.DenySub(PendingSubscription{Endpoint: "test", UserID: 1, Nickname: "b"})

	// Check pending subscription is deleted
	if db.MustInt("select count(*) from pending_subscriptions where nickname = $1", "b") != 0 {
		t.Error("expected pending subscription to be deleted after DenySub")
	}
}

func TestStatusConfirmations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		confirmed   cmdlib.StatusKind
		unconfirmed cmdlib.StatusKind
		timestamp   int
		now         int
		expect      *cmdlib.StatusKind // nil means no confirmation expected
	}{
		{
			name:        "offline to online confirms immediately",
			confirmed:   cmdlib.StatusOffline,
			unconfirmed: cmdlib.StatusOnline,
			timestamp:   100,
			now:         100,
			expect:      ptr(cmdlib.StatusOnline),
		},
		{
			name:        "online to offline confirms when timing met",
			confirmed:   cmdlib.StatusOnline,
			unconfirmed: cmdlib.StatusOffline,
			timestamp:   100,
			now:         105,
			expect:      ptr(cmdlib.StatusOffline),
		},
		{
			name:        "online to offline not confirmed when timing not met",
			confirmed:   cmdlib.StatusOnline,
			unconfirmed: cmdlib.StatusOffline,
			timestamp:   103,
			now:         105,
			expect:      nil,
		},
		{
			name:        "online to unknown confirms immediately",
			confirmed:   cmdlib.StatusOnline,
			unconfirmed: cmdlib.StatusUnknown,
			timestamp:   100,
			now:         100,
			expect:      ptr(cmdlib.StatusUnknown),
		},
		{
			name:        "offline to unknown confirms immediately",
			confirmed:   cmdlib.StatusOffline,
			unconfirmed: cmdlib.StatusUnknown,
			timestamp:   100,
			now:         100,
			expect:      ptr(cmdlib.StatusUnknown),
		},
		{
			name:        "same status online no change",
			confirmed:   cmdlib.StatusOnline,
			unconfirmed: cmdlib.StatusOnline,
			timestamp:   100,
			now:         105,
			expect:      nil,
		},
		{
			name:        "same status offline no change",
			confirmed:   cmdlib.StatusOffline,
			unconfirmed: cmdlib.StatusOffline,
			timestamp:   100,
			now:         105,
			expect:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			defer db.terminate()

			// Set up background streamers that should remain unchanged
			insertStreamerRow(db.Database, Streamer{
				Nickname:             "always_online",
				ConfirmedStatus:      cmdlib.StatusOnline,
				UnconfirmedStatus:    cmdlib.StatusOnline,
				UnconfirmedTimestamp: 100,
			})
			insertStreamerRow(db.Database, Streamer{
				Nickname:             "always_offline",
				ConfirmedStatus:      cmdlib.StatusOffline,
				UnconfirmedStatus:    cmdlib.StatusOffline,
				UnconfirmedTimestamp: 100,
			})
			insertStreamerRow(db.Database, Streamer{
				Nickname:             "always_unknown",
				ConfirmedStatus:      cmdlib.StatusUnknown,
				UnconfirmedStatus:    cmdlib.StatusUnknown,
				UnconfirmedTimestamp: 100,
			})
			insertStreamerRow(db.Database, Streamer{
				Nickname:             "ch",
				ConfirmedStatus:      tt.confirmed,
				UnconfirmedStatus:    tt.unconfirmed,
				UnconfirmedTimestamp: tt.timestamp,
			})

			// Online confirms at once, offline after 5 seconds
			changes := db.ConfirmStatusChanges(tt.now, 0, 5)

			if tt.expect == nil {
				if len(changes) != 0 {
					t.Errorf("expected no confirmation, got %v", changes)
				}
			} else {
				if len(changes) != 1 || changes[0].Status != *tt.expect {
					t.Errorf("expected %v confirmation, got %v", *tt.expect, changes)
				}
			}

			// Verify background streamers were not affected
			if s := db.MustInt("select confirmed_status from streamers where nickname = $1", "always_online"); s != int(cmdlib.StatusOnline) {
				t.Errorf("always_online confirmed_status was affected, got %v", s)
			}
			if s := db.MustInt("select confirmed_status from streamers where nickname = $1", "always_offline"); s != int(cmdlib.StatusOffline) {
				t.Errorf("always_offline confirmed_status was affected, got %v", s)
			}
			if s := db.MustInt("select confirmed_status from streamers where nickname = $1", "always_unknown"); s != int(cmdlib.StatusUnknown) {
				t.Errorf("always_unknown confirmed_status was affected, got %v", s)
			}
		})
	}
}

// TestAddUserReportsCreation pins the created flag the referral bonus gates on:
// a new chat gets what it was given, and a known one keeps its row
func TestAddUserReportsCreation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Each step acts on what the steps before it left
	steps := []struct {
		name          string
		chatID        int64
		maxSubs       int
		now           int
		chatType      string
		wantCreated   bool
		wantMaxSubs   int
		wantCreatedAt int
		wantChatType  string
	}{
		{"new chat", 1, 5, 1000, "private", true, 5, 1000, "private"},
		{"known chat keeps its row", 1, 9, 2000, "group", false, 5, 1000, "private"},
		{"another new chat", 2, 7, 3000, "group", true, 7, 3000, "group"},
	}
	ids := map[int64]UserID{}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			id, created := db.AddUser(tt.chatID, tt.maxSubs, tt.now, tt.chatType)
			if created != tt.wantCreated {
				t.Errorf("created = %v, want %v", created, tt.wantCreated)
			}
			if known, ok := ids[tt.chatID]; ok && id != known {
				t.Errorf("id = %d, want the known %d", id, known)
			}
			ids[tt.chatID] = id
			var maxSubs, createdAt int
			var chatType string
			db.MaybeRecord(
				"select max_subs, created_at, chat_type from users where id = $1 and chat_id = $2",
				QueryParams{int64(id), tt.chatID},
				ScanTo{&maxSubs, &createdAt, &chatType})
			if maxSubs != tt.wantMaxSubs || createdAt != tt.wantCreatedAt || chatType != tt.wantChatType {
				t.Errorf(
					"stored (%d, %d, %q), want (%d, %d, %q)",
					maxSubs, createdAt, chatType,
					tt.wantMaxSubs, tt.wantCreatedAt, tt.wantChatType)
			}
		})
	}
}

func TestEnsureUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	before := time.Now().Unix()
	created := db.EnsureUser(1)
	user, found := db.UserByID(created)
	if !found || user.ChatID != 1 || user.MaxSubs != 5 || user.ChatType != nil || user.CreatedAt < before {
		t.Errorf("created %+v, found: %v, want chat 1 at the default limit, untyped, stamped now", user, found)
	}
	if id := db.EnsureUser(1); id != created {
		t.Errorf("EnsureUser of a created chat = %d, want %d", id, created)
	}
	known, _ := db.AddUser(2, 9, 1000, "group")
	if id := db.EnsureUser(2); id != known {
		t.Errorf("EnsureUser of a known chat = %d, want %d", id, known)
	}
	if limit := db.MustInt("select max_subs from users where id = $1", int64(known)); limit != 9 {
		t.Errorf("a known chat's limit = %d, want its own 9", limit)
	}
	if n := db.MustInt("select count(*) from users"); n != 2 {
		t.Errorf("%d users, want 2", n)
	}
}

// userRow reads a user's whole row past the readers under test; numbers come as float64
func userRow(d *Database, id UserID) map[string]any {
	var row map[string]any
	d.MaybeRecord("select to_jsonb(u) from users u where id = $1", QueryParams{int64(id)}, ScanTo{&row})
	return row
}

// TestUserSettersWriteTheirColumn pins each setter to its column on its user alone
func TestUserSettersWriteTheirColumn(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	target, _ := db.AddUser(1, 5, 1000, "private")
	bystander, _ := db.AddUser(2, 5, 1000, "private")

	// Each step acts on what the steps before it left
	steps := []struct {
		name   string
		set    func()
		column string
		want   any
	}{
		{"SetLimit", func() { db.SetLimit(target, 42) }, "max_subs", 42.0},
		{"AddReferrerBonus", func() { db.AddReferrerBonus(target, 3) }, "max_subs", 45.0},
		{"SetShowImages off", func() { db.SetShowImages(target, false) }, "show_images", false},
		{"SetShowImages on", func() { db.SetShowImages(target, true) }, "show_images", true},
		{
			"SetOfflineNotifications off",
			func() { db.SetOfflineNotifications(target, false) },
			"offline_notifications",
			false,
		},
		{
			"SetOfflineNotifications on",
			func() { db.SetOfflineNotifications(target, true) },
			"offline_notifications",
			true,
		},
		{"SetShowSubject off", func() { db.SetShowSubject(target, false) }, "show_subject", false},
		{"SetShowSubject on", func() { db.SetShowSubject(target, true) }, "show_subject", true},
		{"SetSilentMessages on", func() { db.SetSilentMessages(target, true) }, "silent_messages", true},
		{"SetSilentMessages off", func() { db.SetSilentMessages(target, false) }, "silent_messages", false},
		{
			"SetMemberSubscriptions on",
			func() { db.SetMemberSubscriptions(target, true) },
			"member_subscriptions",
			true,
		},
		{
			"SetMemberSubscriptions off",
			func() { db.SetMemberSubscriptions(target, false) },
			"member_subscriptions",
			false,
		},
		{"BlacklistUser", func() { db.BlacklistUser(target) }, "blacklist", true},
		{"SetTimezone", func() { db.SetTimezone(target, "Europe/Berlin") }, "timezone", "Europe/Berlin"},
		{"SetTimezone clears on empty", func() { db.SetTimezone(target, "") }, "timezone", nil},
		{
			"SetAffiliateParams",
			func() { db.SetAffiliateParams(target, map[string]string{"campaign": "a"}) },
			"affiliate_params",
			map[string]any{"campaign": "a"},
		},
		{"SetAffiliateParams clears on empty", func() { db.SetAffiliateParams(target, nil) }, "affiliate_params", nil},
		{"UpdateMemberCount", func() { db.UpdateMemberCount(target, 7) }, "member_count", 7.0},
		{
			"IncrementReports counts duplicates",
			func() { db.IncrementReports([]UserID{target, target}) },
			"reports",
			2.0,
		},
	}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			wantTarget, wantBystander := userRow(db.Database, target), userRow(db.Database, bystander)
			wantTarget[tt.column] = tt.want
			tt.set()
			if got := userRow(db.Database, target); !reflect.DeepEqual(got, wantTarget) {
				t.Errorf("target = %v, want %v", got, wantTarget)
			}
			if got := userRow(db.Database, bystander); !reflect.DeepEqual(got, wantBystander) {
				t.Errorf("bystander = %v, want %v", got, wantBystander)
			}
		})
	}
}

func TestIncrementReportsPerUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	a, _ := db.AddUser(1, 5, 0, "")
	b, _ := db.AddUser(2, 5, 0, "")
	c, _ := db.AddUser(3, 5, 0, "")

	db.IncrementReports([]UserID{a, b, a})
	db.IncrementReports([]UserID{a})

	got := map[UserID]int{}
	var id UserID
	var reports int
	db.MustQuery("select id, reports from users", nil, ScanTo{&id, &reports}, func() { got[id] = reports })
	if want := (map[UserID]int{a: 3, b: 1, c: 0}); !reflect.DeepEqual(got, want) {
		t.Errorf("reports = %v, want %v", got, want)
	}
}

// TestUserReadersReadEveryColumn holds both user readers to the row stored
func TestUserReadersReadEveryColumn(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Each flag has its own pattern across the rows, so swapping any two shows
	users := []User{
		{
			ChatID:               1,
			MaxSubs:              11,
			Reports:              21,
			Blacklist:            true,
			OfflineNotifications: true,
			SilentMessages:       true,
			CreatedAt:            31,
			ChatType:             ptr("private"),
			MemberCount:          ptr(41),
			AffiliateParams:      map[string]string{"campaign": "a"},
			Timezone:             ptr("Europe/Berlin"),
		},
		{
			ChatID:               2,
			MaxSubs:              12,
			Reports:              22,
			ShowImages:           true,
			OfflineNotifications: true,
			CreatedAt:            32,
			ChatType:             ptr("group"),
			MemberCount:          ptr(42),
			AffiliateParams:      map[string]string{"campaign": "b"},
			MemberSubscriptions:  true,
			Timezone:             ptr("Asia/Tokyo"),
		},
		// No nullable column set
		{
			ChatID:              3,
			MaxSubs:             13,
			Reports:             23,
			ShowSubject:         true,
			SilentMessages:      true,
			CreatedAt:           33,
			MemberSubscriptions: true,
		},
	}
	for i, u := range users {
		users[i].UserID = UserID(db.MustInt(`
			insert into users (
				chat_id,
				max_subs,
				reports,
				blacklist,
				show_images,
				offline_notifications,
				show_subject,
				silent_messages,
				created_at,
				chat_type,
				member_count,
				affiliate_params,
				member_subscriptions,
				timezone)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			returning id`,
			u.ChatID,
			u.MaxSubs,
			u.Reports,
			u.Blacklist,
			u.ShowImages,
			u.OfflineNotifications,
			u.ShowSubject,
			u.SilentMessages,
			u.CreatedAt,
			u.ChatType,
			u.MemberCount,
			u.AffiliateParams,
			u.MemberSubscriptions,
			u.Timezone))
	}

	readers := []struct {
		name string
		read func(u User) (User, bool)
	}{
		{"UserByID", func(u User) (User, bool) { return db.UserByID(u.UserID) }},
		{"UserByChatID", func(u User) (User, bool) { return db.UserByChatID(u.ChatID) }},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			for _, want := range users {
				if got, found := reader.read(want); !found || !reflect.DeepEqual(got, want) {
					t.Errorf("read %+v, found: %v, want %+v", got, found, want)
				}
			}
			if got, found := reader.read(User{UserID: 999, ChatID: 999}); found {
				t.Errorf("read %+v of an absent user", got)
			}
		})
	}
}

func TestSubscriptionReads(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	alice, _ := db.AddUser(1, 5, 0, "private")
	bob, _ := db.AddUser(2, 5, 0, "private")
	a := Streamer{
		Nickname:                 "a",
		UnconfirmedStatus:        cmdlib.StatusOnline,
		UnconfirmedTimestamp:     400,
		PrevUnconfirmedStatus:    cmdlib.StatusOffline,
		PrevUnconfirmedTimestamp: 300,
	}
	b := Streamer{
		Nickname:                 "b",
		UnconfirmedStatus:        cmdlib.StatusOffline,
		UnconfirmedTimestamp:     600,
		PrevUnconfirmedStatus:    cmdlib.StatusOnline,
		PrevUnconfirmedTimestamp: 500,
	}
	c := Streamer{
		Nickname:                 "c",
		UnconfirmedStatus:        cmdlib.StatusUnknown,
		UnconfirmedTimestamp:     800,
		PrevUnconfirmedStatus:    cmdlib.StatusOffline,
		PrevUnconfirmedTimestamp: 700,
	}
	a.ID = insertStreamerRow(db.Database, a)
	b.ID = insertStreamerRow(db.Database, b)
	c.ID = insertStreamerRow(db.Database, c)
	insertStreamerRow(db.Database, Streamer{Nickname: "nobody's", UnconfirmedStatus: cmdlib.StatusOnline})
	// Added out of order, and with a nickname both subscribed and pending
	db.AddSubscription(alice, b.ID, "ep1")
	db.AddSubscription(alice, a.ID, "ep1")
	db.AddSubscription(alice, c.ID, "ep2")
	db.AddSubscription(bob, c.ID, "ep1")
	db.AddPendingSubscription(alice, "aa", "ep1", false, "", 0)
	db.AddPendingSubscription(alice, "a", "ep1", false, "", 0)
	db.AddPendingSubscription(alice, "q", "ep2", false, "", 0)
	db.AddPendingSubscription(bob, "r", "ep1", false, "", 0)

	lists := []struct {
		name          string
		endpoint      string
		user          UserID
		wantStreamers []Streamer
		wantNicknames []string
		wantCount     int
	}{
		{"alice on ep1", "ep1", alice, []Streamer{a, b}, []string{"a", "aa", "b"}, 4},
		{"alice on ep2", "ep2", alice, []Streamer{c}, []string{"c", "q"}, 2},
		{"bob on ep1", "ep1", bob, []Streamer{c}, []string{"c", "r"}, 2},
		{"bob on ep2", "ep2", bob, nil, nil, 0},
	}
	for _, tt := range lists {
		t.Run(tt.name, func(t *testing.T) {
			var wantIDs []Streamer
			for _, s := range tt.wantStreamers {
				wantIDs = append(wantIDs, Streamer{ID: s.ID, Nickname: s.Nickname})
			}
			if got := db.StreamersForUser(tt.endpoint, tt.user); !reflect.DeepEqual(got, wantIDs) {
				t.Errorf("StreamersForUser = %+v, want %+v", got, wantIDs)
			}
			if got := db.UnconfirmedStatusesForUser(tt.endpoint, tt.user); !reflect.DeepEqual(got, tt.wantStreamers) {
				t.Errorf("UnconfirmedStatusesForUser = %+v, want %+v", got, tt.wantStreamers)
			}
			if got := db.SubscribedOrPendingNicknames(tt.endpoint, tt.user); !reflect.DeepEqual(got, tt.wantNicknames) {
				t.Errorf("SubscribedOrPendingNicknames = %v, want %v", got, tt.wantNicknames)
			}
			if got := db.SubscribedOrPendingCount(tt.endpoint, tt.user); got != tt.wantCount {
				t.Errorf("SubscribedOrPendingCount = %d, want %d", got, tt.wantCount)
			}
		})
	}

	lookups := []struct {
		name     string
		endpoint string
		user     UserID
		nickname string
		want     bool
	}{
		{"subscribed", "ep1", alice, "b", true},
		{"pending", "ep1", alice, "aa", true},
		{"subscribed on another endpoint", "ep2", alice, "b", false},
		{"pending on another endpoint", "ep2", alice, "aa", false},
		{"another user's subscription", "ep1", bob, "b", false},
		{"another user's pending", "ep1", alice, "r", false},
		{"nobody's", "ep1", alice, "nobody's", false},
	}
	for _, tt := range lookups {
		t.Run("SubscribedOrPending "+tt.name, func(t *testing.T) {
			if got := db.SubscribedOrPending(tt.endpoint, tt.user, tt.nickname); got != tt.want {
				t.Errorf("SubscribedOrPending = %v, want %v", got, tt.want)
			}
		})
	}

	want := map[string]bool{"a": true, "b": true, "c": true}
	if got := db.SubscribedStreamers(); !reflect.DeepEqual(got, want) {
		t.Errorf("SubscribedStreamers = %v, want %v", got, want)
	}
}

func TestRemoveSubscriptions(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	alice, _ := db.AddUser(1, 5, 0, "private")
	db.AddUser(2, 5, 0, "private")
	db.addSub(1, "a", "ep1")
	db.addSub(1, "b", "ep1")
	db.addSub(1, "a", "ep2")
	db.addSub(2, "a", "ep1")
	db.addPending(1, "p", "ep1")
	db.addPending(1, "p", "ep2")
	db.addPending(2, "p", "ep1")

	subs := func() []string {
		return db.MustStrings(`
			select u.chat_id || ' ' || sub.endpoint || ' ' || s.nickname
			from subscriptions sub
			join users u on u.id = sub.user_id
			join streamers s on s.id = sub.streamer_id
			order by 1`)
	}
	pending := func() []string {
		return db.MustStrings(`
			select u.chat_id || ' ' || ps.endpoint || ' ' || ps.nickname
			from pending_subscriptions ps
			join users u on u.id = ps.user_id
			order by 1`)
	}

	// Each step acts on what the steps before it left
	steps := []struct {
		name        string
		act         func()
		wantSubs    []string
		wantPending []string
	}{
		{
			"RemoveSubscription takes a subscription",
			func() { db.RemoveSubscription(alice, "a", "ep1") },
			[]string{"1 ep1 b", "1 ep2 a", "2 ep1 a"},
			[]string{"1 ep1 p", "1 ep2 p", "2 ep1 p"},
		},
		{
			"RemoveSubscription takes a pending one",
			func() { db.RemoveSubscription(alice, "p", "ep1") },
			[]string{"1 ep1 b", "1 ep2 a", "2 ep1 a"},
			[]string{"1 ep2 p", "2 ep1 p"},
		},
		{
			"RemoveAllSubscriptions takes one endpoint of one user",
			func() { db.RemoveAllSubscriptions(alice, "ep2") },
			[]string{"1 ep1 b", "2 ep1 a"},
			[]string{"2 ep1 p"},
		},
	}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			tt.act()
			if got := subs(); !reflect.DeepEqual(got, tt.wantSubs) {
				t.Errorf("subscriptions = %q, want %q", got, tt.wantSubs)
			}
			if got := pending(); !reflect.DeepEqual(got, tt.wantPending) {
				t.Errorf("pending subscriptions = %q, want %q", got, tt.wantPending)
			}
		})
	}
}

// TestPendingSubscriptionChecks walks pending subscriptions through a check
func TestPendingSubscriptionChecks(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	alice, _ := db.AddUser(1, 5, 0, "private")
	bob, _ := db.AddUser(-2, 5, 0, "group")
	aliceOnA := PendingSubscription{
		UserID:   alice,
		Nickname: "a",
		Endpoint: "ep1",
		ChatID:   1,
		Referral: true,
		Command:  "add",
		ReplySeq: 3,
	}
	bobOnA := PendingSubscription{UserID: bob, Nickname: "a", Endpoint: "ep2", ChatID: -2}
	aliceOnB := PendingSubscription{UserID: alice, Nickname: "b", Endpoint: "ep1", ChatID: 1, Command: "start"}
	bobOnC := PendingSubscription{UserID: bob, Nickname: "c", Endpoint: "ep2", ChatID: -2, ReplySeq: 1}
	add := func(subs ...PendingSubscription) func() {
		return func() {
			for _, s := range subs {
				db.AddPendingSubscription(s.UserID, s.Nickname, s.Endpoint, s.Referral, s.Command, s.ReplySeq)
			}
		}
	}

	// Each step acts on what the steps before it left
	steps := []struct {
		name            string
		act             func()
		wantUnconfirmed map[string]bool
		wantChecking    map[string][]PendingSubscription
	}{
		{
			"new ones wait for a check",
			add(aliceOnA, bobOnA, aliceOnB),
			map[string]bool{"a": true, "b": true},
			map[string][]PendingSubscription{},
		},
		{
			"marking puts them under check",
			db.MarkUnconfirmedAsChecking,
			map[string]bool{},
			map[string][]PendingSubscription{"a": {aliceOnA, bobOnA}, "b": {aliceOnB}},
		},
		{
			"one added later waits alone",
			add(bobOnC),
			map[string]bool{"c": true},
			map[string][]PendingSubscription{"a": {aliceOnA, bobOnA}, "b": {aliceOnB}},
		},
		{
			"a reset brings them all back",
			db.ResetCheckingToUnconfirmed,
			map[string]bool{"a": true, "b": true, "c": true},
			map[string][]PendingSubscription{},
		},
	}
	checking := func(nicknames ...string) map[string][]PendingSubscription {
		subs := db.CheckingSubs(nicknames)
		for _, s := range subs {
			slices.SortFunc(s, func(x, y PendingSubscription) int { return cmp.Compare(x.UserID, y.UserID) })
		}
		return subs
	}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			tt.act()
			if got := db.UnconfirmedSubs(); !reflect.DeepEqual(got, tt.wantUnconfirmed) {
				t.Errorf("UnconfirmedSubs = %v, want %v", got, tt.wantUnconfirmed)
			}
			if got := checking("a", "b", "c"); !reflect.DeepEqual(got, tt.wantChecking) {
				t.Errorf("CheckingSubs = %+v, want %+v", got, tt.wantChecking)
			}
		})
	}

	db.MarkUnconfirmedAsChecking()
	want := map[string][]PendingSubscription{"b": {aliceOnB}}
	if got := checking("b"); !reflect.DeepEqual(got, want) {
		t.Errorf("CheckingSubs of one nickname = %+v, want %+v", got, want)
	}
	if n := db.MustInt("select count(*) from pending_subscriptions where command is null"); n != 2 {
		t.Errorf("%d pending subscriptions store a null command, want the 2 with none", n)
	}
}

func TestStreamerReads(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// The confirmed status differs from the unconfirmed one, which alone decides these reads
	a := Streamer{
		Nickname:                 "a",
		ConfirmedStatus:          cmdlib.StatusOffline,
		UnconfirmedStatus:        cmdlib.StatusOnline,
		UnconfirmedTimestamp:     400,
		PrevUnconfirmedStatus:    cmdlib.StatusUnknown,
		PrevUnconfirmedTimestamp: 300,
	}
	b := Streamer{
		Nickname:                 "b",
		ConfirmedStatus:          cmdlib.StatusOnline,
		UnconfirmedStatus:        cmdlib.StatusOffline,
		UnconfirmedTimestamp:     600,
		PrevUnconfirmedStatus:    cmdlib.StatusUnknown,
		PrevUnconfirmedTimestamp: 500,
	}
	c := Streamer{
		Nickname:                 "c",
		ConfirmedStatus:          cmdlib.StatusOnline,
		UnconfirmedStatus:        cmdlib.StatusUnknown,
		UnconfirmedTimestamp:     800,
		PrevUnconfirmedStatus:    cmdlib.StatusOnline,
		PrevUnconfirmedTimestamp: 700,
	}
	a.ID = insertStreamerRow(db.Database, a)
	b.ID = insertStreamerRow(db.Database, b)
	c.ID = insertStreamerRow(db.Database, c)
	db.SetPoll("b", true)
	db.SetPoll("a", true)

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"OnlineStreamers", db.OnlineStreamers(), map[string]bool{"a": true}},
		{"KnownStreamers", db.KnownStreamers(), map[string]bool{"a": true, "b": true}},
		{"PolledStreamersWithStatus", db.PolledStreamersWithStatus(), []Streamer{a, b}},
		{
			"UnconfirmedStatusesForStreamers",
			db.UnconfirmedStatusesForStreamers([]string{"a", "c", "absent"}),
			map[string]StatusChange{
				"a": {Nickname: "a", Status: cmdlib.StatusOnline, Timestamp: 400},
				"c": {Nickname: "c", Status: cmdlib.StatusUnknown, Timestamp: 800},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Errorf("got %+v, want %+v", tt.got, tt.want)
			}
		})
	}
}

func TestIncrementPollErrors(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	for _, nickname := range []string{"a", "b", "c"} {
		db.ensureStreamer(nickname)
	}

	db.IncrementPollErrors([]string{"a", "b"})
	db.IncrementPollErrors([]string{"a", "absent"})

	got := map[string]int{}
	var nickname string
	var errorCount int
	db.MustQuery(
		"select nickname, poll_error_count from streamers",
		nil,
		ScanTo{&nickname, &errorCount},
		func() { got[nickname] = errorCount })
	if want := (map[string]int{"a": 2, "b": 1, "c": 0}); !reflect.DeepEqual(got, want) {
		t.Errorf("poll errors = %v, want %v", got, want)
	}
}

func TestUsersForStreamers(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Each flag has its own pattern across the users, so swapping any two shows
	users := []User{
		{ChatID: 1, OfflineNotifications: true, ShowImages: true},
		{ChatID: 2, OfflineNotifications: true, ShowSubject: true},
		{ChatID: 3, ShowImages: true, ShowSubject: true},
	}
	for i, u := range users {
		users[i].UserID, _ = db.AddUser(u.ChatID, 5, 0, "private")
		db.SetOfflineNotifications(users[i].UserID, u.OfflineNotifications)
		db.SetShowImages(users[i].UserID, u.ShowImages)
		db.SetShowSubject(users[i].UserID, u.ShowSubject)
	}
	db.addSub(1, "a", "ep1")
	db.addSub(1, "a", "ep2")
	db.addSub(1, "b", "ep1")
	db.addSub(2, "a", "ep1")
	db.addSub(3, "c", "ep1")
	db.addBlock(1, "ep1", 3)
	// On an endpoint this user has no subscription on
	db.addBlock(2, "ep2", 9)
	a, b := db.MaybeStreamer("a").ID, db.MaybeStreamer("b").ID

	type recipient struct {
		User
		endpoint string
	}
	blockedOnEP1 := users[0]
	blockedOnEP1.Blocked = 3
	want := map[int][]recipient{
		a: {{blockedOnEP1, "ep1"}, {users[0], "ep2"}, {users[1], "ep1"}},
		b: {{blockedOnEP1, "ep1"}},
	}

	gotUsers, gotEndpoints := db.UsersForStreamers([]int{a, b})

	got := map[int][]recipient{}
	for id := range gotUsers {
		for i, u := range gotUsers[id] {
			got[id] = append(got[id], recipient{u, gotEndpoints[id][i]})
		}
		slices.SortFunc(got[id], func(x, y recipient) int {
			return cmp.Or(cmp.Compare(x.ChatID, y.ChatID), cmp.Compare(x.endpoint, y.endpoint))
		})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recipients = %+v, want %+v", got, want)
	}
}

// TestNotificationQueueStates walks queued notifications through fetches, requeues and deletes
func TestNotificationQueueStates(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	user, _ := db.AddUser(1, 5, 0, "private")
	streamer := insertStreamerRow(db.Database, Streamer{Nickname: "a"})
	db.StoreNotifications([]Notification{
		{Endpoint: "ep", UserID: user, StreamerID: &streamer},
		{Endpoint: "ep", UserID: user, StreamerID: &streamer},
		{Endpoint: "ep", UserID: user, StreamerID: &streamer},
	})
	ids := db.MustInts("select id from notification_queue order by id")
	first, second, third := ids[0], ids[1], ids[2]

	// Each step acts on what the steps before it left
	steps := []struct {
		name string
		act  func()
		// wantFetched is what the fetch after the act returns
		wantFetched []int
		wantQueued  []int
	}{
		{"a fetch takes them all", func() {}, []int{first, second, third}, []int{first, second, third}},
		{"a fetch finds none while they are sent", func() {}, nil, []int{first, second, third}},
		{
			"RequeueNotification puts one back",
			func() { db.RequeueNotification(second) },
			[]int{second},
			[]int{first, second, third},
		},
		{"DeleteNotification removes one", func() { db.DeleteNotification(first) }, nil, []int{second, third}},
		{
			"ResetNotificationSending puts all back",
			db.ResetNotificationSending,
			[]int{second, third},
			[]int{second, third},
		},
	}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			tt.act()
			var fetched []int
			for _, n := range db.NewNotifications() {
				fetched = append(fetched, n.ID)
			}
			if !reflect.DeepEqual(fetched, tt.wantFetched) {
				t.Errorf("fetched %v, want %v", fetched, tt.wantFetched)
			}
			got := db.MustInts("select id from notification_queue order by id")
			if !reflect.DeepEqual(got, tt.wantQueued) {
				t.Errorf("queued %v, want %v", got, tt.wantQueued)
			}
		})
	}
}

func TestSentMessageLog(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	a, _ := db.AddUser(1, 5, 0, "")
	b, _ := db.AddUser(2, 5, 0, "")

	type row struct {
		Timestamp int
		UserID    UserID
		Result    int
		Endpoint  string
		Priority  Priority
		Latency   int
		Kind      PacketKind
		Command   *string
		ReplySeq  int
		ImageMode ImageMode
	}
	tests := []struct {
		name      string
		log       func()
		timestamp int
		want      []row
	}{
		{
			name:      "a command is stored by name",
			log:       func() { db.LogSentMessage(1000, a, 200, "ep", PriorityLow, 77, ReplyPacket, "add", 3, ImageUpload) },
			timestamp: 1000,
			want:      []row{{1000, a, 200, "ep", PriorityLow, 77, ReplyPacket, ptr("add"), 3, ImageUpload}},
		},
		{
			name:      "no command is stored as null",
			log:       func() { db.LogSentMessage(2000, b, 403, "ep2", PriorityHigh, 88, AdPacket, "", 0, NoImage) },
			timestamp: 2000,
			want:      []row{{2000, b, 403, "ep2", PriorityHigh, 88, AdPacket, nil, 0, NoImage}},
		},
		{
			name: "dice-skipped sends take zero latency and no command",
			log: func() {
				skips := []SkippedSend{{UserID: a, Endpoint: "ep"}, {UserID: b, Endpoint: "ep2"}}
				db.LogDiceSkippedSends(3000, 429, PriorityLow, MessagePacket, skips)
			},
			timestamp: 3000,
			want: []row{
				{3000, a, 429, "ep", PriorityLow, 0, MessagePacket, nil, 0, NoImage},
				{3000, b, 429, "ep2", PriorityLow, 0, MessagePacket, nil, 0, NoImage},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.log()
			var got []row
			var r row
			db.MustQuery(`
				select timestamp, user_id, result, endpoint, priority, latency, kind, command, reply_seq, image_mode
				from sent_message_log
				where timestamp = $1
				order by user_id`,
				QueryParams{tt.timestamp},
				ScanTo{
					&r.Timestamp,
					&r.UserID,
					&r.Result,
					&r.Endpoint,
					&r.Priority,
					&r.Latency,
					&r.Kind,
					&r.Command,
					&r.ReplySeq,
					&r.ImageMode,
				},
				func() { got = append(got, r) })
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sent_message_log = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReceivedMessageLog(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	user, _ := db.AddUser(1, 5, 0, "")

	tests := []struct {
		name      string
		timestamp int
		command   string
		want      *string
	}{
		{"a command is stored by name", 1000, "start", ptr("start")},
		{"no command is stored as null", 2000, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db.LogReceivedMessage(tt.timestamp, "ep", user, tt.command)
			var endpoint string
			var userID UserID
			var command *string
			found := db.MaybeRecord(
				"select endpoint, user_id, command from received_message_log where timestamp = $1",
				QueryParams{tt.timestamp},
				ScanTo{&endpoint, &userID, &command})
			if !found || endpoint != "ep" || userID != user || !reflect.DeepEqual(command, tt.want) {
				t.Errorf("stored (%q, %d, %v), found: %v, want command %v", endpoint, userID, command, found, tt.want)
			}
		})
	}
}

func TestReferralCounts(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	alice, _ := db.AddUser(1, 5, 0, "private")
	bob, _ := db.AddUser(2, 5, 0, "private")

	if db.ReferralIDExists("alices") {
		t.Error("a key nobody took exists")
	}
	db.AddReferral(alice, "alices")
	db.AddReferral(bob, "bobs")
	if !db.ReferralIDExists("alices") {
		t.Error("a taken key does not exist")
	}
	if db.ReferralIDExists("nobodys") {
		t.Error("a key nobody took exists after others were taken")
	}
	if id := db.ReferralID(alice); id == nil || *id != "alices" {
		t.Errorf("ReferralID = %v, want alices", id)
	}
	if owner := db.UserByReferralID("alices"); owner == nil || *owner != alice {
		t.Errorf("UserByReferralID = %v, want %d", owner, alice)
	}
	if n, found := db.referredUsers(1); !found || n != 0 {
		t.Errorf("a new key's referred users = (%d, %v), want 0", n, found)
	}

	// Each step acts on what the steps before it left
	steps := []struct {
		name      string
		user      UserID
		wantAlice int
		wantBob   int
	}{
		{"alice refers one", alice, 1, 0},
		{"alice refers another", alice, 2, 0},
		{"bob counts apart", bob, 2, 1},
	}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			db.IncrementReferredUsers(tt.user)
			if n, found := db.referredUsers(1); !found || n != tt.wantAlice {
				t.Errorf("alice's referred users = (%d, %v), want %d", n, found, tt.wantAlice)
			}
			if n, found := db.referredUsers(2); !found || n != tt.wantBob {
				t.Errorf("bob's referred users = (%d, %v), want %d", n, found, tt.wantBob)
			}
		})
	}
}

func TestAddReferralEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	referrer, _ := db.AddUser(1, 5, 0, "private")
	follower, _ := db.AddUser(2, 5, 0, "private")
	// Past the users' ids, so the columns can't pass for one another
	streamer := db.MustInt("insert into streamers (id, nickname) overriding system value values (77, 'a') returning id")

	tests := []struct {
		name      string
		timestamp int
		referrer  *UserID
		streamer  *int
	}{
		{"a referrer and a streamer", 1000, &referrer, &streamer},
		{"neither", 2000, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db.AddReferralEvent(tt.timestamp, tt.referrer, follower, tt.streamer)
			var gotReferrer *UserID
			var gotFollower UserID
			var gotStreamer *int
			found := db.MaybeRecord(
				"select referrer_user_id, follower_user_id, streamer_id from referral_events where timestamp = $1",
				QueryParams{tt.timestamp},
				ScanTo{&gotReferrer, &gotFollower, &gotStreamer})
			if !found {
				t.Fatal("no event stored at this timestamp")
			}
			if !reflect.DeepEqual(gotReferrer, tt.referrer) {
				t.Errorf("referrer_user_id = %v, want %v", gotReferrer, tt.referrer)
			}
			if gotFollower != follower {
				t.Errorf("follower_user_id = %d, want %d", gotFollower, follower)
			}
			if !reflect.DeepEqual(gotStreamer, tt.streamer) {
				t.Errorf("streamer_id = %v, want %v", gotStreamer, tt.streamer)
			}
		})
	}
}

func TestAddFeedback(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()
	user, _ := db.AddUser(1, 5, 0, "private")

	db.AddFeedback("ep", user, "hello", 1000)

	var endpoint, text string
	var userID UserID
	var timestamp int
	found := db.MaybeRecord(
		"select endpoint, user_id, text, timestamp from feedback",
		nil,
		ScanTo{&endpoint, &userID, &text, &timestamp})
	if !found || endpoint != "ep" || userID != user || text != "hello" || timestamp != 1000 {
		t.Errorf("stored (%q, %d, %q, %d), found: %v", endpoint, userID, text, timestamp, found)
	}
}

func TestProfilePhotoHashes(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	type photo struct{ endpoint, hash string }
	// Each step acts on what the steps before it left
	steps := []struct {
		name string
		set  photo
		want map[photo]bool
	}{
		{
			"a first upload",
			photo{"ep1", "old"},
			map[photo]bool{{"ep1", "old"}: true, {"ep1", "new"}: false, {"ep2", "old"}: false},
		},
		{
			"another endpoint keeps its own",
			photo{"ep2", "other"},
			map[photo]bool{{"ep1", "old"}: true, {"ep2", "other"}: true, {"ep2", "old"}: false},
		},
		{
			"a new upload replaces the last",
			photo{"ep1", "new"},
			map[photo]bool{{"ep1", "new"}: true, {"ep1", "old"}: false, {"ep2", "other"}: true},
		},
	}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			db.SetProfilePhotoHash(tt.set.endpoint, tt.set.hash)
			for p, want := range tt.want {
				if got := db.ProfilePhotoUploaded(p.endpoint, p.hash); got != want {
					t.Errorf("ProfilePhotoUploaded(%q, %q) = %v, want %v", p.endpoint, p.hash, got, want)
				}
			}
		})
	}
}

// TestSearchStreamers pins each search leg:
// nicknames holds the rows the search indexes see, streamers those the exact and prefix legs see
func TestSearchStreamers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		term      string
		nicknames []string
		streamers []string
		want      []string
	}{
		{
			name:      "an exact match in streamers alone",
			term:      "wonderland",
			nicknames: []string{"bob"},
			streamers: []string{"wonderland"},
			want:      []string{"wonderland"},
		},
		{
			name:      "a substring of a nickname",
			term:      "smi",
			nicknames: []string{"alice_smith", "bob"},
			want:      []string{"alice_smith"},
		},
		{
			name:      "an underscore in a substring is literal",
			term:      "abc_de",
			nicknames: []string{"xabc_dey", "xabcxdey"},
			want:      []string{"xabc_dey"},
		},
		{
			name:      "a typo over the similarity threshold",
			term:      "wonderwxyz",
			nicknames: []string{"wonderland", "bob"},
			want:      []string{"wonderland"},
		},
		{
			name:      "a long repeated run",
			term:      "aaaaa",
			nicknames: []string{"xaaaaay", "xaaaay"},
			want:      []string{"xaaaaay"},
		},
		{
			name:      "a long non-alnum run of literal underscores",
			term:      "___",
			nicknames: []string{"x___y", "x__y", "x_-_y"},
			want:      []string{"x___y"},
		},
		{
			name:      "a short prefix in streamers alone",
			term:      "ab",
			nicknames: []string{"abz"},
			streamers: []string{"ab1", "xab"},
			want:      []string{"ab1"},
		},
		{
			name:      "an underscore in a prefix is literal",
			term:      "a_",
			streamers: []string{"a_b", "abb"},
			want:      []string{"a_b"},
		},
		{
			name:      "a character no nickname can hold finds nothing",
			term:      "Alice",
			nicknames: []string{"Alice", "alice"},
			streamers: []string{"Alice", "alice"},
		},
		{
			name: "the closest seven, each once",
			term: "rank",
			nicknames: []string{
				"rank1234567", "rank123456", "rank12345", "rank1234", "rank123", "rank12", "rank1", "rank",
			},
			streamers: []string{"rank"},
			want:      []string{"rank", "rank1", "rank12", "rank123", "rank1234", "rank12345", "rank123456"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			defer db.terminate()
			for _, nickname := range tt.nicknames {
				db.MustExec("insert into nicknames (nickname) values ($1)", nickname)
			}
			for _, nickname := range tt.streamers {
				db.ensureStreamer(nickname)
			}
			if got := db.SearchStreamers(tt.term); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SearchStreamers(%q) = %q, want %q", tt.term, got, tt.want)
			}
		})
	}
}
