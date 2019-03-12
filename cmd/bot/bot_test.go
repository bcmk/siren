package main

import (
	"reflect"
	"sort"
	"testing"
)

func TestSql(t *testing.T) {
	w := newTestWorker()
	w.createDatabase()
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 1, "a")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 2, "b")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 3, "c")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 4, "d")
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", 5, "d")
	w.mustExec("insert into users (chat_id, block) values (?,?)", 2, 0)
	w.mustExec("insert into users (chat_id, block) values (?,?)", 3, 20)
	w.mustExec("insert into users (chat_id, block) values (?,?)", 5, 20)
	models := w.models()
	sort.Strings(models)
	if !reflect.DeepEqual(models, []string{"a", "b", "d"}) {
		t.Error("unexpected models result", models)
	}
	broadcastChats := w.broadcastChats()
	sort.Slice(broadcastChats, func(i, j int) bool { return broadcastChats[i] < broadcastChats[j] })
	if !reflect.DeepEqual(broadcastChats, []int64{1, 2, 4}) {
		t.Error("unexpected broadcast chats result", broadcastChats)
	}
	chatsForModel := w.chatsForModel("a")
	sort.Slice(chatsForModel, func(i, j int) bool { return chatsForModel[i] < chatsForModel[j] })
	if !reflect.DeepEqual(chatsForModel, []int64{1}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel = w.chatsForModel("b")
	sort.Slice(chatsForModel, func(i, j int) bool { return chatsForModel[i] < chatsForModel[j] })
	if !reflect.DeepEqual(chatsForModel, []int64{2}) {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel = w.chatsForModel("c")
	sort.Slice(chatsForModel, func(i, j int) bool { return chatsForModel[i] < chatsForModel[j] })
	if len(chatsForModel) > 0 {
		t.Error("unexpected chats for model result", chatsForModel)
	}
	chatsForModel = w.chatsForModel("d")
	sort.Slice(chatsForModel, func(i, j int) bool { return chatsForModel[i] < chatsForModel[j] })
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
}
