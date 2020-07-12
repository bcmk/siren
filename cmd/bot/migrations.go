package main

import (
	"database/sql"
)

var migrations = []func(w *worker){
	func(w *worker) {
		w.mustExec(`
			create table if not exists signals (
				chat_id integer,
				model_id text,
				endpoint text not null default '',
				primary key (chat_id, model_id, endpoint));`)
		w.mustExec(`
			create table if not exists status_changes (
				model_id text,
				status integer not null default 0,
				timestamp integer not null default 0);`)
		w.mustExec(`
			create table if not exists last_status_changes (
				model_id text primary key,
				status integer not null default 0,
				timestamp integer not null default 0);`)
		w.mustExec(`
			create table if not exists models (
				model_id text primary key,
				status integer not null default 0,
				referred_users integer not null default 0);`)
		w.mustExec(`
			create table if not exists feedback (
				chat_id integer,
				text text,
				endpoint text not null default '');`)
		w.mustExec(`
			create table if not exists block (
				chat_id integer,
				block integer not null default 0,
				endpoint text not null default '',
				primary key(chat_id, endpoint));`)
		w.mustExec(`
			create table if not exists users (
				chat_id integer primary key,
				max_models integer not null default 0,
				reports integer not null default 0);`)
		w.mustExec(`
			create table if not exists emails (
				chat_id integer,
				endpoint text not null default '',
				email text not null default '',
				primary key(chat_id, endpoint));`)
		w.mustExec(`
			create table if not exists transactions (
				local_id text primary key,
				kind text,
				chat_id integer,
				remote_id text,
				timeout integer,
				amount text,
				address string,
				status_url string,
				checkout_url string,
				dest_tag string,
				status integer,
				timestamp integer,
				model_number integer,
				currency text,
				endpoint text not null default '');`)
		w.mustExec(`
			create table if not exists referrals (
				chat_id integer primary key,
				referral_id text not null default '',
				referred_users integer not null default 0);`)
	},
}

func (w *worker) applyMigrations() {
	row := w.db.QueryRow("select version from schema_version")
	var version int
	err := row.Scan(&version)
	if err == sql.ErrNoRows {
		version = -1
		w.mustExec("insert into schema_version(version) values (0)")
	} else {
		checkErr(err)
	}
	for i, m := range migrations[version+1:] {
		n := i + version + 1
		linf("applying migration %d", n)
		m(w)
		w.mustExec("update schema_version set version=?", n)
	}
}
