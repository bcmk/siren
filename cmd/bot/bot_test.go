package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/bcmk/siren/v2/internal/db"
	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5"
)

func checkUnconfirmedOnlineStreamers(w *testWorker, t *testing.T) {
	dbOnline := map[string]bool{}
	var nickname string
	w.db.MustQuery(
		"select nickname from streamers where unconfirmed_status = $1",
		[]interface{}{cmdlib.StatusOnline},
		db.ScanTo{&nickname},
		func() { dbOnline[nickname] = true })
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

func TestSql(t *testing.T) {
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()
	for _, n := range []string{"a", "b", "c", "c2", "c3", "d", "e", "f", "g"} {
		insertTestStreamer(&w.db, db.Streamer{Nickname: n})
	}
	insertSubscription(&w.db, "ep1", 1, "a")
	insertSubscription(&w.db, "ep1", 2, "b")
	insertSubscription(&w.db, "ep1", 3, "c")
	insertSubscription(&w.db, "ep1", 3, "c2")
	insertSubscription(&w.db, "ep1", 3, "c3")
	insertSubscription(&w.db, "ep1", 4, "d")
	insertSubscription(&w.db, "ep1", 5, "d")
	insertSubscription(&w.db, "ep1", 6, "e")
	insertSubscription(&w.db, "ep1", 7, "f")
	insertSubscription(&w.db, "ep2", 6, "e")
	insertSubscription(&w.db, "ep2", 7, "f")
	insertSubscription(&w.db, "ep2", 8, "g")
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 2, 0)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 3, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 4, w.cfg.BlockThreshold-1)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 5, w.cfg.BlockThreshold+1)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 6, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 7, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep2", 7, w.cfg.BlockThreshold)
	w.db.MustExec("update streamers set confirmed_status = $2 where nickname = $1", "a", cmdlib.StatusOnline)
	w.db.MustExec("update streamers set confirmed_status = $2 where nickname = $1", "b", cmdlib.StatusOnline)
	w.db.MustExec("update streamers set confirmed_status = $2 where nickname = $1", "c", cmdlib.StatusOnline)
	w.db.MustExec("update streamers set confirmed_status = $2 where nickname = $1", "c2", cmdlib.StatusOnline)
	broadcastChats := w.db.BroadcastChats("ep1")
	if !reflect.DeepEqual(broadcastChats, []int64{1, 2, 3, 4, 5, 6, 7}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	broadcastChats = w.db.BroadcastChats("ep2")
	if !reflect.DeepEqual(broadcastChats, []int64{6, 7, 8}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	chatsForStreamer, endpoints := w.chatsForStreamer("a")
	if !reflect.DeepEqual(endpoints, []string{"ep1"}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	if !reflect.DeepEqual(chatsForStreamer, []int64{1}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = w.chatsForStreamer("b")
	if !reflect.DeepEqual(chatsForStreamer, []int64{2}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = w.chatsForStreamer("c")
	if !reflect.DeepEqual(chatsForStreamer, []int64{3}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = w.chatsForStreamer("d")
	if !reflect.DeepEqual(chatsForStreamer, []int64{4, 5}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = w.chatsForStreamer("e")
	if !reflect.DeepEqual(chatsForStreamer, []int64{6, 6}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	chatsForStreamer, _ = w.chatsForStreamer("f")
	if !reflect.DeepEqual(chatsForStreamer, []int64{7, 7}) {
		t.Error("unexpected chats for streamer result", chatsForStreamer)
	}
	w.db.IncrementBlock("ep1", 2)
	w.db.IncrementBlock("ep1", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep1") != 2 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	w.db.IncrementBlock("ep2", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	w.db.ResetBlock("ep1", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep1") != 0 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	w.db.IncrementBlock("ep1", 1)
	w.db.IncrementBlock("ep1", 1)
	if w.db.MustInt("select block from block where chat_id = $1", 1) != 2 {
		t.Error("unexpected block for streamer result", chatsForStreamer)
	}
	statuses := confirmedStatusesForChat(&w.db, "ep1", 3)
	if !reflect.DeepEqual(statuses, []db.Streamer{
		{Nickname: "c", ConfirmedStatus: cmdlib.StatusOnline},
		{Nickname: "c2", ConfirmedStatus: cmdlib.StatusOnline},
		{Nickname: "c3"}}) {
		t.Error("unexpected statuses", statuses)
	}
	_ = w.db.Close()
}

func TestUpdateNotifications(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	for _, n := range []string{"a", "b", "c", "d"} {
		insertTestStreamer(&w.db, db.Streamer{Nickname: n})
	}
	insertSubscription(&w.db, "ep1", 1, "a")
	insertSubscription(&w.db, "ep1", 2, "b")
	insertSubscription(&w.db, "ep1", 3, "a")
	insertSubscription(&w.db, "ep1", 3, "c")
	insertSubscription(&w.db, "ep1", 4, "d")
	insertSubscription(&w.db, "ep2", 4, "d")

	w.db.MustExec("insert into users (chat_id, created_at) values ($1, 0)", 1)
	w.db.MustExec("insert into users (chat_id, created_at) values ($1, 0)", 2)
	w.db.MustExec("insert into users (chat_id, created_at) values ($1, 0)", 3)
	w.db.MustExec("insert into users (chat_id, created_at) values ($1, 0)", 4)

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
	checkInv(&w.worker, t)

	// Streamer "a" goes offline — no notifications yet (needs 5s confirmation)
	result.Streamers = map[string]cmdlib.StreamerInfo{}
	if r := w.handleCheckerResults(result, 3); len(r.notifications) != 0 {
		t.Errorf("expected 0 notifications before offline confirmation, got %d", len(r.notifications))
	}
	checkInv(&w.worker, t)

	// Trigger confirmation check at t=8 — offline confirmed, 2 notifications
	result.Streamers = map[string]cmdlib.StreamerInfo{}
	if r := w.handleCheckerResults(result, 8); len(r.notifications) != 2 {
		t.Errorf("expected 2 notifications after offline confirmation, got %d", len(r.notifications))
	}
	checkInv(&w.worker, t)

	// Streamer "d" goes online — 2 notifications (chat 4 on ep1 and ep2)
	result.Streamers = map[string]cmdlib.StreamerInfo{
		"d": {},
	}
	if r := w.handleCheckerResults(result, 9); len(r.notifications) != 2 {
		t.Errorf("expected 2 notifications for streamer 'd' online, got %d", len(r.notifications))
	}
	checkInv(&w.worker, t)
}

func TestNotificationsStorage(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	w.db.AddUser(1, 3, 0, "private")
	w.db.AddUser(2, 3, 0, "private")
	w.db.AddUser(3, 3, 0, "private")

	idA := insertTestStreamer(&w.db, db.Streamer{Nickname: "a"})
	idB := insertTestStreamer(&w.db, db.Streamer{Nickname: "b"})
	idC := insertTestStreamer(&w.db, db.Streamer{Nickname: "c"})

	timeDiff := 2
	nots := []db.Notification{
		{
			Endpoint:   "endpoint_a",
			ChatID:     1,
			StreamerID: &idA,
			Nickname:   "a",
			Status:     cmdlib.StatusUnknown,
			TimeDiff:   nil,
			ImageURL:   "image_a",
			Social:     false,
			Priority:   db.PriorityHigh,
			Sound:      false,
			Kind:       db.NotificationPacket,
		},
		{
			Endpoint:   "endpoint_b",
			ChatID:     2,
			StreamerID: &idB,
			Nickname:   "b",
			Status:     cmdlib.StatusOffline,
			TimeDiff:   &timeDiff,
			ImageURL:   "image_b",
			Social:     true,
			Priority:   db.PriorityLow,
			Sound:      true,
			Kind:       db.ReplyPacket,
		},
	}

	w.db.StoreNotifications(nots)
	newNots := w.db.NewNotifications()
	nots[0].ID = 1
	nots[1].ID = 2
	if !reflect.DeepEqual(nots, newNots) {
		t.Errorf("unexpected notifications, expocted: %v, got: %v", nots, newNots)
	}
	nots = []db.Notification{
		{
			Endpoint:   "endpoint_c",
			ChatID:     3,
			StreamerID: &idC,
			Nickname:   "c",
			Status:     cmdlib.StatusOnline,
			TimeDiff:   nil,
			ImageURL:   "image_c",
			Social:     true,
			Priority:   db.PriorityLow,
		},
	}
	w.db.StoreNotifications(nots)
	newNots = w.db.NewNotifications()
	nots[0].ID = 3
	if !reflect.DeepEqual(nots, newNots) {
		t.Errorf("unexpected notifications, expocted: %v, got: %v", nots, newNots)
	}
	count := w.db.MustInt("select count(*) from notification_queue")
	if count != 3 {
		t.Errorf("unexpected notifications count %d", count)
	}
}

func TestStreamers(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	insertTestStreamer(&w.db, db.Streamer{Nickname: "a", ConfirmedStatus: cmdlib.StatusOffline})
	if w.db.MaybeStreamer("a") == nil {
		t.Error("unexpected result")
	}
	if w.db.MaybeStreamer("b") != nil {
		t.Error("unexpected result")
	}
}

func TestCopyFromAndBatchInTransaction(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Test that CopyFrom and SendBatch are in the same transaction
	// by making SendBatch fail and verifying CopyFrom data is rolled back

	// Insert a streamer to get an integer ID for the CopyFrom test
	streamerIntID := insertTestStreamer(&w.db, db.Streamer{Nickname: "a"})

	tx, err := w.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// CopyFrom should succeed
	rows := [][]interface{}{
		{streamerIntID, cmdlib.StatusOnline, 100},
	}
	_, err = tx.CopyFrom(
		context.Background(),
		pgx.Identifier{"status_changes"},
		[]string{"streamer_id", "status", "timestamp"},
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
	count := w.db.MustInt(
		"select count(*) from status_changes where streamer_id = $1",
		streamerIntID,
	)
	if count != 0 {
		t.Errorf("expected 0 status_changes after rollback, got %d", count)
	}
}

func TestCommandParser(t *testing.T) {
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
	if chatID != 0 || command != "" || args != "" {
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
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{NewChatMembers: []models.User{{ID: 2}}, Chat: models.Chat{ID: 1}}}, "", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{Message: &models.Message{NewChatMembers: []models.User{{ID: 2}}, Chat: models.Chat{ID: 1}}}, "", []int64{2})
	if chatID != 1 || command != "start" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{ChannelPost: &models.Message{Text: "command", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{ChannelPost: &models.Message{Text: "command@bot", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{ChannelPost: &models.Message{Text: "command @bot", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(&models.Update{ChannelPost: &models.Message{Text: " /command@bot", Chat: models.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
}

func TestUnconfirmedStatusConsistency(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	// Insert first status change for streamer "a"
	w.db.UpsertUnconfirmedStatusChanges([]db.StatusChange{
		{Nickname: "a", Status: cmdlib.StatusOnline},
	}, 100)

	streamer := w.db.MaybeStreamer("a")
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
	w.db.UpsertUnconfirmedStatusChanges([]db.StatusChange{
		{Nickname: "a", Status: cmdlib.StatusOffline},
	}, 200)

	streamer = w.db.MaybeStreamer("a")
	if streamer.UnconfirmedStatus != cmdlib.StatusOffline || streamer.UnconfirmedTimestamp != 200 {
		t.Errorf("unexpected unconfirmed status: %+v", streamer)
	}
	if streamer.PrevUnconfirmedStatus != cmdlib.StatusOnline || streamer.PrevUnconfirmedTimestamp != 100 {
		t.Errorf("unexpected prev unconfirmed status: %+v", streamer)
	}

	// Insert third status change — prev should shift
	w.db.UpsertUnconfirmedStatusChanges([]db.StatusChange{
		{Nickname: "a", Status: cmdlib.StatusOnline},
	}, 300)

	streamer = w.db.MaybeStreamer("a")
	if streamer.UnconfirmedStatus != cmdlib.StatusOnline || streamer.UnconfirmedTimestamp != 300 {
		t.Errorf("unexpected unconfirmed status: %+v", streamer)
	}
	if streamer.PrevUnconfirmedStatus != cmdlib.StatusOffline || streamer.PrevUnconfirmedTimestamp != 200 {
		t.Errorf("unexpected prev unconfirmed status: %+v", streamer)
	}
}

type lastTwoChanges struct {
	unconfirmed, prev db.StatusChange
}

func queryLastTwoChanges(d *db.Database) map[string]lastTwoChanges {
	lastTwo := map[string]lastTwoChanges{}
	var sc db.StatusChange
	var row int
	d.MustQuery(`
		select s.nickname, sub.status, sub.timestamp, sub.row
		from (
			select *, row_number() over (partition by streamer_id order by timestamp desc) as row
			from status_changes
		) sub
		join streamers s on s.id = sub.streamer_id
		where sub.row <= 2
		order by s.id, sub.row`,
		nil,
		db.ScanTo{&sc.Nickname, &sc.Status, &sc.Timestamp, &row},
		func() {
			entry := lastTwo[sc.Nickname]
			if row == 1 {
				entry.unconfirmed = sc
			} else {
				entry.prev = sc
			}
			lastTwo[sc.Nickname] = entry
		})
	return lastTwo
}

func queryAllStreamers(d *db.Database) []db.Streamer {
	var streamers []db.Streamer
	var streamer db.Streamer
	d.MustQuery(`
		select
			nickname,
			unconfirmed_status,
			unconfirmed_timestamp,
			prev_unconfirmed_status,
			prev_unconfirmed_timestamp
		from streamers`,
		nil,
		db.ScanTo{
			&streamer.Nickname,
			&streamer.UnconfirmedStatus,
			&streamer.UnconfirmedTimestamp,
			&streamer.PrevUnconfirmedStatus,
			&streamer.PrevUnconfirmedTimestamp,
		},
		func() { streamers = append(streamers, streamer) })
	return streamers
}

func queryAllNicknames(d *db.Database) map[string]bool {
	nicknames := map[string]bool{}
	var nickname string
	d.MustQuery(
		"select nickname from nicknames",
		nil,
		db.ScanTo{&nickname},
		func() { nicknames[nickname] = true })
	return nicknames
}

func checkLatestStatusChanges(t *testing.T, d *db.Database, lastTwo map[string]lastTwoChanges) {
	t.Helper()
	latest := map[string]db.StatusChange{}
	for nickname, pair := range lastTwo {
		latest[nickname] = pair.unconfirmed
	}
	if !reflect.DeepEqual(latest, queryLastStatusChanges(d)) {
		t.Errorf("latest status changes mismatch: %v vs %v", latest, queryLastStatusChanges(d))
	}
}

func checkUnconfirmedConsistency(t *testing.T, streamers []db.Streamer, lastTwo map[string]lastTwoChanges) {
	t.Helper()
	for _, s := range streamers {
		if s.UnconfirmedTimestamp == 0 {
			continue
		}
		expected := lastTwo[s.Nickname]
		if s.UnconfirmedStatus != expected.unconfirmed.Status ||
			s.UnconfirmedTimestamp != expected.unconfirmed.Timestamp {
			t.Errorf("unconfirmed status mismatch for %s: streamer=%+v, expected=%+v", s.Nickname, s, expected)
		}
		if s.PrevUnconfirmedStatus != expected.prev.Status ||
			s.PrevUnconfirmedTimestamp != expected.prev.Timestamp {
			t.Errorf("prev unconfirmed status mismatch for %s: streamer=%+v, expected=%+v", s.Nickname, s, expected)
		}
	}
}

func checkNicknamesMatch(t *testing.T, streamers []db.Streamer, dbNicknames map[string]bool) {
	t.Helper()
	streamerNicknames := map[string]bool{}
	for _, s := range streamers {
		streamerNicknames[s.Nickname] = true
	}
	if !reflect.DeepEqual(streamerNicknames, dbNicknames) {
		t.Errorf("nicknames mismatch: streamers=%v, nicknames=%v", streamerNicknames, dbNicknames)
	}
}

func checkNoConsecutiveSameStatuses(t *testing.T, d *db.Database) {
	t.Helper()
	type entry struct {
		nickname string
		status   cmdlib.StatusKind
	}
	var nickname string
	var status cmdlib.StatusKind
	var results []entry
	d.MustQuery(`
		with periods as (
			select
				s.nickname,
				sc.status,
				lead(sc.status) over (partition by sc.streamer_id order by sc.timestamp) as next_status
			from status_changes sc
			join streamers s on s.id = sc.streamer_id
		)
		select nickname, status
		from periods
		where status = next_status`,
		nil,
		db.ScanTo{&nickname, &status},
		func() { results = append(results, entry{nickname, status}) })
	for _, r := range results {
		t.Errorf("consecutive same status found for %s: %v", r.nickname, r.status)
	}
}

func checkInv(w *worker, t *testing.T) {
	t.Helper()
	lastTwoChanges := queryLastTwoChanges(&w.db)
	streamers := queryAllStreamers(&w.db)
	nicknames := queryAllNicknames(&w.db)

	checkLatestStatusChanges(t, &w.db, lastTwoChanges)
	checkUnconfirmedConsistency(t, streamers, lastTwoChanges)
	checkNicknamesMatch(t, streamers, nicknames)
	checkNoConsecutiveSameStatuses(t, &w.db)
}

func TestAddStreamer(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.db.AddUser(1, 3, 0, "private")

	// Add streamer that doesn't exist — should insert into pending_subscriptions and return nil
	if w.addStreamer("test", 1, "newmodel", 100, false) != nil {
		t.Error("expected addStreamer to return nil when streamer is not in the database and subscription is pending verification")
	}
	if w.db.MustInt("select count(*) from pending_subscriptions where nickname = $1", "newmodel") != 1 {
		t.Error("expected pending subscription for new streamer")
	}
	// Drain the "checking streamer" message
	<-w.outgoingMsgCh

	// Add streamer that exists with online status — should return non-nil
	insertTestStreamer(&w.db, db.Streamer{Nickname: "onlinemodel", ConfirmedStatus: cmdlib.StatusOnline})
	if w.addStreamer("test", 1, "onlinemodel", 100, false) == nil {
		t.Error("expected addStreamer to return non-nil for existing streamer")
	}
	if w.db.MustInt(`
		select count(*) from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		where s.nickname = $1`, "onlinemodel") != 1 {
		t.Error("expected subscription for existing streamer")
	}
	// Drain messages
	<-w.outgoingMsgCh
	nots := w.db.NewNotifications()
	if len(nots) != 1 || nots[0].Status != cmdlib.StatusOnline {
		t.Errorf("expected online notification, got %+v", nots)
	}

	// Add streamer that exists with offline status — should return non-nil
	insertTestStreamer(&w.db, db.Streamer{Nickname: "offlinemodel", ConfirmedStatus: cmdlib.StatusOffline})
	if w.addStreamer("test", 1, "offlinemodel", 100, false) == nil {
		t.Error("expected addStreamer to return non-nil for existing offline streamer")
	}
	nots = w.db.NewNotifications()
	if len(nots) != 1 || nots[0].Status != cmdlib.StatusOffline {
		t.Errorf("expected offline notification, got %+v", nots)
	}
}

func TestConfirmSub(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert pending subscription
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname) values ($1, $2, $3)",
		"test", 1, "a",
	)

	// Confirm the subscription
	streamerID := w.db.ConfirmSub(db.PendingSubscription{Endpoint: "test", ChatID: 1, Nickname: "a"})

	// Check returned streamer ID is valid
	if streamerID == 0 {
		t.Error("expected non-zero streamer ID from ConfirmSub")
	}

	// Check subscription was moved to subscriptions table
	if w.db.MustInt(`
		select count(*) from subscriptions
		where streamer_id = $1`, streamerID) != 1 {
		t.Error("expected subscription after ConfirmSub")
	}

	// Check pending subscription was deleted
	if w.db.MustInt("select count(*) from pending_subscriptions where nickname = $1", "a") != 0 {
		t.Error("expected pending subscription to be deleted after ConfirmSub")
	}

	// Check streamer was created
	if w.db.MaybeStreamer("a") == nil {
		t.Error("expected streamer to exist after ConfirmSub")
	}

	checkInv(&w.worker, t)
}

func TestDenySub(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert pending subscription
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname) values ($1, $2, $3)",
		"test", 1, "b",
	)

	// Deny the subscription
	w.db.DenySub(db.PendingSubscription{Endpoint: "test", ChatID: 1, Nickname: "b"})

	// Check pending subscription is deleted
	if w.db.MustInt("select count(*) from pending_subscriptions where nickname = $1", "b") != 0 {
		t.Error("expected pending subscription to be deleted after DenySub")
	}
}

func TestProcessSubsConfirmations(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert pending subscriptions in checking state
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking) values ($1, $2, $3, $4)",
		"test", 1, "online_model", true,
	)
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking) values ($1, $2, $3, $4)",
		"test", 2, "offline_model", true,
	)
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking) values ($1, $2, $3, $4)",
		"test", 3, "notfound_model", true,
	)
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking) values ($1, $2, $3, $4)",
		"test", 4, "denied_model", true,
	)
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking) values ($1, $2, $3, $4)",
		"test", 5, "notfound_denied_model", true,
	)
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking) values ($1, $2, $3, $4)",
		"test", 6, "online_offline_model", true,
	)
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking) values ($1, $2, $3, $4)",
		"test", 7, "unknown_model", true,
	)
	// Referral pending subscription — should create referral event on confirmation
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking, referral) values ($1, $2, $3, $4, $5)",
		"test", 8, "referral_model", true, true,
	)
	// Non-referral pending subscription — should NOT create referral event
	w.db.MustExec(
		"insert into pending_subscriptions (endpoint, chat_id, nickname, checking) values ($1, $2, $3, $4)",
		"test", 9, "nonreferral_model", true,
	)

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

	subCount := func(nickname string) int {
		return w.db.MustInt(`
			select count(*) from subscriptions sub
			join streamers s on s.id = sub.streamer_id
			where s.nickname = $1`, nickname)
	}
	pendingCount := func(nickname string) int {
		return w.db.MustInt(
			"select count(*) from pending_subscriptions where nickname = $1", nickname)
	}

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
	referralCount := w.db.MustInt(`
		select count(*) from referral_events r
		join streamers s on s.id = r.streamer_id
		where s.nickname = $1 and r.follower_chat_id = $2`, "referral_model", 8)
	if referralCount != 1 {
		t.Error("expected referral event for referral_model")
	}

	// Non-referral model should be confirmed but have no referral event
	if subCount("nonreferral_model") != 1 {
		t.Error("expected nonreferral_model to be in subscriptions")
	}
	nonReferralCount := w.db.MustInt(`
		select count(*) from referral_events r
		join streamers s on s.id = r.streamer_id
		where s.nickname = $1 and r.follower_chat_id = $2`, "nonreferral_model", 9)
	if nonReferralCount != 0 {
		t.Error("expected no referral event for nonreferral_model")
	}
}

func TestUserReferral(t *testing.T) {
	t.Run("valid referral creates event and bonuses", func(t *testing.T) {
		w := newTestWorker()
		defer w.terminate()
		w.createDatabase(make(chan bool, 1))

		// Create referrer and their referral link
		w.db.AddUser(20, w.cfg.MaxSubs, 0, "private")
		w.db.AddReferral(20, "ref-abc")

		// New user clicks referral link
		w.start("test", 21, "ref-abc", 100, "private")

		// Drain outgoing messages
		for len(w.outgoingMsgCh) > 0 {
			<-w.outgoingMsgCh
		}

		// Verify referral event was created with referrer
		refCount := w.db.MustInt(`
			select count(*) from referral_events
			where referrer_chat_id = $1 and follower_chat_id = $2`, 20, 21)
		if refCount != 1 {
			t.Error("expected referral event")
		}

		// Verify follower got bonus subs
		follower, exists := w.db.User(21)
		if !exists {
			t.Fatal("expected follower user to exist")
		}
		if follower.MaxSubs != w.cfg.MaxSubs+w.cfg.FollowerBonus {
			t.Errorf("expected follower max_subs %d, got %d",
				w.cfg.MaxSubs+w.cfg.FollowerBonus, follower.MaxSubs)
		}

		// Verify referrer got bonus subs
		referrer, exists := w.db.User(20)
		if !exists {
			t.Fatal("expected referrer user to exist")
		}
		if referrer.MaxSubs != w.cfg.MaxSubs+w.cfg.ReferralBonus {
			t.Errorf("expected referrer max_subs %d, got %d",
				w.cfg.MaxSubs+w.cfg.ReferralBonus, referrer.MaxSubs)
		}

		// Verify referred_users counter incremented
		referredUsers := w.db.MustInt(
			"select referred_users from referrals where chat_id = $1", 20)
		if referredUsers != 1 {
			t.Errorf("expected referred_users=1, got %d", referredUsers)
		}
	})

	t.Run("invalid referral ID", func(t *testing.T) {
		w := newTestWorker()
		defer w.terminate()
		w.createDatabase(make(chan bool, 1))

		w.start("test", 30, "nonexistent-ref", 100, "private")

		// Drain outgoing messages
		for len(w.outgoingMsgCh) > 0 {
			<-w.outgoingMsgCh
		}

		// Verify no referral event
		refCount := w.db.MustInt(
			"select count(*) from referral_events where follower_chat_id = $1", 30)
		if refCount != 0 {
			t.Error("expected no referral event for invalid referral ID")
		}
	})

	t.Run("existing user ignores referral", func(t *testing.T) {
		w := newTestWorker()
		defer w.terminate()
		w.createDatabase(make(chan bool, 1))

		// Create referrer with referral link
		w.db.AddUser(40, w.cfg.MaxSubs, 0, "private")
		w.db.AddReferral(40, "ref-xyz")

		// Create follower who already exists
		w.db.AddUser(41, w.cfg.MaxSubs, 0, "private")

		w.start("test", 41, "ref-xyz", 100, "private")

		// Drain outgoing messages
		for len(w.outgoingMsgCh) > 0 {
			<-w.outgoingMsgCh
		}

		// Verify no referral event
		refCount := w.db.MustInt(
			"select count(*) from referral_events where follower_chat_id = $1", 41)
		if refCount != 0 {
			t.Error("expected no referral event for existing user")
		}

		// Verify referrer max_subs unchanged
		referrer, _ := w.db.User(40)
		if referrer.MaxSubs != w.cfg.MaxSubs {
			t.Errorf("expected referrer max_subs unchanged at %d, got %d",
				w.cfg.MaxSubs, referrer.MaxSubs)
		}
	})

	t.Run("own referral link is rejected", func(t *testing.T) {
		w := newTestWorker()
		defer w.terminate()
		w.createDatabase(make(chan bool, 1))

		// Create user with referral link
		w.db.AddUser(50, w.cfg.MaxSubs, 0, "private")
		w.db.AddReferral(50, "ref-own")

		w.start("test", 50, "ref-own", 100, "private")

		// Drain outgoing messages
		for len(w.outgoingMsgCh) > 0 {
			<-w.outgoingMsgCh
		}

		// Verify no referral event
		refCount := w.db.MustInt(
			"select count(*) from referral_events where follower_chat_id = $1", 50)
		if refCount != 0 {
			t.Error("expected no referral event for own link")
		}
	})
}

func TestStreamerReferral(t *testing.T) {
	t.Run("known streamer creates referral event", func(t *testing.T) {
		w := newTestWorker()
		defer w.terminate()
		w.createDatabase(make(chan bool, 1))

		// Pre-create a known streamer
		insertTestStreamer(&w.db, db.Streamer{Nickname: "known_model", ConfirmedStatus: cmdlib.StatusOnline})

		w.start("test", 10, "m-known_model", 100, "private")

		// Drain outgoing messages
		for len(w.outgoingMsgCh) > 0 {
			<-w.outgoingMsgCh
		}

		// Verify subscription was created
		subCount := w.db.MustInt(`
			select count(*) from subscriptions sub
			join streamers s on s.id = sub.streamer_id
			where s.nickname = $1 and sub.chat_id = $2`, "known_model", 10)
		if subCount != 1 {
			t.Error("expected subscription for known_model")
		}

		// Verify referral event was created
		refCount := w.db.MustInt(`
			select count(*) from referral_events r
			join streamers s on s.id = r.streamer_id
			where s.nickname = $1 and r.follower_chat_id = $2`, "known_model", 10)
		if refCount != 1 {
			t.Error("expected referral event for known_model")
		}
	})

	t.Run("unknown streamer defers referral to pending", func(t *testing.T) {
		w := newTestWorker()
		defer w.terminate()
		w.createDatabase(make(chan bool, 1))

		w.start("test", 11, "m-unknown_model", 100, "private")

		// Drain outgoing messages
		for len(w.outgoingMsgCh) > 0 {
			<-w.outgoingMsgCh
		}

		// Verify pending subscription was created with referral flag
		refFlag := w.db.MustInt(
			"select referral::int from pending_subscriptions where nickname = $1 and chat_id = $2",
			"unknown_model", 11)
		if refFlag != 1 {
			t.Error("expected pending subscription with referral=true")
		}

		// Verify no referral event yet
		refCount := w.db.MustInt(`
			select count(*) from referral_events r
			join streamers s on s.id = r.streamer_id
			where s.nickname = $1 and r.follower_chat_id = $2`, "unknown_model", 11)
		if refCount != 0 {
			t.Error("expected no referral event for unknown streamer")
		}
	})
}

func TestStatusConfirmations(t *testing.T) {
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
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase(make(chan bool, 1))

			// Set up background streamers that should remain unchanged
			insertTestStreamer(&w.db, db.Streamer{
				Nickname:             "always_online",
				ConfirmedStatus:      cmdlib.StatusOnline,
				UnconfirmedStatus:    cmdlib.StatusOnline,
				UnconfirmedTimestamp: 100,
			})
			insertTestStreamer(&w.db, db.Streamer{
				Nickname:             "always_offline",
				ConfirmedStatus:      cmdlib.StatusOffline,
				UnconfirmedStatus:    cmdlib.StatusOffline,
				UnconfirmedTimestamp: 100,
			})
			insertTestStreamer(&w.db, db.Streamer{
				Nickname:             "always_unknown",
				ConfirmedStatus:      cmdlib.StatusUnknown,
				UnconfirmedStatus:    cmdlib.StatusUnknown,
				UnconfirmedTimestamp: 100,
			})
			insertTestStreamer(&w.db, db.Streamer{
				Nickname:             "ch",
				ConfirmedStatus:      tt.confirmed,
				UnconfirmedStatus:    tt.unconfirmed,
				UnconfirmedTimestamp: tt.timestamp,
			})

			changes := w.db.ConfirmStatusChanges(
				tt.now,
				w.cfg.StatusConfirmationSeconds.Online,
				w.cfg.StatusConfirmationSeconds.Offline,
			)

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
			if s := w.db.MustInt("select confirmed_status from streamers where nickname = $1", "always_online"); s != int(cmdlib.StatusOnline) {
				t.Errorf("always_online confirmed_status was affected, got %v", s)
			}
			if s := w.db.MustInt("select confirmed_status from streamers where nickname = $1", "always_offline"); s != int(cmdlib.StatusOffline) {
				t.Errorf("always_offline confirmed_status was affected, got %v", s)
			}
			if s := w.db.MustInt("select confirmed_status from streamers where nickname = $1", "always_unknown"); s != int(cmdlib.StatusUnknown) {
				t.Errorf("always_unknown confirmed_status was affected, got %v", s)
			}
		})
	}
}

func TestQueryLastSubscriptionStatuses(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert streamers and confirmed subscriptions
	insertTestStreamer(&w.db, db.Streamer{
		Nickname:          "model_with_status",
		ConfirmedStatus:   cmdlib.StatusOnline,
		UnconfirmedStatus: cmdlib.StatusOnline,
	})
	insertTestStreamer(&w.db, db.Streamer{Nickname: "model_without_status"})
	w.db.MustExec(`
		insert into subscriptions (endpoint, chat_id, streamer_id)
		values ($1, $2, (select id from streamers where nickname = $3))`,
		"test", 1, "model_with_status",
	)
	w.db.MustExec(`
		insert into subscriptions (endpoint, chat_id, streamer_id)
		values ($1, $2, (select id from streamers where nickname = $3))`,
		"test", 2, "model_without_status",
	)

	statuses := w.db.QueryLastSubscriptionStatuses()

	// Model with unconfirmed_status should return correct status
	if statuses["model_with_status"] != cmdlib.StatusOnline {
		t.Errorf("expected model_with_status to be online, got %v", statuses["model_with_status"])
	}

	// Model with default unconfirmed_status (0) should return StatusUnknown
	if statuses["model_without_status"] != cmdlib.StatusUnknown {
		t.Errorf("expected model_without_status to be unknown, got %v", statuses["model_without_status"])
	}
}

func TestHandleStatusUpdates(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	// Insert a subscription
	insertTestStreamer(&w.db, db.Streamer{Nickname: "a"})
	insertSubscription(&w.db, "test", 1, "a")

	// Test with OnlineListResults
	result := &cmdlib.OnlineListResults{
		Streamers: map[string]cmdlib.StreamerInfo{"a": {ImageURL: "http://a.jpg"}},
	}
	r := w.handleCheckerResults(result, 100)
	if r.changesCount != 1 {
		t.Errorf("expected 1 change with OnlineListResults, got %d", r.changesCount)
	}
	if w.unconfirmedOnlineStreamers["a"].ImageURL != "http://a.jpg" {
		t.Errorf("expected ImageURL to be set, got %s", w.unconfirmedOnlineStreamers["a"].ImageURL)
	}
	checkUnconfirmedOnlineStreamers(w, t)
	checkInv(&w.worker, t)

	// Test ImageURL update for streamer that remains online
	result.Streamers["a"] = cmdlib.StreamerInfo{ImageURL: "http://a2.jpg"}
	w.handleCheckerResults(result, 101)
	if w.unconfirmedOnlineStreamers["a"].ImageURL != "http://a2.jpg" {
		t.Errorf("expected ImageURL to be updated, got %s", w.unconfirmedOnlineStreamers["a"].ImageURL)
	}
	checkUnconfirmedOnlineStreamers(w, t)
	checkInv(&w.worker, t)

	// Test with FixedListOnlineResults — streamer goes offline (not in Streamers)
	result2 := &cmdlib.FixedListOnlineResults{
		RequestedStreamers: map[string]bool{"a": true},
		Streamers:          map[string]cmdlib.StreamerInfo{}, // empty = "a" is offline
	}
	r = w.handleCheckerResults(result2, 102)
	if r.changesCount != 1 {
		t.Errorf("expected 1 change with FixedListOnlineResults, got %d", r.changesCount)
	}
	if _, ok := w.unconfirmedOnlineStreamers["a"]; ok {
		t.Error("expected offline streamer to be removed from unconfirmedOnlineStreamers")
	}
	checkUnconfirmedOnlineStreamers(w, t)
	checkInv(&w.worker, t)

	// Streamer comes back online (use new map to avoid aliasing with unconfirmedOnlineStreamers)
	result2.Streamers = map[string]cmdlib.StreamerInfo{"a": {ImageURL: "http://a3.jpg"}}
	w.handleCheckerResults(result2, 103)
	checkUnconfirmedOnlineStreamers(w, t)
	checkInv(&w.worker, t)

	// Streamer goes offline again (use new empty map)
	result2.Streamers = map[string]cmdlib.StreamerInfo{}
	w.handleCheckerResults(result2, 104)
	if _, ok := w.unconfirmedOnlineStreamers["a"]; ok {
		t.Error("expected offline streamer to be removed from unconfirmedOnlineStreamers")
	}
	checkUnconfirmedOnlineStreamers(w, t)
	checkInv(&w.worker, t)

	// Test error case (should return early with zero values)
	result3 := cmdlib.NewOnlineListResultsFailed()
	r = w.handleCheckerResults(result3, 105)
	if r.changesCount != 0 || r.confirmedChangesCount != 0 || len(r.notifications) != 0 || r.elapsed != 0 {
		t.Errorf(
			"expected zero values on error, got changes=%d, confirmedChanges=%d, nots=%d, elapsed=%d",
			r.changesCount, r.confirmedChangesCount, len(r.notifications), r.elapsed)
	}

	// Test error case with FixedListResults
	result4 := cmdlib.NewFixedListOnlineResultsFailed()
	r = w.handleCheckerResults(result4, 106)
	if r.changesCount != 0 || r.confirmedChangesCount != 0 || len(r.notifications) != 0 || r.elapsed != 0 {
		t.Errorf(
			"expected zero values on error with FixedListResults, got changes=%d, confirmedChanges=%d, nots=%d, elapsed=%d",
			r.changesCount, r.confirmedChangesCount, len(r.notifications), r.elapsed)
	}

	// New streamer discovered via OnlineListResults —
	// should insert into both streamers and nicknames
	result5 := &cmdlib.OnlineListResults{
		Streamers: map[string]cmdlib.StreamerInfo{"newmodel": {}},
	}
	w.handleCheckerResults(result5, 107)
	checkInv(&w.worker, t)
}

func TestUnsubscribeBeforeRestart(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	// Subscribe to "a" and "b"
	insertTestStreamer(&w.db, db.Streamer{Nickname: "a"})
	insertTestStreamer(&w.db, db.Streamer{Nickname: "b"})
	insertSubscription(&w.db, "test", 1, "a")
	insertSubscription(&w.db, "test", 1, "b")

	// Both streamers come online
	result := &cmdlib.FixedListOnlineResults{
		RequestedStreamers: map[string]bool{"a": true, "b": true},
		Streamers: map[string]cmdlib.StreamerInfo{
			"a": {},
			"b": {},
		},
	}
	w.handleCheckerResults(result, 100)
	checkInv(&w.worker, t)

	// Verify both are online in DB
	if w.db.MaybeStreamer("a").UnconfirmedStatus != cmdlib.StatusOnline {
		t.Error("expected 'a' to be online")
	}
	if w.db.MaybeStreamer("b").UnconfirmedStatus != cmdlib.StatusOnline {
		t.Error("expected 'b' to be online")
	}

	// Unsubscribe from "a"
	w.db.MustExec(`
		delete from subscriptions
		where streamer_id = (select id from streamers where nickname = $1)`, "a")

	// Simulate restart: reinitialize cache as would happen on restart
	w.initCache()

	// First query after restart — only "b" is subscribed.
	// "a" is no longer in RequestedStreamers since it's unsubscribed.
	result2 := &cmdlib.FixedListOnlineResults{
		RequestedStreamers: map[string]bool{"b": true},
		Streamers: map[string]cmdlib.StreamerInfo{
			"b": {},
		},
	}
	w.handleCheckerResults(result2, 101)
	checkInv(&w.worker, t)

	// "a" should now have StatusUnknown in DB because it's a known streamer
	// but not in RequestedStreamers (not subscribed anymore).
	streamerA := w.db.MaybeStreamer("a")
	if streamerA.UnconfirmedStatus != cmdlib.StatusUnknown {
		t.Errorf("expected 'a' to have StatusUnknown, got %v", streamerA.UnconfirmedStatus)
	}
}

func TestUnknownStreamerFirstOfflineSaved(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	// Add a user
	w.db.AddUser(1, 3, 0, "private")

	// 1. User subscribes to a streamer we don't know yet — creates unconfirmed subscription
	// This simulates subscribing to a Twitch streamer or a new unknown model
	if w.addStreamer("test", 1, "unknown_model", 100, false) != nil {
		t.Error("expected addStreamer to return nil for unknown streamer")
	}
	// Drain the "checking streamer" message
	<-w.outgoingMsgCh

	// Verify pending subscription exists
	if w.db.MustInt("select count(*) from pending_subscriptions where nickname = $1", "unknown_model") != 1 {
		t.Error("expected pending subscription for new streamer")
	}

	// 2. Subscription is confirmed — checker returns offline status (Twitch returns Online|Offline)
	// Set subscription to "checking" state as queryUnconfirmedSubs would do
	w.db.MustExec("update pending_subscriptions set checking = true where nickname = $1", "unknown_model")
	w.processSubsConfirmations(&cmdlib.ExistenceListResults{
		Streamers: map[string]cmdlib.StreamerInfoWithStatus{
			// Twitch returns Online|Offline when streamer exists but is offline
			"unknown_model": {Status: cmdlib.StatusOnline | cmdlib.StatusOffline},
		},
	})

	// Verify subscription moved to subscriptions table
	if w.db.MustInt(`
		select count(*) from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		where s.nickname = $1`, "unknown_model") != 1 {
		t.Error("expected subscription after confirmation")
	}

	// 3. First status update: offline
	// Offline status SHOULD be saved so we can calculate online duration later
	result := &cmdlib.FixedListOnlineResults{
		RequestedStreamers: map[string]bool{"unknown_model": true},
		Streamers:          map[string]cmdlib.StreamerInfo{}, // empty = offline
	}
	r := w.handleCheckerResults(result, 101)
	checkInv(&w.worker, t)

	// 4. Offline status should be recorded for proper online time calculation
	if r.changesCount != 1 {
		t.Errorf("expected 1 change for first offline status, got %d", r.changesCount)
	}

	// Verify status_change was recorded
	count := w.db.MustInt(
		"select count(*) from status_changes where streamer_id = (select id from streamers where nickname = $1)",
		"unknown_model",
	)
	if count != 1 {
		t.Errorf("expected 1 status_change for first offline, got %d", count)
	}

	// Verify streamer has offline status
	streamer := w.db.MaybeStreamer("unknown_model")
	if streamer == nil {
		t.Fatal("expected streamer to exist")
	}
	if streamer.UnconfirmedStatus != cmdlib.StatusOffline {
		t.Errorf("expected unconfirmed status to be offline, got %v", streamer.UnconfirmedStatus)
	}

	// 5. Subsequent status update with same offline status should NOT record a new change
	result.Streamers = map[string]cmdlib.StreamerInfo{} // use new map to avoid aliasing
	r = w.handleCheckerResults(result, 102)
	checkInv(&w.worker, t)
	if r.changesCount != 0 {
		t.Errorf("expected 0 changes for same offline status, got %d", r.changesCount)
	}

	// Still only 1 status_change
	count = w.db.MustInt(
		"select count(*) from status_changes where streamer_id = (select id from streamers where nickname = $1)",
		"unknown_model",
	)
	if count != 1 {
		t.Errorf("expected still 1 status_change, got %d", count)
	}
}

func TestStatusTransitions(t *testing.T) {
	tests := []struct {
		name          string
		subscribed    bool
		dbBefore      *cmdlib.StatusKind // nil means streamer doesn't exist in DB
		fixedList     bool
		checkerStatus *cmdlib.StatusKind // nil means streamer not in checker result
		dbAfter       *cmdlib.StatusKind // nil means streamer shouldn't exist or no change
	}{
		// Fixed list checker tests
		{
			name:          "fixed list: unknown to online",
			subscribed:    true,
			dbBefore:      nil,
			fixedList:     true,
			checkerStatus: ptr(cmdlib.StatusOnline),
			dbAfter:       ptr(cmdlib.StatusOnline),
		},
		{
			name:          "fixed list: online to offline",
			subscribed:    true,
			dbBefore:      ptr(cmdlib.StatusOnline),
			fixedList:     true,
			checkerStatus: ptr(cmdlib.StatusOffline),
			dbAfter:       ptr(cmdlib.StatusOffline),
		},
		{
			name:          "fixed list: offline to online",
			subscribed:    true,
			dbBefore:      ptr(cmdlib.StatusOffline),
			fixedList:     true,
			checkerStatus: ptr(cmdlib.StatusOnline),
			dbAfter:       ptr(cmdlib.StatusOnline),
		},
		{
			name:          "fixed list: online to unknown (unsubscribe)",
			subscribed:    false,
			dbBefore:      ptr(cmdlib.StatusOnline),
			fixedList:     true,
			checkerStatus: nil, // not in result because unsubscribed
			dbAfter:       ptr(cmdlib.StatusUnknown),
		},
		{
			name:          "fixed list: unknown to offline",
			subscribed:    true,
			dbBefore:      nil,
			fixedList:     true,
			checkerStatus: ptr(cmdlib.StatusOffline),
			dbAfter:       ptr(cmdlib.StatusOffline),
		},
		{
			name:          "fixed list: offline to unknown (unsubscribe)",
			subscribed:    false,
			dbBefore:      ptr(cmdlib.StatusOffline),
			fixedList:     true,
			checkerStatus: nil, // not in result because unsubscribed
			dbAfter:       ptr(cmdlib.StatusUnknown),
		},
		// Online list checker tests
		{
			name:          "online list: unknown to online",
			subscribed:    true,
			dbBefore:      nil,
			fixedList:     false,
			checkerStatus: ptr(cmdlib.StatusOnline),
			dbAfter:       ptr(cmdlib.StatusOnline),
		},
		{
			name:          "online list: online to offline (missing from result)",
			subscribed:    true,
			dbBefore:      ptr(cmdlib.StatusOnline),
			fixedList:     false,
			checkerStatus: nil,
			dbAfter:       ptr(cmdlib.StatusOffline),
		},
		{
			name:          "online list: offline to online",
			subscribed:    true,
			dbBefore:      ptr(cmdlib.StatusOffline),
			fixedList:     false,
			checkerStatus: ptr(cmdlib.StatusOnline),
			dbAfter:       ptr(cmdlib.StatusOnline),
		},
		// Unsubscribed streamer tests
		{
			name:          "online list: unsubscribed streamer stays online",
			subscribed:    false,
			dbBefore:      nil,
			fixedList:     false,
			checkerStatus: ptr(cmdlib.StatusOnline),
			dbAfter:       ptr(cmdlib.StatusOnline),
		},
		{
			name:          "fixed list: unsubscribed streamer stays online",
			subscribed:    false,
			dbBefore:      nil,
			fixedList:     true,
			checkerStatus: ptr(cmdlib.StatusOnline),
			dbAfter:       ptr(cmdlib.StatusOnline),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase(make(chan bool, 1))
			// Set up background streamers that should remain unchanged
			insertTestStreamer(&w.db, db.Streamer{
				Nickname:             "always_online",
				UnconfirmedStatus:    cmdlib.StatusOnline,
				UnconfirmedTimestamp: 1,
			})
			insertTestStreamer(&w.db, db.Streamer{
				Nickname:             "always_offline",
				UnconfirmedStatus:    cmdlib.StatusOffline,
				UnconfirmedTimestamp: 1,
			})
			insertTestStreamer(&w.db, db.Streamer{
				Nickname:             "always_unknown",
				UnconfirmedStatus:    cmdlib.StatusUnknown,
				UnconfirmedTimestamp: 1,
			})
			insertSubscription(&w.db, "ep", 1, "always_online")
			insertSubscription(&w.db, "ep", 1, "always_offline")
			insertSubscription(&w.db, "ep", 1, "always_unknown")
			w.db.MustExec(`
				insert into status_changes (streamer_id, status, timestamp)
				values ((select id from streamers where nickname = $1), $2, $3)`,
				"always_online", cmdlib.StatusOnline, 1,
			)
			w.db.MustExec(`
				insert into status_changes (streamer_id, status, timestamp)
				values ((select id from streamers where nickname = $1), $2, $3)`,
				"always_offline", cmdlib.StatusOffline, 1,
			)
			w.db.MustExec(`
				insert into status_changes (streamer_id, status, timestamp)
				values ((select id from streamers where nickname = $1), $2, $3)`,
				"always_unknown", cmdlib.StatusUnknown, 1,
			)

			// Initialize cache after setting up background streamers
			w.initCache()

			// Always subscribe during setup if we need to set initial state
			// (subscription is needed to track the streamer)
			if tt.dbBefore != nil || tt.subscribed {
				insertTestStreamer(&w.db, db.Streamer{Nickname: "ch"})
				insertSubscription(&w.db, "ep", 1, "ch")
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
				checkInv(&w.worker, t)
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
			checkInv(&w.worker, t)

			streamer := w.db.MaybeStreamer("ch")
			if tt.dbAfter == nil {
				if streamer != nil {
					t.Errorf("expected no streamer in DB, got %v", streamer)
				}
			} else {
				if streamer == nil {
					t.Errorf("expected streamer in DB with status %v, got nil", *tt.dbAfter)
				} else if streamer.UnconfirmedStatus != *tt.dbAfter {
					t.Errorf("expected status %v, got %v", *tt.dbAfter, streamer.UnconfirmedStatus)
				}
			}

			// Verify background streamers were not affected
			if ch := w.db.MaybeStreamer("always_online"); ch == nil || ch.UnconfirmedStatus != cmdlib.StatusOnline {
				t.Errorf("always_online was affected, got %v", ch)
			}
			if ch := w.db.MaybeStreamer("always_offline"); ch == nil || ch.UnconfirmedStatus != cmdlib.StatusOffline {
				t.Errorf("always_offline was affected, got %v", ch)
			}
			if ch := w.db.MaybeStreamer("always_unknown"); ch == nil || ch.UnconfirmedStatus != cmdlib.StatusUnknown {
				t.Errorf("always_unknown was affected, got %v", ch)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestNotifyOfStatuses(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	w.db.AddUser(100, 3, 0, "private")
	w.db.AddUser(101, 3, 0, "private")
	w.db.AddUser(200, 3, 0, "private")
	w.db.AddUser(201, 3, 0, "private")

	nots := []db.Notification{
		{ChatID: 100, Endpoint: "test", Nickname: "a", Status: cmdlib.StatusOnline, Priority: db.PriorityLow},
		{ChatID: 101, Endpoint: "test", Nickname: "b", Status: cmdlib.StatusOnline, Priority: db.PriorityHigh},
		{ChatID: 200, Endpoint: "test", Nickname: "c", Status: cmdlib.StatusOnline, Priority: db.PriorityLow},
		{ChatID: 201, Endpoint: "test", Nickname: "d", Status: cmdlib.StatusOnline, Priority: db.PriorityHigh},
	}

	w.notifyOfStatuses(nots)

	if len(w.outgoingMsgCh) != 4 {
		t.Errorf("expected 4 outgoing messages, got %d", len(w.outgoingMsgCh))
	}
	lowCount := 0
	highCount := 0
	for range len(w.outgoingMsgCh) {
		p := <-w.outgoingMsgCh
		if p.priority == db.PriorityLow {
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
