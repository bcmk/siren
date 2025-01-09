package main

import (
	"database/sql"
)

var migrations = []func(w *worker){
	func(w *worker) {
		w.mustExec(`
			create table block (
				chat_id integer not null,
				endpoint text not null,
				block integer not null,
				primary key (chat_id, endpoint)
			);
		`)
		w.mustExec(`
			create table feedback (
				chat_id integer,
				text text,
				endpoint text not null default ''
			);
		`)
		w.mustExec(`
			create table interactions (
				priority integer not null,
				timestamp integer not null,
				endpoint text not null,
				chat_id integer not null,
				result integer not null,
				delay integer not null,
				kind integer not null default 0
			);
		`)
		w.mustExec(`create index ix_interactions_endpoint on interactions (endpoint);`)
		w.mustExec(`create index ix_interactions_timestamp on interactions ("timestamp");`)
		w.mustExec(`
			create table last_status_changes (
				model_id text primary key,
				status integer not null default 0,
				timestamp integer not null default 0
			);
		`)
		w.mustExec(`
			create table models (
				model_id text primary key,
				status integer not null default 0,
				referred_users integer not null default 0,
				special integer not null default 0
			);
		`)
		w.mustExec(`
			create table notification_queue (
				id serial primary key,
				endpoint text not null,
				chat_id integer not null,
				model_id text not null,
				status integer not null,
				time_diff integer,
				image_url text,
				social boolean not null default false,
				priority integer not null default 0,
				sound boolean not null default false,
				sending integer not null default 0,
				kind integer not null default 0
			);
		`)
		w.mustExec(`
			create table referrals (
				chat_id integer primary key,
				referral_id text not null default '',
				referred_users integer not null default 0
			);
		`)
		w.mustExec(`
			create table signals (
				chat_id integer not null,
				model_id text not null,
				endpoint text not null default '',
				confirmed integer not null default 1,
				primary key (chat_id, model_id, endpoint)
			);
		`)
		w.mustExec(`create index ix_signals_confirmed on signals (confirmed);`)
		w.mustExec(`
			create table status_changes (
				model_id text,
				status integer not null default 0,
				timestamp integer not null default 0
			);`)
		w.mustExec(`create index ix_status_changes_model_id on status_changes (model_id);`)
		w.mustExec(`create index ix_status_changes_timestamp on status_changes ("timestamp");`)
		w.mustExec(`
			create table users (
				chat_id integer primary key,
				max_models integer not null default 0,
				reports integer not null default 0,
				blacklist integer not null default 0,
				show_images integer not null default 1,
				offline_notifications integer not null default 1
			);
		`)
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
		w.mustExec("update schema_version set version = $1", n)
	}
	linf("no more migrations")
}
