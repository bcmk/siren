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
	func(w *worker) {
		w.mustExec("create index ix_status_changes_model_id on status_changes(model_id);")
	},
	func(w *worker) {
		w.mustExec("alter table users add blacklist integer not null default 0;")
	},
	func(w *worker) {
		w.mustExec("alter table users add show_images integer not null default 1;")
	},
	func(w *worker) {
		w.mustExec("alter table users add offline_notifications integer not null default 1;")
	},
	func(w *worker) {
		w.mustExec(`
			create table interactions (
				priority integer not null,
				timestamp integer not null,
				endpoint text not null,
				chat_id integer not null,
				result integer not null,
				delay integer not null);`)
	},
	func(w *worker) {
		w.mustExec("alter table models add special integer not null default 0;")
	},
	func(w *worker) {
		w.mustExec("create index ix_status_changes_timestamp on status_changes(timestamp);")
	},
	func(w *worker) {
		w.mustExec("alter table signals add confirmed integer not null default 1;")
		w.mustExec("create index ix_signals_confirmed on signals(confirmed);")
	},
	func(w *worker) {
		w.mustExec("update models set status = 1 << (status - 1);")
		w.mustExec("update status_changes set status = 1 << (status - 1);")
		w.mustExec("update last_status_changes set status = 1 << (status - 1);")
	},
	func(w *worker) {
		w.mustExec(`
			create table notification_queue (
				id integer primary key autoincrement,
				endpoint text not null,
				chat_id integer not null,
				model_id text not null,
				status integer not null,
				time_diff integer,
				image_url text,
				social integer not null default 0,
				priority integer not null default 0,
				sound integer not null default 0,
				sending integer not null default 0);`)
	},
	func(w *worker) {
		w.mustExec("create index ix_interactions_endpoint on interactions(endpoint);")
		w.mustExec("create index ix_interactions_timestamp on interactions(timestamp);")
	},
	func(w *worker) {
		w.mustExec("alter table interactions add kind integer not null default 0;")
		w.mustExec("alter table notification_queue add kind integer not null default 0;")
	},
	func(w *worker) {
		w.mustExec("drop table transactions;")
		w.mustExec("drop table emails;")
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
	linf("no more migrations")
}
