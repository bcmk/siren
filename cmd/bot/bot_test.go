package main

import (
	"reflect"
	"runtime/debug"
	"testing"

	"github.com/bcmk/siren/internal/db"
	"github.com/bcmk/siren/lib/cmdlib"
	tg "github.com/bcmk/telegram-bot-api"
)

func TestSql(t *testing.T) {
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 1, "a")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 2, "b")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 3, "c")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 3, "c2")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 3, "c3")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 4, "d")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 5, "d")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 6, "e")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 7, "f")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep2", 6, "e")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep2", 7, "f")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep2", 8, "g")
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 2, 0)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 3, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 4, w.cfg.BlockThreshold-1)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 5, w.cfg.BlockThreshold+1)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 6, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 7, w.cfg.BlockThreshold)
	w.db.MustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep2", 7, w.cfg.BlockThreshold)
	w.db.MustExec("insert into models (model_id, status) values ($1, $2)", "a", cmdlib.StatusOnline)
	w.db.MustExec("insert into models (model_id, status) values ($1, $2)", "b", cmdlib.StatusOnline)
	w.db.MustExec("insert into models (model_id, status) values ($1, $2)", "c", cmdlib.StatusOnline)
	w.db.MustExec("insert into models (model_id, status) values ($1, $2)", "c2", cmdlib.StatusOnline)
	broadcastChats := w.db.BroadcastChats("ep1")
	if !reflect.DeepEqual(broadcastChats, []int64{1, 2, 3, 4, 5, 6, 7}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	broadcastChats = w.db.BroadcastChats("ep2")
	if !reflect.DeepEqual(broadcastChats, []int64{6, 7, 8}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	chatsForModel, endpoints := w.chatsForModel("a")
	if !reflect.DeepEqual(endpoints, []string{"ep1"}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	if !reflect.DeepEqual(chatsForModel, []int64{1}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel, _ = w.chatsForModel("b")
	if !reflect.DeepEqual(chatsForModel, []int64{2}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel, _ = w.chatsForModel("c")
	if !reflect.DeepEqual(chatsForModel, []int64{3}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel, _ = w.chatsForModel("d")
	if !reflect.DeepEqual(chatsForModel, []int64{4, 5}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel, _ = w.chatsForModel("e")
	if !reflect.DeepEqual(chatsForModel, []int64{6, 6}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel, _ = w.chatsForModel("f")
	if !reflect.DeepEqual(chatsForModel, []int64{7, 7}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	w.db.IncrementBlock("ep1", 2)
	w.db.IncrementBlock("ep1", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep1") != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.db.IncrementBlock("ep2", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.db.ResetBlock("ep1", 2)
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep1") != 0 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	if w.db.MustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.db.IncrementBlock("ep1", 1)
	w.db.IncrementBlock("ep1", 1)
	if w.db.MustInt("select block from block where chat_id = $1", 1) != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	statuses := w.db.StatusesForChat("ep1", 3)
	if !reflect.DeepEqual(statuses, []db.Model{
		{ModelID: "c", Status: cmdlib.StatusOnline},
		{ModelID: "c2", Status: cmdlib.StatusOnline}}) {
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
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 18); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 19); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 20); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 21); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if len(w.ourOnline) != 1 {
		t.Error("wrong online models count")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 22); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 23); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if len(w.ourOnline) != 1 {
		t.Error("wrong online models count")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 24); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 29); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 31); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 32); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 33); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{}, 34)
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 35); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 36); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 37); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 41); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 42); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 48); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{}, 49)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{}, 50)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 50)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 52)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "b", Status: cmdlib.StatusOnline}}, 53)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "b", Status: cmdlib.StatusOffline}}, 54)
	checkInv(&w.worker, t)
	if !w.ourOnline["b"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}, {ModelID: "b", Status: cmdlib.StatusOnline}}, 55)
	checkInv(&w.worker, t)
	if !w.ourOnline["b"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if len(w.ourOnline) != 2 {
		t.Errorf("wrong online models: %v", w.ourOnline)
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "b", Status: cmdlib.StatusOffline}}, 56)
	if count := len(w.ourOnline); count != 2 {
		t.Errorf("wrong online models count: %d", count)
	}
	w.cfg.OfflineNotifications = true
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 57); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 68); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 69); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusUnknown}}, 70); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 71); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusUnknown}}, 72)
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 73); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{}, 79); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusUnknown}}, 80); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 81); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	_ = w.db.Close()
}

func TestUpdateNotifications(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()

	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 1, "a")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 2, "b")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 3, "a")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 3, "c")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 4, "d")
	w.db.MustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep2", 4, "d")

	w.db.MustExec("insert into users (chat_id) values ($1)", 1)
	w.db.MustExec("insert into users (chat_id) values ($1)", 2)
	w.db.MustExec("insert into users (chat_id) values ($1)", 3)
	w.db.MustExec("insert into users (chat_id) values ($1)", 4)

	if _, _, nots, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "x", Status: cmdlib.StatusOnline}}, 1); len(nots) != 0 {
		t.Error("unexpected notification number")
	}
	if _, _, nots, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 2); len(nots) != 2 {
		t.Error("unexpected notification number")
	}
	if _, _, nots, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 3); len(nots) != 0 {
		t.Error("unexpected notification number")
	}
	if _, _, nots, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 8); len(nots) != 2 {
		t.Error("unexpected notification number")
	}
	if _, _, nots, _ := w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "d", Status: cmdlib.StatusOnline}}, 2); len(nots) != 2 {
		t.Error("unexpected notification number")
	}
	_ = w.db.Close()
}

func queryLastStatusChanges(d *db.Database) map[string]db.StatusChange {
	statusChanges := map[string]db.StatusChange{}
	var statusChange db.StatusChange
	d.MustQuery(
		`select model_id, status, timestamp from status_changes where is_latest = true`,
		nil,
		db.ScanTo{&statusChange.ModelID, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.ModelID] = statusChange })
	return statusChanges
}

func TestCleanStatuses(t *testing.T) {
	const day = 60 * 60 * 24
	w := newTestWorker()
	defer w.terminate()
	w.cfg.StatusConfirmationSeconds.Offline = day + 2
	w.createDatabase(make(chan bool, 1))
	w.initCache()
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, 18)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}, {ModelID: "b", Status: cmdlib.StatusOnline}}, 53)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "b", Status: cmdlib.StatusOffline}}, 55)
	if len(queryLastStatusChanges(&w.db)) != 2 {
		t.Error("wrong number of statuses")
	}
	w.cleanStatusChanges(day + 54)
	if len(queryLastStatusChanges(&w.db)) != 1 {
		t.Logf("site statuses: %v", queryLastStatusChanges(&w.db))
		t.Errorf("wrong number of statuses: %d", len(queryLastStatusChanges(&w.db)))
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{}, day+56)
	if len(w.ourOnline) != 1 {
		t.Logf("site statuses: %v", queryLastStatusChanges(&w.db))
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{}, day+60)
	if len(w.ourOnline) != 0 {
		t.Logf("site statuses: %v", queryLastStatusChanges(&w.db))
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOnline}}, day+100)
	if len(w.ourOnline) != 1 {
		t.Logf("site statuses: %v", queryLastStatusChanges(&w.db))
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	w.cleanStatusChanges(day*100 + 50)
	if len(w.ourOnline) != 1 {
		t.Logf("site statuses: %v", queryLastStatusChanges(&w.db))
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	if len(queryLastStatusChanges(&w.db)) != 0 {
		t.Errorf("wrong number of site statuses: %d", len(queryLastStatusChanges(&w.db)))
	}
	if len(w.siteOnline) != 0 {
		t.Errorf("wrong number of site online models: %d", len(w.siteOnline))
	}
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, day+155)
	if len(w.ourOnline) != 1 {
		t.Logf("site statuses: %v", queryLastStatusChanges(&w.db))
		t.Logf("site online: %v", w.siteOnline)
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	w.processStatusUpdates([]cmdlib.StatusUpdate{{ModelID: "a", Status: cmdlib.StatusOffline}}, 3*day)
	if len(w.ourOnline) != 0 {
		t.Logf("site statuses: %v", queryLastStatusChanges(&w.db))
		t.Logf("site online: %v", w.siteOnline)
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	_ = w.db.Close()
}

func TestNotificationsStorage(t *testing.T) {
	timeDiff := 2
	nots := []db.Notification{
		{
			Endpoint: "endpoint_a",
			ChatID:   1,
			ModelID:  "model_a",
			Status:   cmdlib.StatusUnknown,
			TimeDiff: nil,
			ImageURL: "image_a",
			Social:   false,
			Priority: 1,
			Sound:    false,
			Kind:     db.NotificationPacket,
		},
		{
			Endpoint: "endpoint_b",
			ChatID:   2,
			ModelID:  "model_b",
			Status:   cmdlib.StatusOffline,
			TimeDiff: &timeDiff,
			ImageURL: "image_b",
			Social:   true,
			Priority: 2,
			Sound:    true,
			Kind:     db.ReplyPacket,
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
			Endpoint: "endpoint_c",
			ChatID:   3,
			ModelID:  "model_c",
			Status:   cmdlib.StatusOnline,
			TimeDiff: nil,
			ImageURL: "image_c",
			Social:   true,
			Priority: 3,
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

func TestModels(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.db.MustExec("insert into models (model_id, status) values ($1, $2)", "a", cmdlib.StatusUnknown)
	if w.db.MaybeModel("a") == nil {
		t.Error("unexpected result")
	}
	if w.db.MaybeModel("b") != nil {
		t.Error("unexpected result")
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

func checkInv(w *worker, t *testing.T) {
	a := map[string]db.StatusChange{}
	b := map[string]db.StatusChange{}
	var recStatus db.StatusChange
	w.db.MustQuery(`
		select model_id, status, timestamp
		from (
			select *, row_number() over (partition by model_id order by timestamp desc) as row
			from status_changes
		)
		where row = 1`,
		nil,
		db.ScanTo{&recStatus.ModelID, &recStatus.Status, &recStatus.Timestamp},
		func() { a[recStatus.ModelID] = recStatus })
	w.db.MustQuery(
		`select model_id, status, timestamp from status_changes where is_latest = true`,
		nil,
		db.ScanTo{&recStatus.ModelID, &recStatus.Status, &recStatus.Timestamp},
		func() { b[recStatus.ModelID] = recStatus })

	if !reflect.DeepEqual(a, b) {
		t.Errorf("unexpected inv check result, statuses: %v, last statuses: %v", a, b)
		t.Log(string(debug.Stack()))
	}
	if !reflect.DeepEqual(a, queryLastStatusChanges(&w.db)) {
		t.Errorf("unexpected inv check result, statuses: %v, site statuses: %v", a, queryLastStatusChanges(&w.db))
		t.Log(string(debug.Stack()))
	}
	dbOnline := map[string]bool{}
	var rec db.Model
	w.db.MustQuery(
		`select model_id, status from models`,
		nil,
		db.ScanTo{&rec.ModelID, &rec.Status},
		func() {
			if rec.Status == cmdlib.StatusOnline {
				dbOnline[rec.ModelID] = true
			}
		})
	if !reflect.DeepEqual(w.ourOnline, dbOnline) {
		t.Errorf("unexpected inv check result, left: %v, right: %v", w.ourOnline, dbOnline)
		t.Log(string(debug.Stack()))
	}
}
