package main

import "database/sql"

func (s *server) mustExec(query string, args ...interface{}) {
	stmt, err := s.db.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
	checkErr(stmt.Close())
}

func (s *server) mustQuery(query string, args ...interface{}) *sql.Rows {
	result, err := s.db.Query(query, args...)
	checkErr(err)
	return result
}

func (s *server) mustInt(query string, args ...interface{}) (result int) {
	row := s.db.QueryRow(query, args...)
	checkErr(row.Scan(&result))
	return result
}
