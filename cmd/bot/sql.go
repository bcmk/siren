package main

import (
	"database/sql"
	"time"
)

type queryParams []interface{}
type record []interface{}

var insertStatusChange = "insert into status_changes (model_id, status, timestamp) values (?,?,?)"
var updateLastStatusChange = `
	insert into last_status_changes (model_id, status, timestamp)
	values (?,?,?)
	on conflict(model_id) do update set status=excluded.status, timestamp=excluded.timestamp`
var updateModelStatus = `
	insert into models (model_id, status)
	values (?,?)
	on conflict(model_id) do update set status=excluded.status`

func (w *worker) measure(query string) func() {
	now := time.Now()
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

func (w *worker) mustQueryInternal(query string, args ...interface{}) *sql.Rows {
	result, err := w.db.Query(query, args...)
	checkErr(err)
	return result
}

func (w *worker) maybeRecord(query string, args queryParams, record record) bool {
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
	w.mustQuery(queryString, queryParams(args), record{&current}, func() { result = append(result, current) })
	return
}

func (w *worker) mustQuery(queryString string, args queryParams, record record, store func()) {
	defer w.measure("db: " + queryString)()
	query := w.mustQueryInternal(queryString, args...)
	defer func() { checkErr(query.Close()) }()
	for query.Next() {
		checkErr(query.Scan(record...))
		store()
	}
}
