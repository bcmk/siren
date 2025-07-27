package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s *server) mustExec(query string, args ...interface{}) {
	_, err := s.db.Exec(context.Background(), query, args...)
	checkErr(err)
}

func (s *server) mustQuery(query string, args ...interface{}) pgx.Rows {
	result, err := s.db.Query(context.Background(), query, args...)
	checkErr(err)
	return result
}

func (s *server) mustInt(query string, args ...interface{}) (result int) {
	row := s.db.QueryRow(context.Background(), query, args...)
	checkErr(row.Scan(&result))
	return result
}
