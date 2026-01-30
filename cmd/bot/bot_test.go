package main

import (
	"context"
	"reflect"
	"runtime/debug"
	"testing"

	"github.com/bcmk/siren/internal/db"
	"github.com/bcmk/siren/lib/cmdlib"
	tg "github.com/bcmk/telegram-bot-api"
	"github.com/jackc/pgx/v5"
)

func checkUnconfirmedOnlineChannels(w *testWorker, t *testing.T) {
	dbOnline := map[string]bool{}
	var channelID string
	w.db.MustQuery(
		"select channel_id from channels where unconfirmed_status = $1",
		[]interface{}{cmdlib.StatusOnline},
		db.ScanTo{&channelID},
		func() { dbOnline[channelID] = true })
	if len(w.unconfirmedOnlineChannels) != len(dbOnline) {
		t.Errorf("unconfirmedOnlineChannels size %d != DB size %d", len(w.unconfirmedOnlineChannels), len(dbOnline))
	}
	for ch := range dbOnline {
		if _, ok := w.unconfirmedOnlineChannels[ch]; !ok {
			t.Errorf("channel %s in DB but not in unconfirmedOnlineChannels", ch)
		}
	}
	for ch := range w.unconfirmedOnlineChannels {
		if !dbOnline[ch] {
			t.Errorf("channel %s in unconfirmedOnlineChannels but not in DB", ch)
		}
	}
}

func TestSql(t *testing.T) {
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 1, "a")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 2, "b")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 3, "c")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 3, "c2")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 3, "c3")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 4, "d")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 5, "d")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 6, "e")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 7, "f")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep2", 6, "e")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep2", 7, "f")
	w.db.MustExec("insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep2", 8, "g")
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 2, 0)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 3, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 4, w.cfg.BlockThreshold-1)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 5, w.cfg.BlockThreshold+1)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 6, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 7, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep2", 7, w.cfg.BlockThreshold)
	w.db.MustExec("insert into channels (channel_id, confirmed_status) values ($1, $2)", "a", cmdlib.StatusOnline)
	w.db.MustExec("insert into channels (channel_id, confirmed_status) values ($1, $2)", "b", cmdlib.StatusOnline)
	w.db.MustExec("insert into channels (channel_id, confirmed_status) values ($1, $2)", "c", cmdlib.StatusOnline)
	w.db.MustExec("insert into channels (channel_id, confirmed_status) values ($1, $2)", "c2", cmdlib.StatusOnline)
	broadcastChats := w.db.BroadcastChats("ep1")
	if !reflect.DeepEqual(broadcastChats, []int64{1, 2, 3, 4, 5, 6, 7}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	broadcastChats = w.db.BroadcastChats("ep2")
	if !reflect.DeepEqual(broadcastChats, []int64{6, 7, 8}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	chatsForChannel, endpoints := w.chatsForChannel("a")
	if !reflect.DeepEqual(endpoints, []string{"ep1"}) {
		t.Error("unexpected chats for channel result", chatsForChannel)
	}
	if !reflect.DeepEqual(chatsForChannel, []int64{1}) {
		t.Error("unexpected chats for channel result", chatsForChannel)
	}
	chatsForChannel, _ = w.chatsForChannel("b")
	if !reflect.DeepEqual(chatsForChannel, []int64{2}) {
		t.Error("unexpected chats for channel result", chatsForChannel)
	}
	chatsForChannel, _ = w.chatsForChannel("c")
	if !reflect.DeepEqual(chatsForChannel, []int64{3}) {
		t.Error("unexpected chats for channel result", chatsForChannel)
	}
	chatsForChannel, _ = w.chatsForChannel("d")
	if !reflect.DeepEqual(chatsForChannel, []int64{4, 5}) {
		t.Error("unexpected chats for channel result", chatsForChannel)
	}
	chatsForChannel, _ = w.chatsForChannel("e")
	if !reflect.DeepEqual(chatsForChannel, []int64{6, 6}) {
		t.Error("unexpected chats for channel result", chatsForChannel)
	}
	chatsForChannel, _ = w.chatsForChannel("f")
	if !reflect.DeepEqual(chatsForChannel, []int64{7, 7}) {
		t.Error("unexpected chats for channel result", chatsForChannel)
	}
	w.db.IncrementBlock("ep1", 2)
	w.db.IncrementBlock("ep1", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep1") != 2 {
		t.Error("unexpected block for channel result", chatsForChannel)
	}
	w.db.IncrementBlock("ep2", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for channel result", chatsForChannel)
	}
	w.db.ResetBlock("ep1", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep1") != 0 {
		t.Error("unexpected block for channel result", chatsForChannel)
	}
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for channel result", chatsForChannel)
	}
	w.db.IncrementBlock("ep1", 1)
	w.db.IncrementBlock("ep1", 1)
	if w.db.MustInt("select block from block where chat_id = $1", 1) != 2 {
		t.Error("unexpected block for channel result", chatsForChannel)
	}
	statuses := w.db.ConfirmedStatusesForChat("ep1", 3)
	if !reflect.DeepEqual(statuses, []db.Channel{
		{ChannelID: "c", ConfirmedStatus: cmdlib.StatusOnline},
		{ChannelID: "c2", ConfirmedStatus: cmdlib.StatusOnline}}) {
		t.Error("unexpected statuses", statuses)
	}
	_ = w.db.Close()
}

func TestUpdateNotifications(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)",
		"ep1", 1, "a")
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)",
		"ep1", 2, "b")
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)",
		"ep1", 3, "a")
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)",
		"ep1", 3, "c")
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)",
		"ep1", 4, "d")
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id) values ($1, $2, $3)",
		"ep2", 4, "d")

	w.db.MustExec("insert into users (chat_id, created_at) values ($1, 0)", 1)
	w.db.MustExec("insert into users (chat_id, created_at) values ($1, 0)", 2)
	w.db.MustExec("insert into users (chat_id, created_at) values ($1, 0)", 3)
	w.db.MustExec("insert into users (chat_id, created_at) values ($1, 0)", 4)

	// All subscribed channels for this test
	allChannels := map[string]bool{"a": true, "b": true, "c": true, "d": true}

	// Use fixed list checker mode for these tests
	// Channel "a" goes online — 2 notifications (chat 1 and chat 3)
	result := &cmdlib.FixedListOnlineResults{
		RequestedChannels: allChannels,
		Channels:          map[string]cmdlib.ChannelInfo{"a": {}},
	}
	if _, _, nots, _ := w.handleCheckerResults(result, 2); len(nots) != 2 {
		t.Errorf("expected 2 notifications for channel 'a' online, got %d", len(nots))
	}
	checkInv(&w.worker, t)

	// Channel "a" goes offline — no notifications yet (needs 5s confirmation)
	result.Channels = map[string]cmdlib.ChannelInfo{}
	if _, _, nots, _ := w.handleCheckerResults(result, 3); len(nots) != 0 {
		t.Errorf("expected 0 notifications before offline confirmation, got %d", len(nots))
	}
	checkInv(&w.worker, t)

	// Trigger confirmation check at t=8 — offline confirmed, 2 notifications
	result.Channels = map[string]cmdlib.ChannelInfo{}
	if _, _, nots, _ := w.handleCheckerResults(result, 8); len(nots) != 2 {
		t.Errorf("expected 2 notifications after offline confirmation, got %d", len(nots))
	}
	checkInv(&w.worker, t)

	// Channel "d" goes online — 2 notifications (chat 4 on ep1 and ep2)
	result.Channels = map[string]cmdlib.ChannelInfo{
		"d": {},
	}
	if _, _, nots, _ := w.handleCheckerResults(result, 9); len(nots) != 2 {
		t.Errorf("expected 2 notifications for channel 'd' online, got %d", len(nots))
	}
	checkInv(&w.worker, t)
}

func queryLastStatusChanges(d *db.Database) map[string]db.StatusChange {
	statusChanges := map[string]db.StatusChange{}
	var statusChange db.StatusChange
	d.MustQuery(
		`
			select distinct on (channel_id) channel_id, status, timestamp
			from status_changes
			order by channel_id, timestamp desc
		`,
		nil,
		db.ScanTo{&statusChange.ChannelID, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.ChannelID] = statusChange })
	return statusChanges
}

func TestNotificationsStorage(t *testing.T) {
	timeDiff := 2
	nots := []db.Notification{
		{
			Endpoint:  "endpoint_a",
			ChatID:    1,
			ChannelID: "a",
			Status:    cmdlib.StatusUnknown,
			TimeDiff:  nil,
			ImageURL:  "image_a",
			Social:    false,
			Priority:  1,
			Sound:     false,
			Kind:      db.NotificationPacket,
		},
		{
			Endpoint:  "endpoint_b",
			ChatID:    2,
			ChannelID: "b",
			Status:    cmdlib.StatusOffline,
			TimeDiff:  &timeDiff,
			ImageURL:  "image_b",
			Social:    true,
			Priority:  2,
			Sound:     true,
			Kind:      db.ReplyPacket,
		},
	}
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.db.StoreNotifications(nots)
	newNots := w.db.NewNotifications()
	nots[0].ID = 1
	nots[1].ID = 2
	if !reflect.DeepEqual(nots, newNots) {
		t.Errorf("unexpected notifications, expocted: %v, got: %v", nots, newNots)
	}
	nots = []db.Notification{
		{
			Endpoint:  "endpoint_c",
			ChatID:    3,
			ChannelID: "c",
			Status:    cmdlib.StatusOnline,
			TimeDiff:  nil,
			ImageURL:  "image_c",
			Social:    true,
			Priority:  3,
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

func TestChannels(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.db.MustExec("insert into channels (channel_id, confirmed_status) values ($1, $2)", "a", cmdlib.StatusOffline)
	if w.db.MaybeChannel("a") == nil {
		t.Error("unexpected result")
	}
	if w.db.MaybeChannel("b") != nil {
		t.Error("unexpected result")
	}
}

func TestCopyFromAndBatchInTransaction(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Test that CopyFrom and SendBatch are in the same transaction
	// by making SendBatch fail and verifying CopyFrom data is rolled back

	tx, err := w.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// CopyFrom should succeed
	rows := [][]interface{}{
		{"a", cmdlib.StatusOnline, 100},
	}
	_, err = tx.CopyFrom(
		context.Background(),
		pgx.Identifier{"status_changes"},
		[]string{"channel_id", "status", "timestamp"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		t.Fatal(err)
	}

	// SendBatch with invalid status (violates check constraint) should fail
	batch := &pgx.Batch{}
	batch.Queue(
		`
			insert into channels (channel_id, unconfirmed_status)
			values ($1, $2)
		`,
		"test_channel", 999) // 999 violates check constraint
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
	count := w.db.MustInt("select count(*) from status_changes where channel_id = 'a'")
	if count != 0 {
		t.Errorf("expected 0 status_changes after rollback, got %d", count)
	}
}

func TestCommandParser(t *testing.T) {
	chatID, command, args := getCommandAndArgs(tg.Update{}, "", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{}}, "", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: "command", Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: "   ", Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: "/command", Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: " command", Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: " /command", Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: "command args", Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "args" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: "command  args", Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "args" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: "command arg1 arg2", Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 1 || command != "command" || args != "arg1 arg2" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{Text: "command@bot arg1 arg2", Chat: &tg.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "arg1 arg2" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{NewChatMembers: &([]tg.User{}), Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{NewChatMembers: &([]tg.User{{ID: 2}}), Chat: &tg.Chat{ID: 1}}}, "", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{Message: &tg.Message{NewChatMembers: &([]tg.User{{ID: 2}}), Chat: &tg.Chat{ID: 1}}}, "", []int64{2})
	if chatID != 1 || command != "start" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{ChannelPost: &tg.Message{Text: "command", Chat: &tg.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{ChannelPost: &tg.Message{Text: "command@bot", Chat: &tg.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{ChannelPost: &tg.Message{Text: "command @bot", Chat: &tg.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{ChannelPost: &tg.Message{Text: " /command@bot", Chat: &tg.Chat{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
}

func TestUnconfirmedStatusConsistency(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	// Insert first status change for channel "a"
	w.db.InsertStatusChanges([]db.StatusChange{
		{ChannelID: "a", Status: cmdlib.StatusOnline},
	}, 100)

	channel := w.db.MaybeChannel("a")
	if channel == nil {
		t.Fatal("channel not found")
	}
	if channel.UnconfirmedStatus != cmdlib.StatusOnline || channel.UnconfirmedTimestamp != 100 {
		t.Errorf("unexpected unconfirmed status: %+v", channel)
	}
	if channel.PrevUnconfirmedStatus != cmdlib.StatusUnknown || channel.PrevUnconfirmedTimestamp != 0 {
		t.Errorf("unexpected prev unconfirmed status: %+v", channel)
	}

	// Insert second status change — prev should be updated
	w.db.InsertStatusChanges([]db.StatusChange{
		{ChannelID: "a", Status: cmdlib.StatusOffline},
	}, 200)

	channel = w.db.MaybeChannel("a")
	if channel.UnconfirmedStatus != cmdlib.StatusOffline || channel.UnconfirmedTimestamp != 200 {
		t.Errorf("unexpected unconfirmed status: %+v", channel)
	}
	if channel.PrevUnconfirmedStatus != cmdlib.StatusOnline || channel.PrevUnconfirmedTimestamp != 100 {
		t.Errorf("unexpected prev unconfirmed status: %+v", channel)
	}

	// Insert third status change — prev should shift
	w.db.InsertStatusChanges([]db.StatusChange{
		{ChannelID: "a", Status: cmdlib.StatusOnline},
	}, 300)

	channel = w.db.MaybeChannel("a")
	if channel.UnconfirmedStatus != cmdlib.StatusOnline || channel.UnconfirmedTimestamp != 300 {
		t.Errorf("unexpected unconfirmed status: %+v", channel)
	}
	if channel.PrevUnconfirmedStatus != cmdlib.StatusOffline || channel.PrevUnconfirmedTimestamp != 200 {
		t.Errorf("unexpected prev unconfirmed status: %+v", channel)
	}
}

func checkInv(w *worker, t *testing.T) {
	a := map[string]db.StatusChange{}
	var recStatus db.StatusChange
	w.db.MustQuery(`
		select channel_id, status, timestamp
		from (
			select *, row_number() over (partition by channel_id order by timestamp desc) as row
			from status_changes
		)
		where row = 1`,
		nil,
		db.ScanTo{&recStatus.ChannelID, &recStatus.Status, &recStatus.Timestamp},
		func() { a[recStatus.ChannelID] = recStatus })

	if !reflect.DeepEqual(a, queryLastStatusChanges(&w.db)) {
		t.Errorf("unexpected inv check result, statuses: %v, site statuses: %v", a, queryLastStatusChanges(&w.db))
		t.Log(string(debug.Stack()))
	}
	// Check unconfirmed status consistency — channels table must match last two status_changes
	type lastTwo struct {
		unconfirmed, prev db.StatusChange
	}
	fromStatusChanges := map[string]lastTwo{}
	var sc db.StatusChange
	var row int
	w.db.MustQuery(`
		select channel_id, status, timestamp, row
		from (
			select *, row_number() over (partition by channel_id order by timestamp desc) as row
			from status_changes
		)
		where row <= 2
		order by channel_id, row`,
		nil,
		db.ScanTo{&sc.ChannelID, &sc.Status, &sc.Timestamp, &row},
		func() {
			entry := fromStatusChanges[sc.ChannelID]
			if row == 1 {
				entry.unconfirmed = sc
			} else {
				entry.prev = sc
			}
			fromStatusChanges[sc.ChannelID] = entry
		})

	var channel db.Channel
	w.db.MustQuery(`
		select channel_id, unconfirmed_status, unconfirmed_timestamp, prev_unconfirmed_status, prev_unconfirmed_timestamp
		from channels
		where unconfirmed_timestamp > 0`,
		nil,
		db.ScanTo{&channel.ChannelID, &channel.UnconfirmedStatus, &channel.UnconfirmedTimestamp, &channel.PrevUnconfirmedStatus, &channel.PrevUnconfirmedTimestamp},
		func() {
			expected := fromStatusChanges[channel.ChannelID]
			if channel.UnconfirmedStatus != expected.unconfirmed.Status ||
				channel.UnconfirmedTimestamp != expected.unconfirmed.Timestamp {
				t.Errorf("unconfirmed status mismatch for %s: channel=%+v, expected=%+v", channel.ChannelID, channel, expected)
				t.Log(string(debug.Stack()))
			}
			if channel.PrevUnconfirmedStatus != expected.prev.Status ||
				channel.PrevUnconfirmedTimestamp != expected.prev.Timestamp {
				t.Errorf("prev unconfirmed status mismatch for %s: channel=%+v, expected=%+v", channel.ChannelID, channel, expected)
				t.Log(string(debug.Stack()))
			}
		})

	// Check for consecutive same statuses — should never happen
	var badChannelID string
	var badStatus cmdlib.StatusKind
	w.db.MustQuery(
		`
		with periods as (
			select
				channel_id,
				status,
				lead(status) over (partition by channel_id order by timestamp) as next_status
			from status_changes
		)
		select channel_id, status
		from periods
		where status = next_status
		`,
		nil,
		db.ScanTo{&badChannelID, &badStatus},
		func() {
			t.Errorf("consecutive same status found for %s: %v", badChannelID, badStatus)
			t.Log(string(debug.Stack()))
		})
}

func TestAddChannel(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.db.AddUser(1, 3, 0, "private")

	// Add channel that doesn't exist — should insert with confirmed=0 and return false
	if w.addChannel("test", 1, "newmodel", 100) {
		t.Error("expected addChannel to return false for new channel")
	}
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "newmodel") != 0 {
		t.Error("expected confirmed=0 for new channel")
	}
	// Drain the "checking channel" message
	<-w.highPriorityMsg

	// Add channel that exists with online status — should return true
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status) values ($1, $2)",
		"onlinemodel",
		cmdlib.StatusOnline,
	)
	if !w.addChannel("test", 1, "onlinemodel", 100) {
		t.Error("expected addChannel to return true for existing channel")
	}
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "onlinemodel") != 1 {
		t.Error("expected confirmed=1 for existing channel")
	}
	// Drain messages
	<-w.highPriorityMsg
	nots := w.db.NewNotifications()
	if len(nots) != 1 || nots[0].Status != cmdlib.StatusOnline {
		t.Errorf("expected online notification, got %+v", nots)
	}

	// Add channel that exists with offline status — should return true
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status) values ($1, $2)",
		"offlinemodel",
		cmdlib.StatusOffline,
	)
	if !w.addChannel("test", 1, "offlinemodel", 100) {
		t.Error("expected addChannel to return true for existing offline channel")
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

	// Insert unconfirmed subscription
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "a", 0,
	)

	// Confirm the subscription
	w.db.ConfirmSub(db.Subscription{Endpoint: "test", ChatID: 1, ChannelID: "a"})

	// Check subscription is confirmed
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "a") != 1 {
		t.Error("expected confirmed=1 after ConfirmSub")
	}

	// Check channel was created
	if w.db.MaybeChannel("a") == nil {
		t.Error("expected channel to exist after ConfirmSub")
	}
}

func TestDenySub(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert unconfirmed subscription
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "b", 0,
	)

	// Deny the subscription
	w.db.DenySub(db.Subscription{Endpoint: "test", ChatID: 1, ChannelID: "b"})

	// Check subscription is deleted
	if w.db.MustInt("select count(*) from subscriptions where channel_id = $1", "b") != 0 {
		t.Error("expected subscription to be deleted after DenySub")
	}
}

func TestProcessSubsConfirmations(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert subscriptions waiting for confirmation (confirmed=2)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "online_model", 2,
	)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 2, "offline_model", 2,
	)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 3, "notfound_model", 2,
	)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 4, "denied_model", 2,
	)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 5, "notfound_denied_model", 2,
	)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 6, "online_offline_model", 2,
	)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 7, "unknown_model", 2,
	)

	// Process confirmations with checker results
	w.processSubsConfirmations(&cmdlib.ExistenceListResults{
		Channels: map[string]cmdlib.ChannelInfoWithStatus{
			"online_model":          {Status: cmdlib.StatusOnline},
			"offline_model":         {Status: cmdlib.StatusOffline},
			"notfound_model":        {Status: cmdlib.StatusNotFound},
			"denied_model":          {Status: cmdlib.StatusDenied},
			"notfound_denied_model": {Status: cmdlib.StatusNotFound | cmdlib.StatusDenied},
			"online_offline_model":  {Status: cmdlib.StatusOnline | cmdlib.StatusOffline},
			"unknown_model":         {Status: cmdlib.StatusUnknown},
		},
	})

	// Online model should be confirmed
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "online_model") != 1 {
		t.Error("expected online_model to be confirmed")
	}

	// Offline model should be confirmed
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "offline_model") != 1 {
		t.Error("expected offline_model to be confirmed")
	}

	// NotFound model should be denied (deleted)
	if w.db.MustInt("select count(*) from subscriptions where channel_id = $1", "notfound_model") != 0 {
		t.Error("expected notfound_model to be deleted")
	}

	// Denied model should be confirmed (StatusDenied is a valid status)
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "denied_model") != 1 {
		t.Error("expected denied_model to be confirmed")
	}

	// NotFound|Denied model should be confirmed (StatusDenied bit is set)
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "notfound_denied_model") != 1 {
		t.Error("expected notfound_denied_model to be confirmed")
	}

	// Online|Offline model should be confirmed (found but status uncertain)
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "online_offline_model") != 1 {
		t.Error("expected online_offline_model to be confirmed")
	}

	// Unknown model should be denied (deleted)
	if w.db.MustInt("select count(*) from subscriptions where channel_id = $1", "unknown_model") != 0 {
		t.Error("expected unknown_model to be deleted")
	}
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

			// Set up background channels that should remain unchanged
			w.db.MustExec(
				`
					insert into channels
					(channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp)
					values ($1, $2, $3, $4)
				`,
				"always_online", cmdlib.StatusOnline, cmdlib.StatusOnline, 100,
			)
			w.db.MustExec(
				`
					insert into channels
					(channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp)
					values ($1, $2, $3, $4)
				`,
				"always_offline", cmdlib.StatusOffline, cmdlib.StatusOffline, 100,
			)
			w.db.MustExec(
				`
					insert into channels
					(channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp)
					values ($1, $2, $3, $4)
				`,
				"always_unknown", cmdlib.StatusUnknown, cmdlib.StatusUnknown, 100,
			)

			w.db.MustExec(
				`
					insert into channels
					(channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp)
					values ($1, $2, $3, $4)
				`,
				"ch", tt.confirmed, tt.unconfirmed, tt.timestamp,
			)

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

			// Verify background channels were not affected
			if s := w.db.MustInt("select confirmed_status from channels where channel_id = $1", "always_online"); s != int(cmdlib.StatusOnline) {
				t.Errorf("always_online confirmed_status was affected, got %v", s)
			}
			if s := w.db.MustInt("select confirmed_status from channels where channel_id = $1", "always_offline"); s != int(cmdlib.StatusOffline) {
				t.Errorf("always_offline confirmed_status was affected, got %v", s)
			}
			if s := w.db.MustInt("select confirmed_status from channels where channel_id = $1", "always_unknown"); s != int(cmdlib.StatusUnknown) {
				t.Errorf("always_unknown confirmed_status was affected, got %v", s)
			}
		})
	}
}

func TestQueryLastSubscriptionStatuses(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert confirmed subscriptions
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "model_with_status", 1,
	)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 2, "model_without_status", 1,
	)

	// Insert model with unconfirmed_status for one model only
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status, unconfirmed_status) values ($1, $2, $3)",
		"model_with_status", cmdlib.StatusOnline, cmdlib.StatusOnline,
	)

	statuses := w.db.QueryLastSubscriptionStatuses()

	// Model with unconfirmed_status should return correct status
	if statuses["model_with_status"] != cmdlib.StatusOnline {
		t.Errorf("expected model_with_status to be online, got %v", statuses["model_with_status"])
	}

	// Model without models record should return StatusUnknown
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
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "a", 1,
	)

	// Test with OnlineListResults
	result := &cmdlib.OnlineListResults{
		Channels: map[string]cmdlib.ChannelInfo{"a": {ImageURL: "http://a.jpg"}},
	}
	changes, _, _, _ := w.handleCheckerResults(result, 100)
	if changes != 1 {
		t.Errorf("expected 1 change with OnlineListResults, got %d", changes)
	}
	if w.unconfirmedOnlineChannels["a"].ImageURL != "http://a.jpg" {
		t.Errorf("expected ImageURL to be set, got %s", w.unconfirmedOnlineChannels["a"].ImageURL)
	}
	checkUnconfirmedOnlineChannels(w, t)
	checkInv(&w.worker, t)

	// Test ImageURL update for channel that remains online
	result.Channels["a"] = cmdlib.ChannelInfo{ImageURL: "http://a2.jpg"}
	w.handleCheckerResults(result, 101)
	if w.unconfirmedOnlineChannels["a"].ImageURL != "http://a2.jpg" {
		t.Errorf("expected ImageURL to be updated, got %s", w.unconfirmedOnlineChannels["a"].ImageURL)
	}
	checkUnconfirmedOnlineChannels(w, t)
	checkInv(&w.worker, t)

	// Test with FixedListOnlineResults — channel goes offline (not in Channels)
	result2 := &cmdlib.FixedListOnlineResults{
		RequestedChannels: map[string]bool{"a": true},
		Channels:          map[string]cmdlib.ChannelInfo{}, // empty = "a" is offline
	}
	changes, _, _, _ = w.handleCheckerResults(result2, 102)
	if changes != 1 {
		t.Errorf("expected 1 change with FixedListOnlineResults, got %d", changes)
	}
	if _, ok := w.unconfirmedOnlineChannels["a"]; ok {
		t.Error("expected offline channel to be removed from unconfirmedOnlineChannels")
	}
	checkUnconfirmedOnlineChannels(w, t)
	checkInv(&w.worker, t)

	// Channel comes back online (use new map to avoid aliasing with unconfirmedOnlineChannels)
	result2.Channels = map[string]cmdlib.ChannelInfo{"a": {ImageURL: "http://a3.jpg"}}
	w.handleCheckerResults(result2, 103)
	checkUnconfirmedOnlineChannels(w, t)
	checkInv(&w.worker, t)

	// Channel goes offline again (use new empty map)
	result2.Channels = map[string]cmdlib.ChannelInfo{}
	w.handleCheckerResults(result2, 104)
	if _, ok := w.unconfirmedOnlineChannels["a"]; ok {
		t.Error("expected offline channel to be removed from unconfirmedOnlineChannels")
	}
	checkUnconfirmedOnlineChannels(w, t)
	checkInv(&w.worker, t)

	// Test error case (should return early with zero values)
	result3 := cmdlib.NewOnlineListResultsFailed()
	changes, confirmedChanges, nots, elapsed := w.handleCheckerResults(result3, 105)
	if changes != 0 || confirmedChanges != 0 || len(nots) != 0 || elapsed != 0 {
		t.Errorf(
			"expected zero values on error, got changes=%d, confirmedChanges=%d, nots=%d, elapsed=%d",
			changes, confirmedChanges, len(nots), elapsed)
	}

	// Test error case with FixedListResults
	result4 := cmdlib.NewFixedListOnlineResultsFailed()
	changes, confirmedChanges, nots, elapsed = w.handleCheckerResults(result4, 106)
	if changes != 0 || confirmedChanges != 0 || len(nots) != 0 || elapsed != 0 {
		t.Errorf(
			"expected zero values on error with FixedListResults, got changes=%d, confirmedChanges=%d, nots=%d, elapsed=%d",
			changes, confirmedChanges, len(nots), elapsed)
	}
}

func TestUnsubscribeBeforeRestart(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	// Subscribe to "a" and "b"
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "a", 1)
	w.db.MustExec(
		"insert into subscriptions (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "b", 1)

	// Both channels come online
	result := &cmdlib.FixedListOnlineResults{
		RequestedChannels: map[string]bool{"a": true, "b": true},
		Channels: map[string]cmdlib.ChannelInfo{
			"a": {},
			"b": {},
		},
	}
	w.handleCheckerResults(result, 100)
	checkInv(&w.worker, t)

	// Verify both are online in DB
	if w.db.MaybeChannel("a").UnconfirmedStatus != cmdlib.StatusOnline {
		t.Error("expected 'a' to be online")
	}
	if w.db.MaybeChannel("b").UnconfirmedStatus != cmdlib.StatusOnline {
		t.Error("expected 'b' to be online")
	}

	// Unsubscribe from "a"
	w.db.MustExec("delete from subscriptions where channel_id = $1", "a")

	// Simulate restart: reinitialize cache as would happen on restart
	w.initCache()

	// First query after restart — only "b" is subscribed.
	// "a" is no longer in RequestedChannels since it's unsubscribed.
	result2 := &cmdlib.FixedListOnlineResults{
		RequestedChannels: map[string]bool{"b": true},
		Channels: map[string]cmdlib.ChannelInfo{
			"b": {},
		},
	}
	w.handleCheckerResults(result2, 101)
	checkInv(&w.worker, t)

	// "a" should now have StatusUnknown in DB because it's a known channel
	// but not in RequestedChannels (not subscribed anymore).
	channelA := w.db.MaybeChannel("a")
	if channelA.UnconfirmedStatus != cmdlib.StatusUnknown {
		t.Errorf("expected 'a' to have StatusUnknown, got %v", channelA.UnconfirmedStatus)
	}
}

func TestUnknownChannelFirstOfflineSaved(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	// Add a user
	w.db.AddUser(1, 3, 0, "private")

	// 1. User subscribes to a channel we don't know yet — creates unconfirmed subscription
	// This simulates subscribing to a Twitch channel or a new unknown model
	if w.addChannel("test", 1, "unknown_model", 100) {
		t.Error("expected addChannel to return false for unknown channel")
	}
	// Drain the "checking channel" message
	<-w.highPriorityMsg

	// Verify subscription is unconfirmed
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "unknown_model") != 0 {
		t.Error("expected confirmed=0 for new channel")
	}

	// 2. Subscription is confirmed — checker returns offline status (Twitch returns Online|Offline)
	// First, set subscription to "checking" state (confirmed=2) as queryUnconfirmedSubs would do
	w.db.MustExec("update subscriptions set confirmed = 2 where channel_id = $1", "unknown_model")
	w.processSubsConfirmations(&cmdlib.ExistenceListResults{
		Channels: map[string]cmdlib.ChannelInfoWithStatus{
			// Twitch returns Online|Offline when channel exists but is offline
			"unknown_model": {Status: cmdlib.StatusOnline | cmdlib.StatusOffline},
		},
	})

	// Verify subscription is now confirmed
	if w.db.MustInt("select confirmed from subscriptions where channel_id = $1", "unknown_model") != 1 {
		t.Error("expected confirmed=1 after confirmation")
	}

	// 3. First status update: offline
	// Offline status SHOULD be saved so we can calculate online duration later
	result := &cmdlib.FixedListOnlineResults{
		RequestedChannels: map[string]bool{"unknown_model": true},
		Channels:          map[string]cmdlib.ChannelInfo{}, // empty = offline
	}
	changes, _, _, _ := w.handleCheckerResults(result, 101)
	checkInv(&w.worker, t)

	// 4. Offline status should be recorded for proper online time calculation
	if changes != 1 {
		t.Errorf("expected 1 change for first offline status, got %d", changes)
	}

	// Verify status_change was recorded
	count := w.db.MustInt("select count(*) from status_changes where channel_id = $1", "unknown_model")
	if count != 1 {
		t.Errorf("expected 1 status_change for first offline, got %d", count)
	}

	// Verify channel has offline status
	channel := w.db.MaybeChannel("unknown_model")
	if channel == nil {
		t.Fatal("expected channel to exist")
	}
	if channel.UnconfirmedStatus != cmdlib.StatusOffline {
		t.Errorf("expected unconfirmed status to be offline, got %v", channel.UnconfirmedStatus)
	}

	// 5. Subsequent status update with same offline status should NOT record a new change
	result.Channels = map[string]cmdlib.ChannelInfo{} // use new map to avoid aliasing
	changes, _, _, _ = w.handleCheckerResults(result, 102)
	checkInv(&w.worker, t)
	if changes != 0 {
		t.Errorf("expected 0 changes for same offline status, got %d", changes)
	}

	// Still only 1 status_change
	count = w.db.MustInt("select count(*) from status_changes where channel_id = $1", "unknown_model")
	if count != 1 {
		t.Errorf("expected still 1 status_change, got %d", count)
	}
}

func TestStatusTransitions(t *testing.T) {
	tests := []struct {
		name          string
		subscribed    bool
		dbBefore      *cmdlib.StatusKind // nil means channel doesn't exist in DB
		fixedList     bool
		checkerStatus *cmdlib.StatusKind // nil means channel not in checker result
		dbAfter       *cmdlib.StatusKind // nil means channel shouldn't exist or no change
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
		// Unsubscribed channel tests
		{
			name:          "online list: unsubscribed channel stays online",
			subscribed:    false,
			dbBefore:      nil,
			fixedList:     false,
			checkerStatus: ptr(cmdlib.StatusOnline),
			dbAfter:       ptr(cmdlib.StatusOnline),
		},
		{
			name:          "fixed list: unsubscribed channel stays online",
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
			// Set up background channels that should remain unchanged
			w.db.AddSubscription(1, "always_online", "ep", 1)
			w.db.AddSubscription(1, "always_offline", "ep", 1)
			w.db.AddSubscription(1, "always_unknown", "ep", 1)
			w.db.MustExec(
				"insert into channels (channel_id, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3)",
				"always_online", cmdlib.StatusOnline, 1,
			)
			w.db.MustExec(
				"insert into channels (channel_id, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3)",
				"always_offline", cmdlib.StatusOffline, 1,
			)
			w.db.MustExec(
				"insert into channels (channel_id, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3)",
				"always_unknown", cmdlib.StatusUnknown, 1,
			)
			w.db.MustExec(
				"insert into status_changes (channel_id, status, timestamp) values ($1, $2, $3)",
				"always_online", cmdlib.StatusOnline, 1,
			)
			w.db.MustExec(
				"insert into status_changes (channel_id, status, timestamp) values ($1, $2, $3)",
				"always_offline", cmdlib.StatusOffline, 1,
			)
			w.db.MustExec(
				"insert into status_changes (channel_id, status, timestamp) values ($1, $2, $3)",
				"always_unknown", cmdlib.StatusUnknown, 1,
			)

			// Initialize cache after setting up background channels
			w.initCache()

			// Always subscribe during setup if we need to set initial state
			// (subscription is needed to track the channel)
			if tt.dbBefore != nil || tt.subscribed {
				w.db.AddSubscription(1, "ch", "ep", 1)
				// Create channel entry like ConfirmSub does for confirmed subscriptions
				w.db.MustExec("insert into channels (channel_id) values ($1) on conflict(channel_id) do nothing", "ch")
			}

			// Include background channels in RequestedChannels to prevent them from being set to unknown
			bgChannels := map[string]bool{"always_online": true, "always_offline": true}

			if tt.dbBefore != nil {
				if tt.fixedList {
					setupResult := &cmdlib.FixedListOnlineResults{
						RequestedChannels: map[string]bool{"ch": true, "always_online": true, "always_offline": true},
						Channels:          map[string]cmdlib.ChannelInfo{"always_online": {}},
					}
					if *tt.dbBefore == cmdlib.StatusOnline {
						setupResult.Channels["ch"] = cmdlib.ChannelInfo{}
					}
					w.handleCheckerResults(setupResult, 100)
				} else {
					setupResult := &cmdlib.OnlineListResults{
						Channels: map[string]cmdlib.ChannelInfo{"always_online": {}},
					}
					if *tt.dbBefore == cmdlib.StatusOnline {
						setupResult.Channels["ch"] = cmdlib.ChannelInfo{}
					}
					w.handleCheckerResults(setupResult, 100)
				}
				checkInv(&w.worker, t)
			}

			if tt.fixedList {
				result := &cmdlib.FixedListOnlineResults{
					RequestedChannels: bgChannels,
					Channels:          map[string]cmdlib.ChannelInfo{"always_online": {}},
				}
				if tt.subscribed {
					result.RequestedChannels["ch"] = true
				}
				if tt.checkerStatus != nil && *tt.checkerStatus == cmdlib.StatusOnline {
					result.Channels["ch"] = cmdlib.ChannelInfo{}
				}
				w.handleCheckerResults(result, 101)
			} else {
				result := &cmdlib.OnlineListResults{
					Channels: map[string]cmdlib.ChannelInfo{"always_online": {}},
				}
				if tt.checkerStatus != nil && *tt.checkerStatus == cmdlib.StatusOnline {
					result.Channels["ch"] = cmdlib.ChannelInfo{}
				}
				w.handleCheckerResults(result, 101)
			}
			checkInv(&w.worker, t)

			channel := w.db.MaybeChannel("ch")
			if tt.dbAfter == nil {
				if channel != nil {
					t.Errorf("expected no channel in DB, got %v", channel)
				}
			} else {
				if channel == nil {
					t.Errorf("expected channel in DB with status %v, got nil", *tt.dbAfter)
				} else if channel.UnconfirmedStatus != *tt.dbAfter {
					t.Errorf("expected status %v, got %v", *tt.dbAfter, channel.UnconfirmedStatus)
				}
			}

			// Verify background channels were not affected
			if ch := w.db.MaybeChannel("always_online"); ch == nil || ch.UnconfirmedStatus != cmdlib.StatusOnline {
				t.Errorf("always_online was affected, got %v", ch)
			}
			if ch := w.db.MaybeChannel("always_offline"); ch == nil || ch.UnconfirmedStatus != cmdlib.StatusOffline {
				t.Errorf("always_offline was affected, got %v", ch)
			}
			if ch := w.db.MaybeChannel("always_unknown"); ch == nil || ch.UnconfirmedStatus != cmdlib.StatusUnknown {
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

	nots := []db.Notification{
		{ChatID: 100, Endpoint: "test", ChannelID: "a", Status: cmdlib.StatusOnline, Priority: 0},
		{ChatID: 101, Endpoint: "test", ChannelID: "b", Status: cmdlib.StatusOnline, Priority: 1},
		{ChatID: 200, Endpoint: "test", ChannelID: "c", Status: cmdlib.StatusOnline, Priority: 0},
		{ChatID: 201, Endpoint: "test", ChannelID: "d", Status: cmdlib.StatusOnline, Priority: 1},
	}

	w.notifyOfStatuses(w.highPriorityMsg, w.lowPriorityMsg, nots)

	if len(w.lowPriorityMsg) != 2 {
		t.Errorf("expected 2 low priority messages, got %d", len(w.lowPriorityMsg))
	}
	if len(w.highPriorityMsg) != 2 {
		t.Errorf("expected 2 high priority messages, got %d", len(w.highPriorityMsg))
	}
}
