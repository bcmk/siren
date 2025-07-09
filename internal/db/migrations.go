package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

var migrations = []func(d *Database){
	func(d *Database) {
		d.MustExec(`
			create table block (
				chat_id bigint not null,
				endpoint text not null,
				block integer not null,
				primary key (chat_id, endpoint)
			);
		`)
		d.MustExec(`
			create table feedback (
				chat_id bigint,
				text text,
				endpoint text not null default ''
			);
		`)
		d.MustExec(`
			create table interactions (
				priority integer not null,
				timestamp integer not null,
				endpoint text not null,
				chat_id bigint not null,
				result integer not null,
				delay integer not null,
				kind integer not null default 0
			);
		`)
		d.MustExec(`create index ix_interactions_endpoint on interactions (endpoint);`)
		d.MustExec(`create index ix_interactions_timestamp on interactions ("timestamp");`)
		d.MustExec(`
			create table models (
				model_id text primary key,
				status integer not null default 0,
				referred_users integer not null default 0,
				special boolean not null default false
			);
		`)
		d.MustExec(`
			create table notification_queue (
				id serial primary key,
				endpoint text not null,
				chat_id bigint not null,
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
		d.MustExec(`
			create table referrals (
				chat_id bigint primary key,
				referral_id text not null default '',
				referred_users integer not null default 0
			);
		`)
		d.MustExec(`
			create table signals (
				chat_id bigint not null,
				model_id text not null,
				endpoint text not null default '',
				confirmed integer not null default 1,
				primary key (chat_id, model_id, endpoint)
			);
		`)
		d.MustExec(`create index ix_signals_confirmed on signals (confirmed);`)
		d.MustExec(`
			create table status_changes (
				model_id text,
				status integer not null default 0,
				timestamp integer not null default 0,
				is_latest boolean not null default false
			);`)
		d.MustExec(`create index ix_status_changes_model_id on status_changes (model_id);`)
		d.MustExec(`create index ix_status_changes_timestamp on status_changes ("timestamp");`)
		d.MustExec(`
			create unique index ix_status_changes_model_id_is_latest
			on status_changes (model_id)
			where is_latest = true;`)
		d.MustExec(`
			create table users (
				chat_id bigint primary key,
				max_models integer not null default 0,
				reports integer not null default 0,
				blacklist boolean not null default false,
				show_images boolean not null default true,
				offline_notifications boolean not null default true
			);
		`)
	},
	func(d *Database) {
		d.MustExec(`alter table feedback add column timestamp integer;`)
		d.MustExec(`update feedback set timestamp = extract(epoch from now())::integer;`)
		d.MustExec(`alter table feedback alter column timestamp set not null;`)
	},
	func(d *Database) {
		d.MustExec(`drop index ix_status_changes_model_id_is_latest;`)
		d.MustExec(`
			create unique index ix_status_changes_model_id_is_latest
			on status_changes (model_id)
			include (status, timestamp)
			where is_latest = true;`)
	},
	func(d *Database) {
		d.MustExec(`drop index ix_interactions_endpoint;`)
		d.MustExec(`drop index ix_interactions_timestamp;`)
		d.MustExec(`drop index ix_status_changes_timestamp;`)
		d.MustExec(`create index ix_interactions_timestamp on interactions using brin ("timestamp");`)
		d.MustExec(`create index ix_status_changes_timestamp on status_changes using brin ("timestamp");`)
	},
	func(d *Database) {
		d.MustExec(`alter index ix_status_changes_timestamp set (pages_per_range = 8);`)
		d.MustExec(`reindex index ix_status_changes_timestamp;`)
	},
}

// ApplyMigrations applies all migrations to the database
func (d *Database) ApplyMigrations() {
	row := d.db.QueryRow(context.Background(), "select version from schema_version")
	var version int
	err := row.Scan(&version)
	if err == pgx.ErrNoRows {
		version = -1
		d.MustExec("insert into schema_version(version) values (0)")
	} else {
		checkErr(err)
	}
	for i, m := range migrations[version+1:] {
		n := i + version + 1
		linf("applying migration %d", n)
		m(d)
		d.MustExec("update schema_version set version = $1", n)
	}
	linf("no more migrations")
}
