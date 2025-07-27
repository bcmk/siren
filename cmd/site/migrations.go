package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

var migrations = []func(s *server){
	func(s *server) {
		s.mustExec(`create table likes (
			address text,
			pack text,
			"like" boolean not null default false,
			primary key (address, pack));`)
	},
	func(s *server) {
		s.mustExec("alter table likes add timestamp integer not null default 0;")
	},
}

func (s *server) applyMigrations() {
	row := s.db.QueryRow(context.Background(), "select version from schema_version")
	var version int
	err := row.Scan(&version)
	if err == pgx.ErrNoRows {
		version = -1
		s.mustExec("insert into schema_version(version) values (-1)")
	} else {
		checkErr(err)
	}
	for i, m := range migrations[version+1:] {
		n := i + version + 1
		linf("applying migration %d", n)
		m(s)
		s.mustExec("update schema_version set version = $1", n)
	}
}

func (s *server) createDatabase() {
	linf("creating database if needed...")
	s.mustExec(`create table if not exists schema_version (version integer);`)
	s.applyMigrations()
}
