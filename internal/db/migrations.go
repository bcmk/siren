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
	func(d *Database) {
		d.MustExec(`
			create index ix_status_changes_status_is_latest
			on status_changes (status)
			include (model_id, timestamp)
			where is_latest = true;`)
	},
	func(d *Database) {
		d.MustExec(`drop index ix_status_changes_status_is_latest;`)
		d.MustExec(`
			create index ix_status_changes_model_id_is_online
			on status_changes (model_id)
			include (status, timestamp)
			where status = 2 and is_latest;`)
		d.MustExec(`
			create index ix_models_model_id_is_online
			on models (model_id)
			where status = 2;`)
	},
	func(d *Database) {
		d.MustExec(`
			create index ix_models_model_id_special
			on models (model_id)
			where special;`)
	},
	func(d *Database) {
		d.MustExec(`
			create table referral_events (
				model_id text,
				referrer_chat_id bigint,
				follower_chat_id bigint,
				timestamp integer not null
			);`)
		d.MustExec(`
			insert into referral_events (model_id, timestamp)
			select m.model_id, 0
			from models m
			cross join generate_series(1, m.referred_users)
			where m.referred_users > 0;`)
		d.MustExec(`
			insert into referral_events (referrer_chat_id, timestamp)
			select r.chat_id, 0
			from referrals r
			cross join generate_series(1, r.referred_users)
			where r.referred_users > 0;`)
		d.MustExec(`create index ix_referral_events_timestamp on referral_events (timestamp);`)
	},
	func(d *Database) {
		d.MustExec(`alter table interactions rename to sent_message_log;`)
		d.MustExec(`alter index ix_interactions_timestamp rename to ix_sent_message_log_timestamp;`)
	},
	func(d *Database) {
		d.MustExec(`
			create table received_message_log (
				timestamp integer not null,
				endpoint text not null,
				chat_id bigint not null,
				command text
			);`)
		d.MustExec(`create index ix_received_message_log_timestamp on received_message_log using brin ("timestamp");`)
	},
	func(d *Database) {
		// StatusUnknown, StatusOffline, StatusOnline
		d.MustExec(`alter table models add constraint chk_models_status check (status in (0, 1, 2));`)
		d.MustExec(`alter table status_changes add constraint chk_status_changes_status check (status in (0, 1, 2));`)
	},
	func(d *Database) {
		// Create temporary indexes for efficient migration
		d.MustExec(`create index ix_status_changes_model_timestamp on status_changes (model_id, timestamp desc) include (status);`)
		d.MustExec(`create index ix_status_changes_timestamp_btree on status_changes (timestamp);`)
		d.MustExec(`vacuum analyze status_changes;`)

		// Rename status to confirmed_status in models
		d.MustExec(`alter table models rename column status to confirmed_status;`)
		d.MustExec(`alter table models rename constraint chk_models_status to chk_models_confirmed_status;`)

		// Add unconfirmed status columns to models (last two status changes)
		d.MustExec(`
			alter table models
				add column unconfirmed_status integer not null default 0,
				add column unconfirmed_timestamp integer not null default 0,
				add column prev_unconfirmed_status integer not null default 0,
				add column prev_unconfirmed_timestamp integer not null default 0,
				add constraint chk_models_unconfirmed_status check (unconfirmed_status in (0, 1, 2)),
				add constraint chk_models_prev_unconfirmed_status check (prev_unconfirmed_status in (0, 1, 2));
		`)

		// Populate from status_changes using window function
		d.MustExec(`
			update models m set
				unconfirmed_status = sub.unconfirmed_status,
				unconfirmed_timestamp = sub.unconfirmed_timestamp,
				prev_unconfirmed_status = sub.prev_unconfirmed_status,
				prev_unconfirmed_timestamp = sub.prev_unconfirmed_timestamp
			from (
				select
					model_id,
					status as unconfirmed_status,
					timestamp as unconfirmed_timestamp,
					coalesce(lead(status) over w, 0) as prev_unconfirmed_status,
					coalesce(lead(timestamp) over w, 0) as prev_unconfirmed_timestamp,
					row_number() over w as rn
				from status_changes
				window w as (partition by model_id order by timestamp desc)
			) sub
			where sub.rn = 1 and m.model_id = sub.model_id;
		`)

		// Drop temporary index and fix BRIN by clustering
		d.MustExec(`drop index ix_status_changes_model_timestamp;`)
		d.MustExec(`cluster status_changes using ix_status_changes_timestamp_btree;`)
		d.MustExec(`drop index ix_status_changes_timestamp_btree;`)
		d.MustExec(`analyze status_changes;`)
	},
	func(d *Database) {
		d.MustExec(`create index ix_signals_model_id on signals (model_id);`)
		d.MustExec(`create index ix_models_unconfirmed_online on models (model_id) where unconfirmed_status = 2;`)
	},
	func(d *Database) {
		d.MustExec(`drop index ix_status_changes_model_id_is_latest;`)
		d.MustExec(`drop index ix_status_changes_model_id_is_online;`)
		d.MustExec(`alter table status_changes drop column is_latest;`)
	},
	func(d *Database) {
		d.MustExec(`drop index ix_status_changes_model_id;`)
		d.MustExec(`create index ix_status_changes_model_id_timestamp on status_changes (model_id, timestamp) include (status);`)
		d.MustExec(`vacuum analyze status_changes;`)
	},
	func(d *Database) {
		// Treat unknown confirmed status as offline
		d.MustExec(`update models set confirmed_status = 1 where confirmed_status = 0;`)
		d.MustExec(`alter table models alter column confirmed_status set default 1;`)
		d.MustExec(`alter table models drop constraint chk_models_confirmed_status;`)
		d.MustExec(`alter table models add constraint chk_models_confirmed_status check (confirmed_status in (1, 2));`)
	},
	func(d *Database) {
		d.MustExec(`drop index ix_models_model_id_special;`)
		d.MustExec(`alter table models drop column special;`)
	},
	func(d *Database) {
		// Rename model_id to channel_id and models to channels
		d.MustExec(`alter table models rename column model_id to channel_id;`)
		d.MustExec(`alter table models rename to channels;`)
		d.MustExec(`alter table signals rename column model_id to channel_id;`)
		d.MustExec(`alter table status_changes rename column model_id to channel_id;`)
		d.MustExec(`alter table notification_queue rename column model_id to channel_id;`)
		d.MustExec(`alter table referral_events rename column model_id to channel_id;`)

		// Rename indexes
		d.MustExec(`alter index ix_signals_model_id rename to ix_signals_channel_id;`)
		d.MustExec(`alter index ix_models_unconfirmed_online rename to ix_channels_unconfirmed_online;`)
		d.MustExec(`alter index ix_models_model_id_is_online rename to ix_channels_channel_id_is_online;`)
		d.MustExec(`alter index ix_status_changes_model_id_timestamp rename to ix_status_changes_channel_id_timestamp;`)

		// Rename constraints
		d.MustExec(`alter table channels rename constraint chk_models_confirmed_status to chk_channels_confirmed_status;`)
		d.MustExec(`alter table channels rename constraint chk_models_unconfirmed_status to chk_channels_unconfirmed_status;`)
		d.MustExec(`alter table channels rename constraint chk_models_prev_unconfirmed_status to chk_channels_prev_unconfirmed_status;`)

		// Rename users.max_models to users.max_channels
		d.MustExec(`alter table users rename column max_models to max_channels;`)
	},
	// 19
	func(d *Database) {
		d.MustExec(`drop index ix_channels_channel_id_is_online`)
		d.MustExec(`
			create index ix_signals_channel_id_confirmed
			on signals (channel_id)
			where confirmed = 1;
		`)
		d.MustExec(`
			create index ix_channels_status_mismatch
			on channels (channel_id)
			include (unconfirmed_status, unconfirmed_timestamp)
			where confirmed_status != unconfirmed_status;
		`)
		d.MustExec(`alter table channels drop constraint chk_channels_confirmed_status`)
		d.MustExec(`alter table channels add constraint chk_channels_confirmed_status check (confirmed_status in (0, 1, 2))`)
		d.MustExec(`alter table channels alter column confirmed_status set default 0`)
		d.MustExec(`update channels set confirmed_status = 0 where unconfirmed_status = 0`)
	},
	func(d *Database) {
		d.MustExec(`alter table channels rename constraint models_pkey to channels_pkey`)
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
		linf("applying migration %d...", n)
		m(d)
		d.MustExec("update schema_version set version = $1", n)
	}
	linf("no more migrations")
}
