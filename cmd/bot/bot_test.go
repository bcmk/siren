package main

import (
	"reflect"
	"testing"

	"github.com/bcmk/siren/lib"
)

func TestSql(t *testing.T) {
	linf = func(string, ...interface{}) {}
	w := newTestWorker()
	w.createDatabase()
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 1, "a")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 2, "b")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 3, "c")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 3, "c2")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 3, "c3")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 4, "d")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 5, "d")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 6, "e")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep1", 7, "f")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep2", 6, "e")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep2", 7, "f")
	w.mustExec("insert into signals (endpoint, chat_id, model_id) values (?,?,?)", "ep2", 8, "g")
	w.mustExec("insert into block (endpoint, chat_id, block) values (?,?,?)", "ep1", 2, 0)
	w.mustExec("insert into block (endpoint, chat_id, block) values (?,?,?)", "ep1", 3, w.cfg.BlockThreshold)
	w.mustExec("insert into block (endpoint, chat_id, block) values (?,?,?)", "ep1", 4, w.cfg.BlockThreshold-1)
	w.mustExec("insert into block (endpoint, chat_id, block) values (?,?,?)", "ep1", 5, w.cfg.BlockThreshold+1)
	w.mustExec("insert into block (endpoint, chat_id, block) values (?,?,?)", "ep1", 6, w.cfg.BlockThreshold)
	w.mustExec("insert into block (endpoint, chat_id, block) values (?,?,?)", "ep1", 7, w.cfg.BlockThreshold)
	w.mustExec("insert into block (endpoint, chat_id, block) values (?,?,?)", "ep2", 7, w.cfg.BlockThreshold)
	w.mustExec("insert into models (model_id, status) values (?,?)", "a", lib.StatusOnline)
	w.mustExec("insert into models (model_id, status) values (?,?)", "b", lib.StatusOnline)
	w.mustExec("insert into models (model_id, status) values (?,?)", "c", lib.StatusOnline)
	w.mustExec("insert into models (model_id, status) values (?,?)", "c2", lib.StatusOnline)
	models := w.knownModels()
	if !reflect.DeepEqual(models, []string{"a", "b", "c", "c2"}) {
		t.Error("unexpected models result", models)
	}
	models = w.modelsToPoll()
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
	block := w.db.QueryRow("select block from block where chat_id=? and endpoint=?", 2, "ep1")
	if singleInt(block) != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.incrementBlock("ep2", 2)
	block = w.db.QueryRow("select block from block where chat_id=? and endpoint=?", 2, "ep2")
	if singleInt(block) != 1 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.resetBlock("ep1", 2)
	block = w.db.QueryRow("select block from block where chat_id=? and endpoint=?", 2, "ep1")
	if singleInt(block) != 0 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	block = w.db.QueryRow("select block from block where chat_id=? and endpoint=?", 2, "ep2")
	if singleInt(block) != 1 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.incrementBlock("ep1", 1)
	w.incrementBlock("ep1", 1)
	block = w.db.QueryRow("select block from block where chat_id=?", 1)
	if singleInt(block) != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	statuses := w.statusesForChat("ep1", 3)
	if !reflect.DeepEqual(statuses, []lib.StatusUpdate{
		{ModelID: "c", Status: lib.StatusOnline},
		{ModelID: "c2", Status: lib.StatusOnline}}) {
		t.Error("unexpected statuses", statuses)
	}
}

func TestUpdateStatus(t *testing.T) {
	w := newTestWorker()
	w.createDatabase()
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 18); n == 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 19); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 20); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 21); n != 0 {
		t.Error("unexpected status update")
	}
	if w.onlineModelsCount() != 1 {
		t.Error("wrong online models count")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 22); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 23); n != 0 {
		t.Error("unexpected status update")
	}
	if w.onlineModelsCount() != 1 {
		t.Error("wrong online models count")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 24); n != 0 {
		t.Error("unexpected status update")
	}
	if w.confirmedStatuses["a"] != lib.StatusOnline {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 29); n == 0 {
		t.Error("unexpected status update")
	}
	if w.confirmedStatuses["a"] != lib.StatusNotFound {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 30); n == 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 31); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusUnknown}}, 32); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusUnknown}}, 33); n != 0 {
		t.Error("unexpected status update")
	}
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 34)
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 35); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 37); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 37); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 41); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 42); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 48); n != 0 {
		t.Error("unexpected status update")
	}
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 49)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 50)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 50)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 52)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "b", Status: lib.StatusOnline}}, 53)
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "b", Status: lib.StatusOffline}}, 54)
	if w.confirmedStatuses["b"] != lib.StatusOnline {
		t.Error("wrong active status")
	}
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "b", Status: lib.StatusOnline}}, 55)
	if w.confirmedStatuses["b"] != lib.StatusOnline {
		t.Error("wrong active status")
	}
	if w.onlineModelsCount() != 2 {
		t.Error("wrong online models count")
	}
	w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 56)
	if count := w.onlineModelsCount(); count != 2 {
		t.Errorf("wrong online models count: %d", count)
	}
	w.cfg.OfflineNotifications = true
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 57); n != 0 {
		t.Error("unexpected status update")
	}
	if w.confirmedStatuses["a"] != lib.StatusOnline {
		t.Error("wrong active status")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOffline}}, 68); n == 0 {
		t.Error("unexpected status update")
	}
	if w.confirmedStatuses["a"] != lib.StatusOffline {
		t.Error("wrong active status")
	}
}

func TestUpdateStatusFromNotFoundToOnline(t *testing.T) {
	w := newTestWorker()
	w.createDatabase()
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 18); n != 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusNotFound}}, 24); n == 0 {
		t.Error("unexpected status update")
	}
	if _, n, _, _ := w.processStatusUpdates([]lib.StatusUpdate{{ModelID: "a", Status: lib.StatusOnline}}, 30); n == 0 {
		t.Error("unexpected status update")
	}
}
