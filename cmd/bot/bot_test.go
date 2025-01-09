package main

import (
	"reflect"
	"runtime/debug"
	"testing"

	"github.com/bcmk/siren/lib"
	tg "github.com/bcmk/telegram-bot-api"
)

func TestSql(t *testing.T) {
	linf = func(string, ...interface{}) {}
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.initCache()
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 1, "a")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 2, "b")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 3, "c")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 3, "c2")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 3, "c3")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 4, "d")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 5, "d")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 6, "e")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep1", 7, "f")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep2", 6, "e")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep2", 7, "f")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values ($1, $2, $3)", "ep2", 8, "g")
	w.mustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 2, 0)
	w.mustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 3, w.cfg.BlockThreshold)
	w.mustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 4, w.cfg.BlockThreshold-1)
	w.mustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 5, w.cfg.BlockThreshold+1)
	w.mustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 6, w.cfg.BlockThreshold)
	w.mustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep1", 7, w.cfg.BlockThreshold)
	w.mustExec("insert into block (endpoint, chat_id, block) values ($1, $2, $3)", "ep2", 7, w.cfg.BlockThreshold)
	w.mustExec("insert into models (model_id, status) values ($1, $2)", "a", lib.StatusOnline)
	w.mustExec("insert into models (model_id, status) values ($1, $2)", "b", lib.StatusOnline)
	w.mustExec("insert into models (model_id, status) values ($1, $2)", "c", lib.StatusOnline)
	w.mustExec("insert into models (model_id, status) values ($1, $2)", "c2", lib.StatusOnline)
	models := w.modelsToPoll()
	if !reflect.DeepEqual(models, []string{"a", "d", "e", "g"}) {
		t.Error("unexpected models result", models)
	}
	broadcastChats := w.broadcastChats("ep1")
	if !reflect.DeepEqual(broadcastChats, []int64{1, 2, 3, 4, 5, 6, 7}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	broadcastChats = w.broadcastChats("ep2")
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
	w.incrementBlock("ep1", 2)
	w.incrementBlock("ep1", 2)
	if w.mustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep1") != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.incrementBlock("ep2", 2)
	if w.mustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.resetBlock("ep1", 2)
	if w.mustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep1") != 0 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	if w.mustInt("select block from block where chat_id = $1 and endpoint = $2", 2, "ep2") != 1 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.incrementBlock("ep1", 1)
	w.incrementBlock("ep1", 1)
	if w.mustInt("select block from block where chat_id = $1", 1) != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	statuses := w.statusesForChat("ep1", 3)
	if !reflect.DeepEqual(statuses, []model{
		{modelID: "c", status: lib.StatusOnline},
		{modelID: "c2", status: lib.StatusOnline}}) {
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
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 18); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 19); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 20); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 21); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if len(w.ourOnline) != 1 {
		t.Error("wrong online models count")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 22); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 23); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if len(w.ourOnline) != 1 {
		t.Error("wrong online models count")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 24); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 29); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 31); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 32); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 33); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{}, 34)
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 35); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 36); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 37); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 41); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 42); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 48); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{}, 49)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{}, 50)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 50)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 52)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "b", Status: lib.StatusOnline}}, 53)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "b", Status: lib.StatusOffline}}, 54)
	checkInv(&w.worker, t)
	if !w.ourOnline["b"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}, {ModelID: "b", Status: lib.StatusOnline}}, 55)
	checkInv(&w.worker, t)
	if !w.ourOnline["b"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if len(w.ourOnline) != 2 {
		t.Errorf("wrong online models: %v", w.ourOnline)
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "b", Status: lib.StatusOffline}}, 56)
	if count := len(w.ourOnline); count != 2 {
		t.Errorf("wrong online models count: %d", count)
	}
	w.cfg.OfflineNotifications = true
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 57); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 68); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 69); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusUnknown}}, 70); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 71); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusUnknown}}, 72)
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 73); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{}, 79); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusUnknown}}, 80); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 81); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.ourOnline["a"] {
		t.Error("wrong active status")
	}
	_ = w.db.Close()
}

func TestCleanStatuses(t *testing.T) {
	const day = 60 * 60 * 24
	w := newTestWorker()
	defer w.terminate()
	w.cfg.StatusConfirmationSeconds.Offline = day + 2
	w.createDatabase(make(chan bool, 1))
	w.initCache()
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 18)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}, {ModelID: "b", Status: lib.StatusOnline}}, 53)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "b", Status: lib.StatusOffline}}, 55)
	if len(w.siteStatuses) != 2 {
		t.Error("wrong number of statuses")
	}
	w.cleanStatusChanges(day + 54)
	if len(w.siteStatuses) != 1 {
		t.Logf("site statuses: %v", w.siteStatuses)
		t.Errorf("wrong number of statuses: %d", len(w.siteStatuses))
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{}, day+56)
	if len(w.ourOnline) != 1 {
		t.Logf("site statuses: %v", w.siteStatuses)
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{}, day+60)
	if len(w.ourOnline) != 0 {
		t.Logf("site statuses: %v", w.siteStatuses)
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, day+100)
	if len(w.ourOnline) != 1 {
		t.Logf("site statuses: %v", w.siteStatuses)
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	w.cleanStatusChanges(day*100 + 50)
	if len(w.ourOnline) != 1 {
		t.Logf("site statuses: %v", w.siteStatuses)
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	if len(w.siteStatuses) != 0 {
		t.Errorf("wrong number of site statuses: %d", len(w.siteStatuses))
	}
	if len(w.siteOnline) != 0 {
		t.Errorf("wrong number of site online models: %d", len(w.siteOnline))
	}
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, day+155)
	if len(w.ourOnline) != 1 {
		t.Logf("site statuses: %v", w.siteStatuses)
		t.Logf("site online: %v", w.siteOnline)
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 3*day)
	if len(w.ourOnline) != 0 {
		t.Logf("site statuses: %v", w.siteStatuses)
		t.Logf("site online: %v", w.siteOnline)
		t.Logf("our online: %v", w.ourOnline)
		t.Errorf("wrong number of online: %d", len(w.ourOnline))
	}
	_ = w.db.Close()
}

func TestNotificationsStorage(t *testing.T) {
	timeDiff := 2
	nots := []notification{
		{
			endpoint: "endpoint_a",
			chatID:   1,
			modelID:  "model_a",
			status:   lib.StatusUnknown,
			timeDiff: nil,
			imageURL: "image_a",
			social:   false,
			priority: 1,
			sound:    false,
			kind:     notificationPacket,
		},
		{
			endpoint: "endpoint_b",
			chatID:   2,
			modelID:  "model_b",
			status:   lib.StatusOffline,
			timeDiff: &timeDiff,
			imageURL: "image_b",
			social:   true,
			priority: 2,
			sound:    true,
			kind:     replyPacket,
		},
	}
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.storeNotifications(nots)
	newNots := w.newNotifications()
	nots[0].id = 1
	nots[1].id = 2
	if !reflect.DeepEqual(nots, newNots) {
		t.Errorf("unexpected notifications, expocted: %v, got: %v", nots, newNots)
	}
	nots = []notification{
		{
			endpoint: "endpoint_c",
			chatID:   3,
			modelID:  "model_c",
			status:   lib.StatusOnline,
			timeDiff: nil,
			imageURL: "image_c",
			social:   true,
			priority: 3,
		},
	}
	w.storeNotifications(nots)
	newNots = w.newNotifications()
	nots[0].id = 3
	if !reflect.DeepEqual(nots, newNots) {
		t.Errorf("unexpected notifications, expocted: %v, got: %v", nots, newNots)
	}
	count := w.mustInt("select count(*) from notification_queue")
	if count != 3 {
		t.Errorf("unexpected notifications count %d", count)
	}
}

func TestModels(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase(make(chan bool, 1))
	w.mustExec("insert into models (model_id, status) values ($1, $2)", "a", lib.StatusUnknown)
	if w.maybeModel("a") == nil {
		t.Error("unexpected result")
	}
	if w.maybeModel("b") != nil {
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
	chatID, command, args = getCommandAndArgs(tg.Update{CallbackQuery: &tg.CallbackQuery{Data: "command"}}, "@bot", nil)
	if chatID != 0 || command != "" || args != "" {
		t.Error("unexpected result")
	}
	chatID, command, args = getCommandAndArgs(tg.Update{CallbackQuery: &tg.CallbackQuery{Data: "command", From: &tg.User{ID: 1}}}, "@bot", nil)
	if chatID != 1 || command != "command" || args != "" {
		t.Error("unexpected result")
	}
}

func checkInv(w *worker, t *testing.T) {
	a := map[string]statusChange{}
	b := map[string]statusChange{}
	var recStatus statusChange
	w.mustQuery(`
		select model_id, status, timestamp
		from (
			select *, row_number() over (partition by model_id order by timestamp desc) as row
			from status_changes
		)
		where row = 1`,
		nil,
		scanTo{&recStatus.modelID, &recStatus.status, &recStatus.timestamp},
		func() { a[recStatus.modelID] = recStatus })
	w.mustQuery(
		`select model_id, status, timestamp from last_status_changes`,
		nil,
		scanTo{&recStatus.modelID, &recStatus.status, &recStatus.timestamp},
		func() { b[recStatus.modelID] = recStatus })

	if !reflect.DeepEqual(a, b) {
		t.Errorf("unexpected inv check result, statuses: %v, last statuses: %v", a, b)
		t.Log(string(debug.Stack()))
	}
	if !reflect.DeepEqual(a, w.siteStatuses) {
		t.Errorf("unexpected inv check result, statuses: %v, site statuses: %v", a, w.siteStatuses)
		t.Log(string(debug.Stack()))
	}
	confOnline := map[string]bool{}
	var rec model
	w.mustQuery(
		`select model_id, status from models`,
		nil,
		scanTo{&rec.modelID, &rec.status},
		func() {
			if rec.status == lib.StatusOnline {
				confOnline[rec.modelID] = true
			}
		})
	if !reflect.DeepEqual(w.ourOnline, confOnline) {
		t.Errorf("unexpected inv check result, left: %v, right: %v", w.ourOnline, confOnline)
		t.Log(string(debug.Stack()))
	}
}
