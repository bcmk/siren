package main

import (
	"database/sql"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type queryParams []interface{}
type scanTo []interface{}

var insertStatusChange = "insert into status_changes (model_id, status, timestamp) values (?,?,?)"
var updateLastStatusChange = `
	insert into last_status_changes (model_id, status, timestamp)
	values (?,?,?)
	on conflict(model_id) do update set status=excluded.status, timestamp=excluded.timestamp`
var updateModelStatus = `
	insert into models (model_id, status)
	values (?,?)
	on conflict(model_id) do update set status=excluded.status`
var storeNotification = `
	insert into notification_queue (endpoint, chat_id, model_id, status, time_diff, image_url, social, priority, sound, kind)
	values (?,?,?,?,?,?,?,?,?,?)`

func gid() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	idField := strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0]
	id, err := strconv.Atoi(idField)
	if err != nil {
		checkErr(fmt.Errorf("cannot get goroutine id: %v", err))
	}
	return id
}

func (w *worker) checkTID() {
	if !w.cfg.CheckGID {
		return
	}
	current := gid()
	if w.mainGID != current {
		checkErr(fmt.Errorf("database queries should be run from single thread, expected: %d, actual: %d", w.mainGID, current))
	}
}

func (w *worker) measure(query string) func() {
	now := time.Now()
	w.checkTID()
	return func() {
		elapsed := time.Since(now).Seconds()
		data := w.durations[query]
		data.avg = (data.avg*float64(data.count) + elapsed) / float64(data.count+1)
		data.count++
		w.durations[query] = data
	}
}

func (w *worker) mustExec(query string, args ...interface{}) {
	defer w.measure("db: " + query)()
	stmt, err := w.db.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
	checkErr(stmt.Close())
}

func (w *worker) mustExecPrepared(query string, stmt *sql.Stmt, args ...interface{}) {
	w.checkTID()
	_, err := stmt.Exec(args...)
	checkErr(err)
}

func (w *worker) mustInt(query string, args ...interface{}) (result int) {
	defer w.measure("db: " + query)()
	row := w.db.QueryRow(query, args...)
	checkErr(row.Scan(&result))
	return result
}

func (w *worker) mustString(query string, args ...interface{}) (result string) {
	defer w.measure("db: " + query)()
	row := w.db.QueryRow(query, args...)
	checkErr(row.Scan(&result))
	return result
}

func (w *worker) maybeRecord(query string, args queryParams, record scanTo) bool {
	defer w.measure("db: " + query)()
	row := w.db.QueryRow(query, args...)
	err := row.Scan(record...)
	if err == sql.ErrNoRows {
		return false
	}
	checkErr(err)
	return true
}

func (w *worker) mustStrings(queryString string, args ...interface{}) (result []string) {
	var current string
	w.mustQuery(queryString, args, scanTo{&current}, func() { result = append(result, current) })
	return
}

func (w *worker) mustQuery(queryString string, args queryParams, record scanTo, store func()) {
	defer w.measure("db: " + queryString)()
	query, err := w.db.Query(queryString, args...)
	checkErr(err)
	for query.Next() {
		checkErr(query.Scan(record...))
		store()
	}
	checkErr(query.Close())
}
