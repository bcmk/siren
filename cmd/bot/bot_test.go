package main

import (
	"reflect"
	"testing"

	"github.com/bcmk/siren/lib"
)

func TestSql(t *testing.T) {
	w := newTestWorker()
	w.createDatabase()
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 1, "a")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 2, "b")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 3, "c")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 3, "c2")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 3, "c3")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 4, "d")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 5, "d")
	w.mustExec("insert into users (chat_id, block) values (?,?)", 2, 0)
	w.mustExec("insert into users (chat_id, block) values (?,?)", 3, 20)
	w.mustExec("insert into users (chat_id, block) values (?,?)", 5, 20)
	w.mustExec("insert into statuses (model_id, status) values (?,?)", "a", lib.StatusOnline)
	w.mustExec("insert into statuses (model_id, status) values (?,?)", "b", lib.StatusOnline)
	w.mustExec("insert into statuses (model_id, status) values (?,?)", "c", lib.StatusOnline)
	w.mustExec("insert into statuses (model_id, status) values (?,?)", "c2", lib.StatusOnline)
	models := w.models()
	if !reflect.DeepEqual(models, []string{"a", "b", "d"}) {
		t.Error("unexpected models result", models)
	}
	broadcastChats := w.broadcastChats()
	if !reflect.DeepEqual(broadcastChats, []int64{1, 2, 4}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	chatsForModel := w.chatsForModel("a")
	if !reflect.DeepEqual(chatsForModel, []int64{1}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel = w.chatsForModel("b")
	if !reflect.DeepEqual(chatsForModel, []int64{2}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel = w.chatsForModel("c")
	if len(chatsForModel) > 0 {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel = w.chatsForModel("d")
	if !reflect.DeepEqual(chatsForModel, []int64{4}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	w.incrementBlock(2)
	w.incrementBlock(2)
	block := w.db.QueryRow("select block from users where chat_id=?", 2)
	if singleInt(block) != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.resetBlock(2)
	block = w.db.QueryRow("select block from users where chat_id=?", 2)
	if singleInt(block) != 0 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	w.incrementBlock(1)
	w.incrementBlock(1)
	block = w.db.QueryRow("select block from users where chat_id=?", 1)
	if singleInt(block) != 2 {
		t.Error("unexpected block for model result", chatsForModel)
	}
	statuses := w.statusesForChat(3)
	if !reflect.DeepEqual(statuses, []statusUpdate{
		{modelID: "c", status: lib.StatusOnline},
		{modelID: "c2", status: lib.StatusOnline}}) {
		t.Error("unexpected statuses", statuses)
	}
}

func TestUpdateStatus(t *testing.T) {
	w := newTestWorker()
	w.createDatabase()
	if w.updateStatus("a", lib.StatusOffline) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusOnline) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusNotFound) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusUnknown) {
		t.Error("unexpected status update")
	}
	w.mustExec("insert into statuses (model_id, status) values (?,?)", "a", lib.StatusOnline)
	if w.updateStatus("a", lib.StatusOffline) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusOnline) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusNotFound) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusUnknown) {
		t.Error("unexpected status update")
	}
	w.mustExec("delete from statuses")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 1, "a")
	if !w.updateStatus("a", lib.StatusOnline) {
		t.Error("unexpected status update")
	}
	if !w.updateStatus("a", lib.StatusOffline) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusOffline) {
		t.Error("unexpected status update")
	}
	if !w.updateStatus("a", lib.StatusOnline) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusOnline) {
		t.Error("unexpected status update")
	}
	if !w.updateStatus("a", lib.StatusNotFound) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusNotFound) {
		t.Error("unexpected status update")
	}
	if !w.updateStatus("a", lib.StatusOnline) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusOnline) {
		t.Error("unexpected status update")
	}
	if !w.updateStatus("a", lib.StatusUnknown) {
		t.Error("unexpected status update")
	}
	if w.updateStatus("a", lib.StatusUnknown) {
		t.Error("unexpected status update")
	}
	w.updateStatus("a", lib.StatusOffline)
	if w.updateStatus("a", lib.StatusNotFound) {
		t.Error("unexpected status update")
	}
}
