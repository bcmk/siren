//go:build migrations

package db

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// rewindChatsMigrations returns the schema to its shape before chats,
// so the prebuild and the cutover can be replayed against seeded rows.
func rewindChatsMigrations(db *testDB) {
	db.MustExec(`
		drop table sent_message_log;

		drop table received_message_log;

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
			image_mode integer not null default 0,
			constraint chk_sent_message_log_priority check (priority in (0, 1)),
			constraint fk_sent_message_log_user_id foreign key (user_id) references users(id)
		) with (
			tsdb.hypertable,
			tsdb.partition_column = 'timestamp',
			tsdb.chunk_interval = 604800,
			tsdb.create_default_indexes = false,
			tsdb.columnstore = false
		);

		create index ix_sent_message_log_timestamp on sent_message_log (timestamp);

		create index ix_sent_message_log_user_id_timestamp on sent_message_log (user_id, timestamp);

		create table received_message_log (
			timestamp integer not null,
			endpoint text not null,
			user_id bigint not null,
			command text,
			constraint fk_received_message_log_user_id foreign key (user_id) references users(id)
		) with (
			tsdb.hypertable,
			tsdb.partition_column = 'timestamp',
			tsdb.chunk_interval = 604800,
			tsdb.create_default_indexes = false,
			tsdb.columnstore = false
		);

		create index ix_received_message_log_timestamp on received_message_log (timestamp);

		create index ix_received_message_log_user_id_timestamp on received_message_log (user_id, timestamp);

		alter table subscriptions
		drop column chat_id,
		add column user_id bigint not null,
		add column endpoint text not null default '',
		add constraint subscriptions_pkey primary key (user_id, streamer_id, endpoint),
		add constraint fk_subscriptions_user_id foreign key (user_id) references users(id);

		alter table pending_subscriptions
		drop column chat_id,
		add column user_id bigint not null,
		add column endpoint text not null,
		add constraint pending_subscriptions_pkey primary key (user_id, nickname, endpoint),
		add constraint fk_pending_subscriptions_user_id foreign key (user_id) references users(id);

		alter table notification_queue
		drop column chat_id,
		add column user_id bigint not null,
		add column endpoint text not null,
		add constraint fk_notification_queue_user_id foreign key (user_id) references users(id);

		alter table feedback
		drop column chat_id,
		add column user_id bigint,
		add column endpoint text not null default '',
		add constraint fk_feedback_user_id foreign key (user_id) references users(id);

		alter table star_payments
		drop column chat_id,
		add column user_id bigint not null,
		add column endpoint text not null,
		add constraint fk_star_payments_user_id foreign key (user_id) references users(id);

		drop table chats;

		alter table users rename column tg_chat_id to chat_id;

		alter index ix_users_tg_chat_id rename to ix_users_chat_id;

		alter table users
		add column max_subs integer not null default 0,
		add column reports integer not null default 0,
		add column show_images boolean not null default true,
		add column offline_notifications boolean not null default true,
		add column show_subject boolean not null default true,
		add column silent_messages boolean not null default false,
		add column member_subscriptions boolean not null default false,
		add column affiliate_params jsonb;

		create table block (
			user_id bigint not null references users(id),
			endpoint text not null,
			block integer not null,
			primary key (user_id, endpoint));

		create table referrals (
			user_id bigint primary key references users(id),
			referral_id text not null default '',
			referred_users integer not null default 0);

		delete from schema_migrations where name like 'chats\_%'`)
}

// TestPrebuiltChats replays the chats prebuild and cutover,
// with chats that only the logs name, chats written after the prebuild,
// chats in the small tables, rows on the copy's edges, and a user merged into another.
func TestPrebuiltChats(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	rewindChatsMigrations(db)

	now := int(time.Now().Unix())
	user := func(chatID int64) int64 {
		return int64(db.MustInt(`insert into users (chat_id, max_subs) values ($1, 3) returning id`, chatID))
	}
	logged, late, subscribed, blocked, unseen := user(1), user(2), user(3), user(4), user(5)
	pending, queued, feedbacker, payer := user(6), user(7), user(8), user(9)
	merged, into := user(-10), user(-1000)
	stepEdge, acrossEdge, tailEdge := user(11), user(12), user(13)
	db.MustExec(`update users set max_subs = 8 where id = $1`, unseen)
	// One user per synced column, with a prebuilt chat, so a column the sync leaves out shows
	syncs := []struct {
		column string
		update string
		want   any
	}{
		{"max_subs", `update users set max_subs = 5 where id = $1`, float64(5)},
		{"reports", `update users set reports = 1 where id = $1`, float64(1)},
		{"show_images", `update users set show_images = false where id = $1`, false},
		{"offline_notifications", `update users set offline_notifications = false where id = $1`, false},
		{"show_subject", `update users set show_subject = false where id = $1`, false},
		{"silent_messages", `update users set silent_messages = true where id = $1`, true},
		{"member_subscriptions", `update users set member_subscriptions = true where id = $1`, true},
		{"affiliate_params", `update users set affiliate_params = '{"e": "f"}' where id = $1`, map[string]any{"e": "f"}},
	}
	syncUsers := make([]int64, len(syncs))
	for i := range syncs {
		syncUsers[i] = user(int64(20 + i))
		db.MustExec(`
			insert into received_message_log (timestamp, endpoint, user_id) values ($1, 'en', $2)`,
			now-86400, syncUsers[i])
	}
	db.MustExec(`
		update users set max_subs = 7, reports = 4, show_images = false, affiliate_params = '{"a": "b"}'
		where id = $1`,
		logged)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id) values ($1, 'en', $2)`,
		now-86400, logged)
	db.MustExec(`
		insert into sent_message_log (priority, timestamp, endpoint, user_id, result, latency)
		values (0, $1, 'ru', $2, 0, 0)`,
		now-86400, logged)
	db.MustExec(`insert into streamers (nickname) values ('s')`)
	db.MustExec(`
		insert into subscriptions (user_id, streamer_id, endpoint)
		select $1, id, 'alerts' from streamers`,
		subscribed)
	db.MustExec(`insert into block (user_id, endpoint, block) values ($1, 'en', 5)`, blocked)
	db.MustExec(`insert into block (user_id, endpoint, block) values ($1, 'en', 2)`, logged)
	db.MustExec(`insert into pending_subscriptions (user_id, nickname, endpoint) values ($1, 's', 'p')`, pending)
	db.MustExec(`
		insert into notification_queue (endpoint, user_id, streamer_id, status)
		select 'q', $1, id, 0 from streamers`,
		queued)
	db.MustExec(`
		insert into feedback (user_id, text, endpoint, timestamp) values ($1, 'hi', 'f', $2)`,
		feedbacker, now)
	db.MustExec(`
		insert into star_payments (
			user_id, endpoint, telegram_payment_charge_id, stars_amount, product, quantity, payload, timestamp)
		values ($1, 's', 'charge', 1, 'subs', 1, 'payload', $2)`,
		payer, now)
	// A logged chat on en, made first, which a repoint ignoring the endpoint would take
	for _, id := range []int64{pending, queued, payer} {
		db.MustExec(`
			insert into received_message_log (timestamp, endpoint, user_id) values ($1, 'en', $2)`,
			now-86400, id)
	}
	// A user merged into one with no chat on the merged user's endpoint
	db.MustExec(`update users set migrated_to = $1 where id = $2`, into, merged)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id) values ($1, 'a', $2)`,
		now-86400, merged)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id) values ($1, 'b', $2)`,
		now-86400, into)
	// Every column set, on a step's edge, for a user on two endpoints
	step := (now - 86400) / 6048000 * 6048000
	db.MustExec(`
		insert into sent_message_log (
			priority, timestamp, endpoint, user_id, result, latency, kind, command, reply_seq, image_mode)
		values (1, $1, 'ru', $2, 2, 3, 4, '/a', 5, 6)`,
		step, stepEdge)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id, command) values ($1, 'en', $2, '/b')`,
		step, stepEdge)
	// A chat from before the edge, so a step taking both its ends would copy the edge rows twice
	db.MustExec(`
		insert into sent_message_log (priority, timestamp, endpoint, user_id, result, latency)
		values (0, $1, 'en', $3, 0, 0), (0, $2, 'en', $3, 0, 0)`,
		step-1, step, acrossEdge)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id) values ($1, 'en', $2)`,
		step, acrossEdge)

	if !db.ApplyNextPrebuildMigrations() {
		t.Fatal("expected pending prebuild migrations")
	}
	if n := db.MustInt(`select count(*) from chats`); n != 18 {
		t.Fatalf("the prebuild should have taken the 18 logged chats, got %d", n)
	}
	if n := db.MustInt(`select count(*) from received_message_log_new`); n != 16 {
		t.Fatalf("the prebuild should have copied the 16 received messages, got %d", n)
	}

	// The bot keeps writing, and a setting changes after the copy
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id) values ($1, 'en', $2)`,
		now, late)
	db.MustExec(`update users set max_subs = 9 where id = $1`, logged)
	// Every column set, on the tail's edge, for a user on two endpoints
	boundary := db.MustInt(`select boundary from chats_conversion`)
	db.MustExec(`
		insert into sent_message_log (
			priority, timestamp, endpoint, user_id, result, latency, kind, command, reply_seq, image_mode)
		values (1, $1, 'en', $2, 6, 7, 8, '/c', 9, 10)`,
		boundary, tailEdge)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id, command) values ($1, 'ru', $2, '/d')`,
		boundary, tailEdge)
	// Every setting changes for a user the prebuild gave chats
	db.MustExec(`
		update users set
		max_subs = 6, reports = 2, show_images = false, offline_notifications = false, show_subject = false,
		silent_messages = true, member_subscriptions = true, affiliate_params = '{"c": "d"}'
		where id = $1`,
		stepEdge)
	db.MustExec(`insert into block (user_id, endpoint, block) values ($1, 'en', 3)`, stepEdge)
	// A lone setting changes for another
	db.MustExec(`update users set silent_messages = true where id = $1`, acrossEdge)
	for i, s := range syncs {
		db.MustExec(s.update, syncUsers[i])
	}
	// One user with a referral id, two sharing one, and one with an empty id
	db.MustExec(`
		insert into referrals (user_id, referral_id)
		values ($1, 'alice'), ($2, 'twins'), ($3, 'twins'), ($4, '')`,
		logged, subscribed, blocked, pending)
	oldLogRows := db.MustStrings(`
		select concat_ws(
			' ', 'sent', user_id, endpoint, priority, timestamp, result, latency, kind, command, reply_seq, image_mode)
		from sent_message_log
		union all
		select concat_ws(' ', 'received', user_id, endpoint, timestamp, command)
		from received_message_log`)

	db.ApplyMigrations()

	// Every user gets a chat on every endpoint, a new one with its settings and no reports
	if n := db.MustInt(`select count(*) from chats`); n != 22*9 {
		t.Fatalf("chats = %d, want one for each of the 22 users on each of the 9 endpoints", n)
	}
	if chat, found := db.Chat("b", UserID(unseen)); !found || chat.MaxSubs != 8 {
		t.Errorf("unseen user's chat = (%+v, %v), want one with its raised limit", chat, found)
	}
	if chat, _ := db.Chat("alerts", UserID(logged)); chat.MaxSubs != 9 {
		t.Errorf("logged user's new chat = %+v, want the limit of 9", chat)
	}
	if chat, _ := db.Chat("en", UserID(acrossEdge)); !chat.SilentMessages {
		t.Error("a lone setting change after the prebuild was lost")
	}
	// Every chat takes its user's referral id,
	// but for an empty one or a duplicate, which stays with the lowest user id
	referrals := db.MustStrings(`
		select user_id || ' ' || referral_id || ' ' || count(*)
		from chats
		where referral_id is not null
		group by user_id, referral_id`)
	slices.Sort(referrals)
	wantReferrals := []string{fmt.Sprint(logged, " alice 9"), fmt.Sprint(subscribed, " twins 9")}
	slices.Sort(wantReferrals)
	if !slices.Equal(referrals, wantReferrals) {
		t.Errorf("chats with referrals = %q, want %q", referrals, wantReferrals)
	}

	// Every row names the chat of the user and endpoint it named before
	rows := db.MustStrings(`
		select r.kind || ' ' || c.user_id || ' ' || c.endpoint
		from (
			select 'sent' as kind, chat_id from sent_message_log
			union all
			select 'received', chat_id from received_message_log
			union all
			select 'subscription', chat_id from subscriptions
			union all
			select 'pending', chat_id from pending_subscriptions
			union all
			select 'queued', chat_id from notification_queue
			union all
			select 'feedback', chat_id from feedback
			union all
			select 'payment', chat_id from star_payments
		) r
		join chats c on c.id = r.chat_id`)
	wantRows := []string{
		fmt.Sprintf("feedback %d f", feedbacker),
		fmt.Sprintf("payment %d s", payer),
		fmt.Sprintf("pending %d p", pending),
		fmt.Sprintf("queued %d q", queued),
		fmt.Sprintf("received %d en", logged),
		fmt.Sprintf("received %d en", late),
		fmt.Sprintf("received %d a", merged),
		fmt.Sprintf("received %d b", into),
		fmt.Sprintf("received %d en", stepEdge),
		fmt.Sprintf("received %d en", acrossEdge),
		fmt.Sprintf("received %d ru", tailEdge),
		fmt.Sprintf("received %d en", pending),
		fmt.Sprintf("received %d en", queued),
		fmt.Sprintf("received %d en", payer),
		fmt.Sprintf("sent %d ru", logged),
		fmt.Sprintf("sent %d ru", stepEdge),
		fmt.Sprintf("sent %d en", acrossEdge),
		fmt.Sprintf("sent %d en", acrossEdge),
		fmt.Sprintf("sent %d en", tailEdge),
		fmt.Sprintf("subscription %d alerts", subscribed),
	}
	for _, id := range syncUsers {
		wantRows = append(wantRows, fmt.Sprintf("received %d en", id))
	}
	slices.Sort(rows)
	slices.Sort(wantRows)
	if !slices.Equal(rows, wantRows) {
		t.Errorf("rows by chat = %q, want %q", rows, wantRows)
	}

	// The logs keep every column of every row
	logRows := db.MustStrings(`
		select concat_ws(
			' ', 'sent', c.user_id, c.endpoint, l.priority, l.timestamp, l.result, l.latency, l.kind, l.command,
			l.reply_seq, l.image_mode)
		from sent_message_log l
		join chats c on c.id = l.chat_id
		union all
		select concat_ws(' ', 'received', c.user_id, c.endpoint, l.timestamp, l.command)
		from received_message_log l
		join chats c on c.id = l.chat_id`)
	slices.Sort(logRows)
	slices.Sort(oldLogRows)
	if !slices.Equal(logRows, oldLogRows) {
		t.Errorf("log rows = %q, want %q", logRows, oldLogRows)
	}

	// The sync carries every setting to an old chat, and a cross-joined chat gets all but reports
	for _, want := range []Chat{
		{
			UserID: UserID(stepEdge), Endpoint: "en", MaxSubs: 6, SilentMessages: true,
			MemberSubscriptions: true, AffiliateParams: map[string]string{"c": "d"}, Blocked: 3,
		},
		{
			UserID: UserID(stepEdge), Endpoint: "alerts", MaxSubs: 6, SilentMessages: true,
			MemberSubscriptions: true, AffiliateParams: map[string]string{"c": "d"},
		},
	} {
		if chat, _ := db.Chat(want.Endpoint, want.UserID); !reflect.DeepEqual(chat, want) {
			t.Errorf("chat = %+v, want %+v", chat, want)
		}
	}

	for i, s := range syncs {
		t.Run("the sync carries "+s.column, func(t *testing.T) {
			if got := chatRow(db.Database, "en", UserID(syncUsers[i]))[s.column]; !reflect.DeepEqual(got, s.want) {
				t.Errorf("%s = %v, want %v", s.column, got, s.want)
			}
		})
	}
	for _, endpoint := range []string{"en", "ru"} {
		chat, _ := db.Chat(endpoint, UserID(logged))
		if chat.MaxSubs != 9 || chat.ShowImages || chat.AffiliateParams["a"] != "b" {
			t.Errorf("chat on %s = %+v, want the user's settings as they stood at the cutover", endpoint, chat)
		}
	}
	// Each chat from before the cross join holds its user's whole reports count
	for _, c := range []struct {
		user    int64
		reports string
	}{{logged, "4"}, {stepEdge, "2"}} {
		got := db.MustStrings(`
			select endpoint || ' ' || reports from chats where user_id = $1 and reports <> 0 order by endpoint`,
			c.user)
		if want := []string{"en " + c.reports, "ru " + c.reports}; !slices.Equal(got, want) {
			t.Errorf("user %d's chats with reports = %q, want %q", c.user, got, want)
		}
	}
	if chat, _ := db.Chat("en", UserID(blocked)); chat.Blocked != 5 {
		t.Errorf("block = %d, want 5 from the block table", chat.Blocked)
	}
	en, _ := db.Chat("en", UserID(logged))
	ru, _ := db.Chat("ru", UserID(logged))
	if en.Blocked != 2 || ru.Blocked != 0 {
		t.Errorf("block = %d on en and %d on ru, want 2 and 0", en.Blocked, ru.Blocked)
	}
	if db.MustBool(`select exists(select 1 from information_schema.tables where table_name = 'block')`) {
		t.Error("the block table survived the cutover")
	}
	if db.MustBool(`
		select exists(select 1 from information_schema.columns where table_name = 'users' and column_name = 'max_subs')`) {
		t.Error("users kept max_subs")
	}
	if n := db.MustInt(`select count(*) from users where tg_chat_id = 1`); n != 1 {
		t.Error("users.chat_id was not renamed to tg_chat_id")
	}
	if db.MustBool(`
		select to_regclass('sent_message_log_old') is not null or to_regclass('received_message_log_old') is not null`) {
		t.Error("an old log survived the cutover")
	}
	if n := db.MustInt(`
		select count(*) from pg_indexes
		where indexname in ('ix_sent_message_log_timestamp', 'ix_received_message_log_timestamp')`); n != 2 {
		t.Error("the logs' timestamp indexes kept their prebuild names")
	}
}

// A feedback row with no user fails the prebuild, rather than the cutover during downtime
func TestPrebuildRefusesFeedbackWithNoUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	rewindChatsMigrations(db)
	db.MustExec(`insert into feedback (text, endpoint, timestamp) values ('hi', 'f', 0)`)

	if text := panicText(func() { db.ApplyNextPrebuildMigrations() }); !strings.Contains(text, "null values") {
		t.Fatalf("the prebuild took a feedback row with no user: %q", text)
	}
}

// TestOldLogsDropResumes interrupts the old logs' drop twice, then finishes and replays it
func TestOldLogsDropResumes(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	rewindChatsMigrations(db)

	now := int(time.Now().Unix())
	user := db.MustInt(`insert into users (chat_id, max_subs) values (1, 3) returning id`)
	// 12 sent chunks, more than the 10 drops between commits
	db.MustExec(`
		insert into sent_message_log (priority, timestamp, endpoint, user_id, result, latency)
		select 0, $1 - i * 604800, 'en', $2, 0, 0 from generate_series(1, 12) i`,
		now, user)
	db.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id) values ($1, 'en', $2)`,
		now, user)

	holder := NewDatabase(db.connStr, false, 5)
	defer func() { _ = holder.Close() }()
	// interrupt runs the migrations until the drop times out on the chunk of log at position
	interrupt := func(log string, position int) {
		t.Helper()
		ctx := context.Background()
		tx, err := holder.begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var chunk string
		err = tx.QueryRow(ctx, `select show_chunks($1)::text offset $2 limit 1`, log, position).Scan(&chunk)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `lock table `+chunk+` in access share mode`); err != nil {
			t.Fatal(err)
		}
		db.MustExec(`set lock_timeout = '50ms'`)
		defer db.MustExec(`reset lock_timeout`)
		if text := panicText(db.ApplyMigrations); !strings.Contains(text, "lock timeout") {
			t.Fatalf("the migrations ran past the locked chunk of %s: %q", log, text)
		}
	}

	interrupt("sent_message_log", 10)
	if n := db.MustInt(`select count(*) from show_chunks('sent_message_log_old')`); n != 2 {
		t.Fatalf("the old sent log has %d chunks, want the 2 left after the 10 committed drops", n)
	}
	interrupt("received_message_log_old", 0)
	if db.MustBool(`select to_regclass('sent_message_log_old') is not null`) {
		t.Fatal("the old sent log's drop was not committed before the received log's chunks")
	}

	db.ApplyMigrations()
	// A replay of the finished drop
	db.MustExec(`delete from schema_migrations where name = 'chats_7'`)
	db.ApplyMigrations()

	if db.MustBool(`select to_regclass('received_message_log_old') is not null`) {
		t.Error("the old received log survived the rerun")
	}
	if n := db.MustInt(`
		select count(*) from pg_indexes
		where indexname in ('ix_sent_message_log_timestamp', 'ix_received_message_log_timestamp')`); n != 2 {
		t.Error("the logs' timestamp indexes lost their names")
	}
	if n := db.MustInt(`
		select (select count(*) from sent_message_log) + (select count(*) from received_message_log)`); n != 13 {
		t.Errorf("the logs hold %d rows, want the 13 copied", n)
	}
}
