package db

import (
	"slices"
	"testing"
	"time"
)

// rewindHypertableMigrations returns status_changes to its pre-hypertable shape,
// so the prebuild can be replayed against seeded rows.
func rewindHypertableMigrations(db *testDB) {
	db.MustExec(`drop table status_changes`)
	db.MustExec(`
		create table status_changes (
			timestamp integer not null,
			streamer_id integer not null,
			status smallint not null,
			prev_status smallint not null)`)
	db.MustExec(`delete from schema_migrations where name like '%status_changes_hypertable%'`)
}

// TestPrebuiltStatusChangesHypertable replays the hypertable prebuild,
// with rows arriving before it, a delete after it, and rows arriving after it.
func TestPrebuiltStatusChangesHypertable(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	rewindHypertableMigrations(db)

	// Three rows across two closed weeks, and two at the head week that stay for the tail.
	now := int(time.Now().Unix())
	w3 := now/604800*604800 - 3*604800
	w2 := w3 + 604800
	head := now/604800*604800 + 100
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status) values
		($1, 1, 2, 0), ($2, 1, 1, 2), ($3, 2, 2, 0), ($4, 1, 2, 1)`,
		w3+100, w3+200, w2+100, head)

	// One call takes the whole prebuild group, through the copy and its vacuum.
	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	if db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected no prebuild migrations left")
	}

	if n := db.MustInt(`select count(*) from status_changes_new`); n != 3 {
		t.Fatalf("prebuild should have copied the 3 closed-week rows, got %d", n)
	}

	// A rename fold deletes a copied row; the copy keeps it as orphaned history.
	db.MustExec(`delete from status_changes where timestamp = $1`, w3+200)

	// The bot keeps writing.
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status) values ($1, 2, 1, 2)`,
		head+1)

	// Through ApplyMigrations, as startup does, so its ordering and transaction are covered.
	db.ApplyMigrations()

	// The 3 copied rows, the deleted one included, plus the 2 tail rows.
	if n := db.MustInt(`select count(*) from status_changes`); n != 5 {
		t.Errorf("expected 5 rows after the cutover, got %d", n)
	}
	if n := db.MustInt(`select count(*) from status_changes where timestamp = $1`, w3+200); n != 1 {
		t.Error("the row deleted after the copy should survive as orphaned history")
	}
	if n := db.MustInt(
		`select count(*) from status_changes where timestamp in ($1, $2)`, head, head+1); n != 2 {
		t.Error("the tail rows should have been appended at the cutover")
	}
	if n := db.MustInt(
		`select count(*) from status_changes where timestamp = $1 and status = 2 and prev_status = 0`,
		w3+100); n != 1 {
		t.Error("a copied row lost its values")
	}

	if n := db.MustInt(`
		select count(*) from timescaledb_information.hypertables
		where hypertable_name = 'status_changes'`); n != 1 {
		t.Error("status_changes should be a hypertable after the cutover")
	}
	if n := db.MustInt(`
		select count(*) from timescaledb_information.chunks
		where hypertable_name = 'status_changes'`); n != 3 {
		t.Errorf("expected the rows to span 3 week chunks, got %d", n)
	}

	if n := db.MustInt(`select count(*) from pg_class where relname = 'status_changes_conversion'`); n != 0 {
		t.Error("the cutover should have dropped status_changes_conversion")
	}
	if n := db.MustInt(`
		select count(*) from pg_indexes
		where indexname = 'ix_status_changes_streamer_id_timestamp'`); n != 1 {
		t.Error("covering index missing after the cutover")
	}
	if n := db.MustInt(`
		select count(*) from pg_indexes
		where indexname = 'ix_status_changes_timestamp' and indexdef like '%USING btree%'`); n != 1 {
		t.Error("timestamp btree missing after the cutover")
	}
}

// A row timestamped below the boundary after its week was copied would vanish in the swap,
// so the cutover refuses it.
func TestHypertableCutoverRefusesRowBelowBoundary(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	rewindHypertableMigrations(db)

	now := int(time.Now().Unix())
	w3 := now/604800*604800 - 3*604800
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status) values ($1, 1, 2, 0)`,
		w3+100)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}

	// A clock stepping back past the margin lands a row below an already copied week.
	db.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status) values ($1, 1, 1, 2)`,
		w3+200)

	defer func() {
		if recover() == nil {
			t.Error("the cutover should have refused the row below the boundary")
		}
	}()
	db.ApplyMigrations()
}

// TestPrebuiltPerformanceLogHypertable replays the performance_log prebuild and cutover,
// with rows written after the prebuild at the boundary and above it.
func TestPrebuiltPerformanceLogHypertable(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Back to the 0027 shape
	db.MustExec(`drop table performance_log`)
	db.MustExec(`
		create table performance_log (
			timestamp integer not null,
			kind integer not null,
			duration_ms integer not null,
			data jsonb not null)`)
	db.MustExec(`
		create index ix_performance_log_timestamp on performance_log
		using brin ("timestamp") with (pages_per_range = 8)`)
	db.MustExec(`delete from schema_migrations where name like '%performance_log_hypertable%'`)

	// Three rows below the boundary, an hour behind now, and one above it
	now := int(time.Now().Unix())
	w2 := now/604800*604800 - 2*604800
	db.MustExec(`
		insert into performance_log (timestamp, kind, duration_ms, data) values
		($1, 1, 10, '{"a": 1}'), ($2, 1, 20, '{}'), ($3, 2, 30, '{}'), ($4, 1, 40, '{}')`,
		w2+100, w2+604800+100, now-7200, now)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	if n := db.MustInt(`select count(*) from performance_log_new`); n != 3 {
		t.Fatalf("prebuild should have copied the 3 rows below the boundary, got %d", n)
	}

	// The bot keeps writing, at the boundary and above it
	boundary := db.MustInt(`select boundary from performance_log_conversion`)
	db.MustExec(`
		insert into performance_log (timestamp, kind, duration_ms, data) values
		($1, 1, 50, '{}'), ($2, 1, 60, '{}')`,
		boundary, boundary+10)

	db.ApplyMigrations()

	if n := db.MustInt(`select count(*) from performance_log`); n != 6 {
		t.Errorf("expected 6 rows after the cutover, got %d", n)
	}
	if n := db.MustInt(`select count(*) from performance_log where timestamp = $1`, boundary); n != 1 {
		t.Error("the row at the boundary should have been appended at the cutover")
	}
	if n := db.MustInt(`
		select count(*) from performance_log
		where timestamp = $1 and kind = 1 and duration_ms = 10 and data = '{"a": 1}'`,
		w2+100); n != 1 {
		t.Error("a copied row lost its values")
	}

	if n := db.MustInt(`
		select count(*) from timescaledb_information.hypertables
		where hypertable_name = 'performance_log'`); n != 1 {
		t.Error("performance_log should be a hypertable after the cutover")
	}
	if n := db.MustInt(`
		select count(*) from pg_indexes
		where tablename = 'performance_log'
		and indexname = 'ix_performance_log_timestamp' and indexdef like '%USING btree%'`); n != 1 {
		t.Error("timestamp btree missing after the cutover")
	}
	if n := db.MustInt(`
		select count(*) from pg_constraint
		where conrelid = 'performance_log'::regclass and contype = 'n' and conname in (
			'performance_log_timestamp_not_null',
			'performance_log_kind_not_null',
			'performance_log_duration_ms_not_null',
			'performance_log_data_not_null')`); n != 4 {
		t.Errorf("expected the 4 not-null constraints under their final names, got %d", n)
	}
	if n := db.MustInt(`
		select count(*) from pg_class
		where relname in ('performance_log_conversion', 'performance_log_new')`); n != 0 {
		t.Error("the cutover should have dropped the conversion table and the copy")
	}
}

// TestPrebuiltMessageLogsHypertable replays the message logs' prebuild and cutover,
// with rows written after the prebuild at the boundary and above it.
func TestPrebuiltMessageLogsHypertable(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer db.terminate()

	// Back to the shape before 0089
	db.MustExec(`drop table sent_message_log`)
	db.MustExec(`drop table received_message_log`)
	db.MustExec(`
		create table sent_message_log (
			priority integer not null,
			timestamp integer not null,
			endpoint text not null,
			user_id bigint not null,
			result integer not null,
			latency integer not null,
			kind integer not null default 0,
			command text,
			reply_seq integer not null default 0,
			constraint chk_sent_message_log_priority check (priority in (0, 1)),
			constraint fk_sent_message_log_user_id foreign key (user_id) references users(id) on delete restrict)`)
	db.MustExec(`
		create table received_message_log (
			timestamp integer not null,
			endpoint text not null,
			user_id bigint not null,
			command text,
			constraint fk_received_message_log_user_id foreign key (user_id) references users(id) on delete restrict)`)
	db.MustExec(`
		create index ix_sent_message_log_timestamp on sent_message_log
		using brin ("timestamp") with (pages_per_range = 8)`)
	db.MustExec(`create index ix_sent_message_log_user_id_timestamp on sent_message_log (user_id, timestamp)`)
	db.MustExec(`
		create index ix_received_message_log_timestamp on received_message_log
		using brin ("timestamp") with (pages_per_range = 8)`)
	db.MustExec(`create index ix_received_message_log_user_id_timestamp on received_message_log (user_id, timestamp)`)
	db.MustExec(`delete from schema_migrations where name like '%message_logs_hypertable%'`)
	before := map[string][]string{}
	for _, table := range []string{"sent_message_log", "received_message_log"} {
		before[table] = logShape(db, table)
	}

	db.MustExec(`insert into users (id, chat_id) values (1, 1), (2, 2)`)

	// Three rows of each log below the boundary, an hour behind now, and one above it
	now := int(time.Now().Unix())
	w2 := now/604800*604800 - 2*604800
	db.MustExec(`
		insert into sent_message_log (priority, timestamp, endpoint, user_id, result, latency, kind, command, reply_seq)
		values
		(0, $1, 'en', 1, 200, 15, 1, 'list', 3), (1, $2, 'ru', 2, 403, 20, 0, null, 0),
		(0, $3, 'en', 1, 200, 25, 0, null, 0), (0, $4, 'en', 2, 200, 30, 0, null, 0)`,
		w2+100, w2+604800+100, now-7200, now)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id, command) values
		($1, 'en', 1, 'start'), ($2, 'ru', 2, null), ($3, 'en', 1, 'list'), ($4, 'en', 2, 'help')`,
		w2+100, w2+604800+100, now-7200, now)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	if n := db.MustInt(`select count(*) from sent_message_log_new`); n != 3 {
		t.Fatalf("prebuild should have copied the 3 sent messages below the boundary, got %d", n)
	}
	if n := db.MustInt(`select count(*) from received_message_log_new`); n != 3 {
		t.Fatalf("prebuild should have copied the 3 received messages below the boundary, got %d", n)
	}

	// The bot keeps writing, at the boundary and above it
	boundary := db.MustInt(`select boundary from message_logs_conversion`)
	db.MustExec(`
		insert into sent_message_log (priority, timestamp, endpoint, user_id, result, latency, kind, command, reply_seq)
		values (0, $1, 'en', 1, 200, 35, 0, null, 0), (0, $2, 'en', 2, 200, 40, 0, 'add', 1)`,
		boundary, boundary+10)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id, command) values
		($1, 'en', 1, null), ($2, 'ru', 2, 'add')`,
		boundary, boundary+10)

	db.ApplyMigrations()

	if n := db.MustInt(`select count(*) from sent_message_log`); n != 6 {
		t.Errorf("expected 6 sent messages after the cutover, got %d", n)
	}
	if n := db.MustInt(`select count(*) from received_message_log`); n != 6 {
		t.Errorf("expected 6 received messages after the cutover, got %d", n)
	}
	if n := db.MustInt(`select count(*) from sent_message_log where timestamp = $1`, boundary); n != 1 {
		t.Error("the sent message at the boundary should have been appended at the cutover")
	}
	if n := db.MustInt(`select count(*) from received_message_log where timestamp = $1`, boundary); n != 1 {
		t.Error("the received message at the boundary should have been appended at the cutover")
	}
	for _, table := range []string{"sent_message_log", "received_message_log"} {
		if n := db.MustInt(`
			select count(*) from timescaledb_information.hypertables
			where hypertable_name = $1`, table); n != 1 {
			t.Errorf("%s should be a hypertable after the cutover", table)
		}
		for _, index := range []string{"ix_" + table + "_timestamp", "ix_" + table + "_user_id_timestamp"} {
			if n := db.MustInt(`
				select count(*) from pg_indexes
				where tablename = $1 and indexname = $2 and indexdef like '%USING btree%'`,
				table, index); n != 1 {
				t.Errorf("btree %s missing after the cutover", index)
			}
		}
		if after := logShape(db, table); !slices.Equal(after, before[table]) {
			t.Errorf("%s's constraints and defaults changed:\nbefore %q\nafter  %q", table, before[table], after)
		}
	}
	if n := db.MustInt(`
		select count(*) from sent_message_log
		where timestamp = $1 and priority = 0 and endpoint = 'en' and user_id = 1 and result = 200
		and latency = 15 and kind = 1 and command = 'list' and reply_seq = 3`,
		w2+100); n != 1 {
		t.Error("a copied sent message lost its values")
	}
	if n := db.MustInt(`
		select count(*) from pg_class
		where relname in ('message_logs_conversion', 'sent_message_log_new', 'received_message_log_new')`); n != 0 {
		t.Error("the cutover should have dropped the conversion table and the copies")
	}
}

// logShape lists a table's constraints, by name and definition, and its column defaults,
// so the swapped-in copy can be compared with the table it replaced
func logShape(db *testDB, table string) []string {
	var shape []string
	var item string
	db.MustQuery(`
		select conname || ' ' || pg_get_constraintdef(oid)
		from pg_constraint
		where conrelid = $1::regclass
		union all
		select a.attname || ' default ' || pg_get_expr(d.adbin, d.adrelid)
		from pg_attrdef d
		join pg_attribute a on a.attrelid = d.adrelid and a.attnum = d.adnum
		where d.adrelid = $1::regclass
		order by 1`,
		QueryParams{table},
		ScanTo{&item},
		func() { shape = append(shape, item) })
	return shape
}
