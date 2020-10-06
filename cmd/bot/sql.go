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
		data := w.sqlQueryDurations[query]
		data.avg = (data.avg*float64(data.count) + elapsed) / float64(data.count+1)
		data.count++
		w.sqlQueryDurations[query] = data
	}
}

func (w *worker) mustExec(query string, args ...interface{}) {
	defer w.measure(query)()
	stmt, err := w.db.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
	checkErr(stmt.Close())
}

func (w *worker) mustExecPrepared(query string, stmt *sql.Stmt, args ...interface{}) {
	defer w.measure(query)()
	_, err := stmt.Exec(args...)
	checkErr(err)
}

func (w *worker) mustInt(query string, args ...interface{}) (result int) {
	defer w.measure(query)()
	row := w.db.QueryRow(query, args...)
	checkErr(row.Scan(&result))
	return result
}

func (w *worker) mustString(query string, args ...interface{}) (result string) {
	defer w.measure(query)()
	row := w.db.QueryRow(query, args...)
	checkErr(row.Scan(&result))
	return result
}

func (w *worker) mustQuery(query string, args ...interface{}) *sql.Rows {
	defer w.measure(query)()
	result, err := w.db.Query(query, args...)
	checkErr(err)
	return result
}

func (w *worker) maybeRecord(query string, args queryParams, record record) bool {
	defer w.measure(query)()
	row := w.db.QueryRow(query, args...)
	err := row.Scan(record...)
	if err == sql.ErrNoRows {
		return false
	}
	checkErr(err)
	return true
}
