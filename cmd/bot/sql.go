package main

import "database/sql"

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
