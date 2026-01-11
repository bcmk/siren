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

func isConfirmedOnline(w *testWorker, channelID string) bool {
	m := w.db.MaybeChannel(channelID)
	return m != nil && m.ConfirmedStatus == cmdlib.StatusOnline
}

func confirmedOnlineCount(w *testWorker) int {
	return w.db.MustInt("select count(*) from channels where confirmed_status = $1", cmdlib.StatusOnline)
}

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
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 1, "a")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 2, "b")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 3, "c")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 3, "c2")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 3, "c3")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 4, "d")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 5, "d")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 6, "e")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 7, "f")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep2", 6, "e")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep2", 7, "f")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep2", 8, "g")
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

func TestUpdateStatus(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 18); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 19); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 20); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 21); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if confirmedOnlineCount(w) != 1 {
		t.Error("wrong online channels count")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 22); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 23); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if confirmedOnlineCount(w) != 1 {
		t.Error("wrong online channels count")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 24); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 29); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 31); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 32); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 33); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 34)
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 35); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 36); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 37); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 41); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 42); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 48); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 49)
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 50)
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 50)
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 52)
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"b": {Status: cmdlib.StatusOnline}}, 53)
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"b": {Status: cmdlib.StatusOffline}}, 54)
	checkInv(&w.worker, t)
	if !isConfirmedOnline(w, "b") {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}, "b": {Status: cmdlib.StatusOnline}}, 55)
	checkInv(&w.worker, t)
	if !isConfirmedOnline(w, "b") {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if confirmedOnlineCount(w) != 2 {
		t.Error("wrong online channels count")
	}
	checkInv(&w.worker, t)
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"b": {Status: cmdlib.StatusOffline}}, 56)
	if count := confirmedOnlineCount(w); count != 2 {
		t.Errorf("wrong online channels count: %d", count)
	}
	w.cfg.OfflineNotifications = true
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 57); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 68); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 69); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusUnknown}}, 70); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 71); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusUnknown}}, 72)
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 73); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{}, 79); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusUnknown}}, 80); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 81); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !isConfirmedOnline(w, "a") {
		t.Error("wrong active status")
	}
	_ = w.db.Close()
}

func TestUpdateNotifications(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 1, "a")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 2, "b")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 3, "a")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 3, "c")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep1", 4, "d")
	w.db.MustExec("insert into signals (endpoint, chat_id, channel_id) values ($1, $2, $3)", "ep2", 4, "d")

	w.db.MustExec("insert into users (chat_id) values ($1)", 1)
	w.db.MustExec("insert into users (chat_id) values ($1)", 2)
	w.db.MustExec("insert into users (chat_id) values ($1)", 3)
	w.db.MustExec("insert into users (chat_id) values ($1)", 4)

	if _, _, nots, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"x": {Status: cmdlib.StatusOnline}}, 1); len(nots) != 0 {
		t.Error("unexpected notification number")
	}
	if _, _, nots, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline}}, 2); len(nots) != 2 {
		t.Error("unexpected notification number")
	}
	if _, _, nots, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 3); len(nots) != 0 {
		t.Error("unexpected notification number")
	}
	if _, _, nots, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}}, 8); len(nots) != 2 {
		t.Error("unexpected notification number")
	}
	if _, _, nots, _ := w.applyStatusUpdates(map[string]cmdlib.ChannelInfo{"d": {Status: cmdlib.StatusOnline}}, 2); len(nots) != 2 {
		t.Error("unexpected notification number")
	}
	_ = w.db.Close()
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
		{ChannelID: "a", Status: cmdlib.StatusOnline, Timestamp: 100},
	})

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
		{ChannelID: "a", Status: cmdlib.StatusOffline, Timestamp: 200},
	})

	channel = w.db.MaybeChannel("a")
	if channel.UnconfirmedStatus != cmdlib.StatusOffline || channel.UnconfirmedTimestamp != 200 {
		t.Errorf("unexpected unconfirmed status: %+v", channel)
	}
	if channel.PrevUnconfirmedStatus != cmdlib.StatusOnline || channel.PrevUnconfirmedTimestamp != 100 {
		t.Errorf("unexpected prev unconfirmed status: %+v", channel)
	}

	// Insert third status change — prev should shift
	w.db.InsertStatusChanges([]db.StatusChange{
		{ChannelID: "a", Status: cmdlib.StatusOnline, Timestamp: 300},
	})

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
}

func TestAddChannel(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.db.AddUser(1, 3)

	// Add channel that doesn't exist — should insert with confirmed=0 and return false
	if w.addChannel("test", 1, "newmodel", 100) {
		t.Error("expected addChannel to return false for new channel")
	}
	if w.db.MustInt("select confirmed from signals where channel_id = $1", "newmodel") != 0 {
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
	if w.db.MustInt("select confirmed from signals where channel_id = $1", "onlinemodel") != 1 {
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
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "a", 0,
	)

	// Confirm the subscription
	w.db.ConfirmSub(db.Subscription{Endpoint: "test", ChatID: 1, ChannelID: "a"})

	// Check signal is confirmed
	if w.db.MustInt("select confirmed from signals where channel_id = $1", "a") != 1 {
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
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "b", 0,
	)

	// Deny the subscription
	w.db.DenySub(db.Subscription{Endpoint: "test", ChatID: 1, ChannelID: "b"})

	// Check signal is deleted
	if w.db.MustInt("select count(*) from signals where channel_id = $1", "b") != 0 {
		t.Error("expected signal to be deleted after DenySub")
	}
}

func TestProcessSubsConfirmations(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert subscriptions waiting for confirmation (confirmed=2)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "online_model", 2,
	)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 2, "offline_model", 2,
	)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 3, "notfound_model", 2,
	)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 4, "denied_model", 2,
	)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 5, "notfound_denied_model", 2,
	)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 6, "online_offline_model", 2,
	)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 7, "unknown_model", 2,
	)

	// Process confirmations with checker results
	w.processSubsConfirmations(cmdlib.StatusResults{
		Channels: map[string]cmdlib.ChannelInfo{
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
	if w.db.MustInt("select confirmed from signals where channel_id = $1", "online_model") != 1 {
		t.Error("expected online_model to be confirmed")
	}

	// Offline model should be confirmed
	if w.db.MustInt("select confirmed from signals where channel_id = $1", "offline_model") != 1 {
		t.Error("expected offline_model to be confirmed")
	}

	// NotFound model should be denied (deleted)
	if w.db.MustInt("select count(*) from signals where channel_id = $1", "notfound_model") != 0 {
		t.Error("expected notfound_model to be deleted")
	}

	// Denied model should be confirmed (StatusDenied is a valid status)
	if w.db.MustInt("select confirmed from signals where channel_id = $1", "denied_model") != 1 {
		t.Error("expected denied_model to be confirmed")
	}

	// NotFound|Denied model should be confirmed (StatusDenied bit is set)
	if w.db.MustInt("select confirmed from signals where channel_id = $1", "notfound_denied_model") != 1 {
		t.Error("expected notfound_denied_model to be confirmed")
	}

	// Online|Offline model should be confirmed (found but status uncertain)
	if w.db.MustInt("select confirmed from signals where channel_id = $1", "online_offline_model") != 1 {
		t.Error("expected online_offline_model to be confirmed")
	}

	// Unknown model should be denied (deleted)
	if w.db.MustInt("select count(*) from signals where channel_id = $1", "unknown_model") != 0 {
		t.Error("expected unknown_model to be deleted")
	}
}

func TestConfirmStatusChanges(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Test config: onlineSeconds=0, offlineSeconds=5

	// Case 1: confirmed=offline, unconfirmed=online → confirm immediately (onlineSeconds=0)
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3, $4)",
		"offline_to_online", cmdlib.StatusOffline, cmdlib.StatusOnline, 100,
	)

	// Case 2: confirmed=online, unconfirmed=offline, timing met → confirm offline
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3, $4)",
		"online_to_offline_met", cmdlib.StatusOnline, cmdlib.StatusOffline, 100,
	)

	// Case 3: confirmed=online, unconfirmed=offline, timing NOT met → no change
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3, $4)",
		"online_to_offline_not_met", cmdlib.StatusOnline, cmdlib.StatusOffline, 103,
	)

	// Case 4: confirmed=online, unconfirmed=unknown → confirm unknown immediately
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3, $4)",
		"online_to_unknown", cmdlib.StatusOnline, cmdlib.StatusUnknown, 100,
	)

	// Case 5: confirmed=offline, unconfirmed=unknown → confirm unknown immediately
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3, $4)",
		"offline_to_unknown", cmdlib.StatusOffline, cmdlib.StatusUnknown, 100,
	)

	// Case 6: same status (online=online) → NO change (XOR fails)
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3, $4)",
		"online_to_online", cmdlib.StatusOnline, cmdlib.StatusOnline, 100,
	)

	// Case 7: same status (offline=offline) → NO change (XOR fails)
	w.db.MustExec(
		"insert into channels (channel_id, confirmed_status, unconfirmed_status, unconfirmed_timestamp) values ($1, $2, $3, $4)",
		"offline_to_offline", cmdlib.StatusOffline, cmdlib.StatusOffline, 100,
	)

	// Run confirmation at now=105 (5 seconds after timestamp 100)
	changes := w.db.ConfirmStatusChanges(105, w.cfg.StatusConfirmationSeconds.Online, w.cfg.StatusConfirmationSeconds.Offline)

	changesMap := map[string]cmdlib.StatusKind{}
	for _, c := range changes {
		changesMap[c.ChannelID] = c.Status
	}

	// Case 1: should confirm online
	if status, ok := changesMap["offline_to_online"]; !ok || status != cmdlib.StatusOnline {
		t.Errorf("expected offline_to_online to be confirmed online, got %v, ok=%v", status, ok)
	}

	// Case 2: should confirm offline (timing met: 105-100=5 >= 5)
	if status, ok := changesMap["online_to_offline_met"]; !ok || status != cmdlib.StatusOffline {
		t.Errorf("expected online_to_offline_met to be confirmed offline, got %v, ok=%v", status, ok)
	}

	// Case 3: should NOT be confirmed (timing not met: 105-103=2 < 5)
	if _, ok := changesMap["online_to_offline_not_met"]; ok {
		t.Error("expected online_to_offline_not_met to NOT be confirmed")
	}

	// Case 4: should confirm unknown
	if status, ok := changesMap["online_to_unknown"]; !ok || status != cmdlib.StatusUnknown {
		t.Errorf("expected online_to_unknown to be confirmed with unknown status, got %v, ok=%v", status, ok)
	}

	// Case 5: should confirm unknown
	if status, ok := changesMap["offline_to_unknown"]; !ok || status != cmdlib.StatusUnknown {
		t.Errorf("expected offline_to_unknown to be confirmed with unknown status, got %v, ok=%v", status, ok)
	}

	// Case 6: should NOT be confirmed (XOR fails: both = 2)
	if _, ok := changesMap["online_to_online"]; ok {
		t.Error("expected online_to_online to NOT be confirmed")
	}

	// Case 7: should NOT be confirmed (XOR fails: both != 2)
	if _, ok := changesMap["offline_to_offline"]; ok {
		t.Error("expected offline_to_offline to NOT be confirmed")
	}

	// Verify DB state after confirmation
	if w.db.MustInt("select confirmed_status from channels where channel_id = $1", "offline_to_online") != int(cmdlib.StatusOnline) {
		t.Error("expected offline_to_online confirmed_status to be online in DB")
	}
	if w.db.MustInt("select confirmed_status from channels where channel_id = $1", "online_to_offline_met") != int(cmdlib.StatusOffline) {
		t.Error("expected online_to_offline_met confirmed_status to be offline in DB")
	}
	if w.db.MustInt("select confirmed_status from channels where channel_id = $1", "online_to_offline_not_met") != int(cmdlib.StatusOnline) {
		t.Error("expected online_to_offline_not_met confirmed_status to remain online in DB")
	}
	if w.db.MustInt("select confirmed_status from channels where channel_id = $1", "online_to_unknown") != int(cmdlib.StatusUnknown) {
		t.Error("expected online_to_unknown confirmed_status to be unknown in DB")
	}
}

func TestQueryLastSubscriptionStatuses(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))

	// Insert confirmed subscriptions
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "model_with_status", 1,
	)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
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
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "a", 1,
	)

	// Test with Channels == nil (uses onlineListUpdater)
	request := cmdlib.StatusRequest{Channels: nil}
	result := cmdlib.StatusResults{
		Request:  &request,
		Channels: map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOnline, ImageURL: "http://a.jpg"}},
	}
	changes, _, _, _ := w.handleStatusUpdates(result, 100)
	if changes != 1 {
		t.Errorf("expected 1 change with onlineListUpdater, got %d", changes)
	}
	if w.unconfirmedOnlineChannels["a"].ImageURL != "http://a.jpg" {
		t.Errorf("expected ImageURL to be set, got %s", w.unconfirmedOnlineChannels["a"].ImageURL)
	}
	checkUnconfirmedOnlineChannels(w, t)

	// Test ImageURL update for channel that remains online
	result.Channels["a"] = cmdlib.ChannelInfo{Status: cmdlib.StatusOnline, ImageURL: "http://a2.jpg"}
	w.handleStatusUpdates(result, 101)
	if w.unconfirmedOnlineChannels["a"].ImageURL != "http://a2.jpg" {
		t.Errorf("expected ImageURL to be updated, got %s", w.unconfirmedOnlineChannels["a"].ImageURL)
	}
	checkUnconfirmedOnlineChannels(w, t)

	// Test with Channels != nil (uses fixedListUpdater)
	request2 := cmdlib.StatusRequest{Channels: map[string]bool{"a": true}}
	result2 := cmdlib.StatusResults{
		Request:  &request2,
		Channels: map[string]cmdlib.ChannelInfo{"a": {Status: cmdlib.StatusOffline}},
	}
	changes, _, _, _ = w.handleStatusUpdates(result2, 102)
	if changes != 1 {
		t.Errorf("expected 1 change with fixedListUpdater, got %d", changes)
	}
	if _, ok := w.unconfirmedOnlineChannels["a"]; ok {
		t.Error("expected offline channel to be removed from unconfirmedOnlineChannels")
	}
	checkUnconfirmedOnlineChannels(w, t)

	// Test unknown status also removes from unconfirmedOnlineChannels
	result2.Channels["a"] = cmdlib.ChannelInfo{Status: cmdlib.StatusOnline, ImageURL: "http://a3.jpg"}
	w.handleStatusUpdates(result2, 103)
	checkUnconfirmedOnlineChannels(w, t)
	result2.Channels["a"] = cmdlib.ChannelInfo{Status: cmdlib.StatusUnknown}
	w.handleStatusUpdates(result2, 104)
	if _, ok := w.unconfirmedOnlineChannels["a"]; ok {
		t.Error("expected unknown channel to be removed from unconfirmedOnlineChannels")
	}
	checkUnconfirmedOnlineChannels(w, t)

	// Test error case (should return early with zero values)
	request3 := cmdlib.StatusRequest{Channels: nil}
	result3 := cmdlib.StatusResults{
		Request: &request3,
		Error:   true,
	}
	changes, confirmedChanges, nots, elapsed := w.handleStatusUpdates(result3, 105)
	if changes != 0 || confirmedChanges != 0 || len(nots) != 0 || elapsed != 0 {
		t.Errorf(
			"expected zero values on error, got changes=%d, confirmedChanges=%d, nots=%d, elapsed=%d",
			changes, confirmedChanges, len(nots), elapsed)
	}

	// Test error case with fixedListUpdater (Channels != nil)
	request4 := cmdlib.StatusRequest{Channels: map[string]bool{"a": true}}
	result4 := cmdlib.StatusResults{
		Request: &request4,
		Error:   true,
	}
	changes, confirmedChanges, nots, elapsed = w.handleStatusUpdates(result4, 106)
	if changes != 0 || confirmedChanges != 0 || len(nots) != 0 || elapsed != 0 {
		t.Errorf(
			"expected zero values on error with fixedListUpdater, got changes=%d, confirmedChanges=%d, nots=%d, elapsed=%d",
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
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "a", 1)
	w.db.MustExec(
		"insert into signals (endpoint, chat_id, channel_id, confirmed) values ($1, $2, $3, $4)",
		"test", 1, "b", 1)

	// Both channels come online
	request := cmdlib.StatusRequest{Channels: map[string]bool{"a": true, "b": true}}
	result := cmdlib.StatusResults{
		Request: &request,
		Channels: map[string]cmdlib.ChannelInfo{
			"a": {Status: cmdlib.StatusOnline},
			"b": {Status: cmdlib.StatusOnline},
		},
	}
	w.handleStatusUpdates(result, 100)

	// Verify both are online in DB
	if w.db.MaybeChannel("a").UnconfirmedStatus != cmdlib.StatusOnline {
		t.Error("expected 'a' to be online")
	}
	if w.db.MaybeChannel("b").UnconfirmedStatus != cmdlib.StatusOnline {
		t.Error("expected 'b' to be online")
	}

	// Unsubscribe from "a"
	w.db.MustExec("delete from signals where channel_id = $1", "a")

	// Simulate restart: reinitialize cache as would happen on restart
	w.unconfirmedOnlineChannels = map[string]cmdlib.ChannelInfo{}
	for channelID := range w.db.QueryLastOnlineChannels() {
		w.unconfirmedOnlineChannels[channelID] = cmdlib.ChannelInfo{Status: cmdlib.StatusOnline}
	}

	// First query after restart — only "b" is subscribed
	request2 := cmdlib.StatusRequest{Channels: map[string]bool{"b": true}}
	result2 := cmdlib.StatusResults{
		Request: &request2,
		Channels: map[string]cmdlib.ChannelInfo{
			"b": {Status: cmdlib.StatusOnline},
		},
	}
	w.handleStatusUpdates(result2, 101)

	// "a" should now have StatusUnknown in DB
	channelA := w.db.MaybeChannel("a")
	if channelA.UnconfirmedStatus != cmdlib.StatusUnknown {
		t.Errorf("expected 'a' to have StatusUnknown, got %v", channelA.UnconfirmedStatus)
	}
}
