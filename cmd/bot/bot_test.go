package main

import (
	"reflect"
	"runtime/debug"
	"testing"

	"github.com/bcmk/siren/lib"
)

func TestSql(t *testing.T) {
	linf = func(string, ...interface{}) {}
	w := newTestWorker()
	w.createDatabase()
	w.initCache()
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
	if w.mustInt("select block from block where chat_id=? and endpoint=?", 2, "ep1") != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.incrementBlock("ep2", 2)
	if w.mustInt("select block from block where chat_id=? and endpoint=?", 2, "ep2") != 1 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.resetBlock("ep1", 2)
	if w.mustInt("select block from block where chat_id=? and endpoint=?", 2, "ep1") != 0 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	if w.mustInt("select block from block where chat_id=? and endpoint=?", 2, "ep2") != 1 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.incrementBlock("ep1", 1)
	w.incrementBlock("ep1", 1)
	if w.mustInt("select block from block where chat_id=?", 1) != 2 {
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
	w.createDatabase()
	w.initCache()
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 18); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 19); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 20); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 21); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if len(w.confirmedOnlineModels) != 1 {
		t.Error("wrong online models count")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 22); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 23); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if len(w.confirmedOnlineModels) != 1 {
		t.Error("wrong online models count")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 24); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.confirmedOnlineModels["a"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 29); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.confirmedOnlineModels["a"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 31); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 32); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 33); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{}, 34)
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 35); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 36); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 37); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 41); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 42); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 48); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{}, 49)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{}, 50)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 50)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{}, 52)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{{ModelID: "b"}}, 53)
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{}, 54)
	checkInv(&w.worker, t)
	if !w.confirmedOnlineModels["b"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}, {ModelID: "b"}}, 55)
	checkInv(&w.worker, t)
	if !w.confirmedOnlineModels["b"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if len(w.confirmedOnlineModels) != 2 {
		t.Errorf("wrong online models: %v", w.confirmedOnlineModels)
	}
	checkInv(&w.worker, t)
	w.processStatusUpdates([]lib.OnlineModel{{ModelID: "a"}}, 56)
	if count := len(w.confirmedOnlineModels); count != 2 {
		t.Errorf("wrong online models count: %d", count)
	}
	w.cfg.OfflineNotifications = true
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 57); n != 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if !w.confirmedOnlineModels["a"] {
		t.Error("wrong active status")
	}
	checkInv(&w.worker, t)
	if _, n, _, _ := w.processStatusUpdates([]lib.OnlineModel{}, 68); n == 0 {
		t.Error("unexpected status update")
	}
	checkInv(&w.worker, t)
	if w.confirmedOnlineModels["a"] {
		t.Error("wrong active status")
	}
	_ = w.db.Close()
}

func checkInv(w *worker, t *testing.T) {
	lastStatusesQueryA := w.mustQuery(`
		select model_id, status, timestamp
		from (
			select *, row_number() over (partition by model_id order by timestamp desc) as row
			from status_changes
		)
		where row = 1
	`)
	defer func() { checkErr(lastStatusesQueryA.Close()) }()
	a := map[string]statusChange{}
	for lastStatusesQueryA.Next() {
		var rec statusChange
		checkErr(lastStatusesQueryA.Scan(&rec.modelID, &rec.status, &rec.timestamp))
		a[rec.modelID] = rec
	}
	lastStatusesQueryB := w.mustQuery(`select model_id, status, timestamp from last_status_changes`)
	defer func() { checkErr(lastStatusesQueryB.Close()) }()
	b := map[string]statusChange{}
	for lastStatusesQueryB.Next() {
		var rec statusChange
		checkErr(lastStatusesQueryB.Scan(&rec.modelID, &rec.status, &rec.timestamp))
		b[rec.modelID] = rec
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("unexpected inv check result, left: %v, right: %v", a, b)
		t.Log(string(debug.Stack()))
	}
	confOnlineQuery := w.mustQuery(`select model_id, status from models`)
	defer func() { checkErr(confOnlineQuery.Close()) }()
	confOnline := map[string]bool{}
	for confOnlineQuery.Next() {
		var rec model
		checkErr(confOnlineQuery.Scan(&rec.modelID, &rec.status))
		if rec.status == lib.StatusOnline {
			confOnline[rec.modelID] = true
		}
	}
	if !reflect.DeepEqual(w.confirmedOnlineModels, confOnline) {
		t.Errorf("unexpected inv check result, left: %v, right: %v", w.confirmedOnlineModels, confOnline)
		t.Log(string(debug.Stack()))
	}
}
