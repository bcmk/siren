package main

import (
	"database/sql"
)

type queryParams []interface{}
type record []interface{}

func (w *worker) mustExec(query string, args ...interface{}) {
	stmt, err := w.db.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
	checkErr(stmt.Close())
}

func mustExecInTx(tx *sql.Tx, query string, args ...interface{}) {
	stmt, err := tx.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
	checkErr(stmt.Close())
}

func (w *worker) mustExecPrepared(stmt *sql.Stmt, args ...interface{}) {
	_, err := stmt.Exec(args...)
	checkErr(err)
}

func (w *worker) mustInt(query string, args ...interface{}) (result int) {
	row := w.db.QueryRow(query, args...)
	checkErr(row.Scan(&result))
	return result
}

func (w *worker) mustString(query string, args ...interface{}) (result string) {
	row := w.db.QueryRow(query, args...)
	checkErr(row.Scan(&result))
	return result
}

func (w *worker) mustQuery(query string, args ...interface{}) *sql.Rows {
	result, err := w.db.Query(query, args...)
	checkErr(err)
	return result
}

func (w *worker) maybeRecord(query string, args queryParams, record record) bool {
	row := w.db.QueryRow(query, args...)
	err := row.Scan(record...)
	if err == sql.ErrNoRows {
		return false
	}
	checkErr(err)
	return true
}
