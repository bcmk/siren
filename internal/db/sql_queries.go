package db

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bcmk/siren/v3/lib/cmdlib"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// nullableCommand maps an absent command to a sql null,
// so every command column spells "no command" the same way.
func nullableCommand(command string) *string {
	if command == "" {
		return nil
	}
	return &command
}

// NewNotifications returns new notifications
func (d *Database) NewNotifications() []Notification {
	var nots []Notification
	var iter Notification
	d.MustQuery(`
		select
			n.id, n.endpoint, u.id, n.streamer_id, s.nickname, n.status,
			n.time_diff, n.image_url, n.viewers, n.show_kind, n.social, n.priority,
			n.sound, n.kind, coalesce(n.command, ''), n.reply_seq, n.fields_hint,
			n.subject, u.silent_messages
		from notification_queue n
		join users u on u.id = n.user_id
		join streamers s on s.id = n.streamer_id
		where n.sending = 0
		order by n.id`,
		nil,
		ScanTo{
			&iter.ID,
			&iter.Endpoint,
			&iter.UserID,
			&iter.StreamerID,
			&iter.Nickname,
			&iter.Status,
			&iter.TimeDiff,
			&iter.ImageURL,
			&iter.Viewers,
			&iter.ShowKind,
			&iter.Social,
			&iter.Priority,
			&iter.Sound,
			&iter.Kind,
			&iter.Command,
			&iter.ReplySeq,
			&iter.FieldsHint,
			&iter.Subject,
			&iter.SilentMessages,
		},
		func() { nots = append(nots, iter) },
	)
	d.MustExec("update notification_queue set sending = 1 where sending = 0")
	return nots
}

// StoreNotifications stores notifications
func (d *Database) StoreNotifications(nots []Notification) {
	measureDone := d.Measure("db: insert notifications")
	defer measureDone()
	batch := &pgx.Batch{}
	for _, n := range nots {
		batch.Queue(`
			insert into notification_queue (
				endpoint,
				user_id,
				streamer_id,
				status,
				time_diff,
				image_url,
				viewers,
				show_kind,
				social,
				priority,
				sound,
				kind,
				command,
				reply_seq,
				fields_hint,
				subject
			)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			n.Endpoint, int64(n.UserID), n.StreamerID, n.Status, n.TimeDiff, n.ImageURL, n.Viewers,
			n.ShowKind, n.Social, n.Priority, n.Sound, n.Kind, nullableCommand(n.Command), n.ReplySeq,
			n.FieldsHint, n.Subject,
		)
	}
	d.SendBatch(batch)
}

// UsersForStreamers returns users subscribed to particular streamers
func (d *Database) UsersForStreamers(streamerIDs []int) (users map[int][]User, endpoints map[int][]string) {
	users = map[int][]User{}
	endpoints = make(map[int][]string)
	var streamerID int
	var chatID int64
	var userID int64
	var endpoint string
	var offlineNotifications bool
	var showImages bool
	var showSubject bool
	d.MustQuery(`
		select
			sub.streamer_id,
			u.chat_id,
			u.id,
			sub.endpoint,
			u.offline_notifications,
			u.show_images,
			u.show_subject
		from subscriptions sub
		join users u on u.id = sub.user_id
		where sub.streamer_id = any($1)`,
		QueryParams{streamerIDs},
		ScanTo{&streamerID, &chatID, &userID, &endpoint, &offlineNotifications, &showImages, &showSubject},
		func() {
			users[streamerID] = append(users[streamerID], User{
				ChatID:               chatID,
				UserID:               UserID(userID),
				OfflineNotifications: offlineNotifications,
				ShowImages:           showImages,
				ShowSubject:          showSubject,
			})
			endpoints[streamerID] = append(endpoints[streamerID], endpoint)
		})
	return
}

// BroadcastUsers returns the users to broadcast to on an endpoint:
// its private subscribers (chat_id > 0 excludes groups and channels).
// trySend resolves each user's current chat id at dispatch.
func (d *Database) BroadcastUsers(endpoint string) (users []UserID) {
	var id int64
	d.MustQuery(`
		select distinct u.id
		from subscriptions sub
		join users u on u.id = sub.user_id
		where sub.endpoint = $1 and u.chat_id > 0
		order by u.id`,
		QueryParams{endpoint},
		ScanTo{&id},
		func() { users = append(users, UserID(id)) })
	return
}

// StreamersForUser returns streamers that a particular user is subscribed to
func (d *Database) StreamersForUser(endpoint string, userID UserID) (streamers []Streamer) {
	var iter Streamer
	d.MustQuery(`
		select s.id, s.nickname
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		where sub.user_id = $1 and sub.endpoint = $2
		order by s.nickname`,
		QueryParams{int64(userID), endpoint},
		ScanTo{&iter.ID, &iter.Nickname},
		func() { streamers = append(streamers, iter) })
	return
}

// UnconfirmedStatusesForUser returns streamers a user is subscribed to,
// with their unconfirmed statuses.
func (d *Database) UnconfirmedStatusesForUser(endpoint string, userID UserID) (statuses []Streamer) {
	var iter Streamer
	d.MustQuery(`
		select
			s.id,
			s.nickname,
			s.unconfirmed_status,
			s.unconfirmed_timestamp,
			s.prev_unconfirmed_status,
			s.prev_unconfirmed_timestamp
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		where sub.user_id = $1 and sub.endpoint = $2
		order by s.nickname`,
		QueryParams{int64(userID), endpoint},
		ScanTo{
			&iter.ID,
			&iter.Nickname,
			&iter.UnconfirmedStatus,
			&iter.UnconfirmedTimestamp,
			&iter.PrevUnconfirmedStatus,
			&iter.PrevUnconfirmedTimestamp,
		},
		func() { statuses = append(statuses, iter) })
	return
}

// SubscribedOrPending checks if a subscription or pending subscription exists
func (d *Database) SubscribedOrPending(endpoint string, userID UserID, nickname string) bool {
	return d.MustBool(`
		select exists(
			select 1 from subscriptions sub
			join streamers s on s.id = sub.streamer_id
			where sub.user_id = $1 and s.nickname = $2 and sub.endpoint = $3
			union all
			select 1 from pending_subscriptions ps
			where ps.user_id = $1 and ps.nickname = $2 and ps.endpoint = $3
		)`,
		int64(userID), nickname, endpoint)
}

// SubscribedOrPendingCount returns the total number of subscriptions
// and pending subscriptions of a particular user
func (d *Database) SubscribedOrPendingCount(endpoint string, userID UserID) int {
	return d.MustInt(`
		select
			(select count(*) from subscriptions sub
			where sub.user_id = $1 and sub.endpoint = $2) +
			(select count(*) from pending_subscriptions ps
			where ps.user_id = $1 and ps.endpoint = $2)`,
		int64(userID), endpoint)
}

// User queries a user with particular ID
func (d *Database) User(chatID int64) (user User, found bool) {
	// Follow migrated_to like addUser, so a tombstoned chat reads its live user.
	// The walk is repeated in full in each resolver by design, not shared.
	// migrated_to is acyclic (the group-to-supergroup upgrade is one-way),
	// so the chain has a live end to select; a cycle would select nothing.
	found = d.MaybeRecord(`
		with recursive chain as (
			select id, migrated_to from users where chat_id = $1
			union
			select u.id, u.migrated_to from users u join chain c on u.id = c.migrated_to
		)
		select
			id,
			chat_id,
			max_subs,
			reports,
			blacklist,
			show_images,
			offline_notifications,
			show_subject,
			silent_messages,
			created_at,
			chat_type,
			member_count
		from users
		where id = (select id from chain where migrated_to is null)
	`,
		QueryParams{chatID},
		ScanTo{
			&user.UserID,
			&user.ChatID,
			&user.MaxSubs,
			&user.Reports,
			&user.Blacklist,
			&user.ShowImages,
			&user.OfflineNotifications,
			&user.ShowSubject,
			&user.SilentMessages,
			&user.CreatedAt,
			&user.ChatType,
			&user.MemberCount,
		})
	return
}

// UserByID queries a user by its surrogate id.
func (d *Database) UserByID(userID UserID) (user User, found bool) {
	found = d.MaybeRecord(`
		select
			id,
			chat_id,
			max_subs,
			reports,
			blacklist,
			show_images,
			offline_notifications,
			show_subject,
			silent_messages,
			created_at,
			chat_type,
			member_count
		from users
		where id = $1
	`,
		QueryParams{int64(userID)},
		ScanTo{
			&user.UserID,
			&user.ChatID,
			&user.MaxSubs,
			&user.Reports,
			&user.Blacklist,
			&user.ShowImages,
			&user.OfflineNotifications,
			&user.ShowSubject,
			&user.SilentMessages,
			&user.CreatedAt,
			&user.ChatType,
			&user.MemberCount,
		})
	return
}

// querier runs queries on either the pool or a caller's transaction,
// so user resolution can join a caller's transaction or run on its own.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// AddUser ensures a user row for chatID and returns its surrogate id.
// created reports whether the row was inserted now
// (false if it already existed), which gates the referral follower bonus.
func (d *Database) AddUser(chatID int64, maxSubs int, now int, chatType string) (id UserID, created bool) {
	defer d.Measure("db: add user")()
	return d.addUser(d.db, chatID, maxSubs, now, chatType)
}

// AddUserInTx is AddUser inside a caller's transaction,
// so a rollback undoes a row it creates.
func (d *Database) AddUserInTx(tx pgx.Tx, chatID int64, maxSubs int, now int, chatType string) (id UserID, created bool) {
	return d.addUser(tx, chatID, maxSubs, now, chatType)
}

// addUser reads first, deliberately, not a plain insert-on-conflict upsert:
// this runs on nearly every update, for a chat that almost always exists,
// so the common path is one indexed select with no write,
// no row lock, no dead tuple, and no burned sequence value.
// Only a brand-new chat falls to the insert,
// whose on conflict guards a concurrent insert
// and backfills a chat_type left null by an earlier resolve with no type.
func (d *Database) addUser(q querier, chatID int64, maxSubs int, now int, chatType string) (id UserID, created bool) {
	ctx := context.Background()
	var raw int64
	var storedType *string
	// Like ChatIDForUser on the send path, follow migrated_to:
	// an in-flight update to a tombstoned chat resolves to the destination user,
	// one identity whether we write its data or reply.
	// The walk is repeated in full in each resolver by design, not shared.
	// migrated_to is acyclic (the group-to-supergroup upgrade is one-way),
	// so the chain has a live end to select; a cycle would select nothing.
	err := q.QueryRow(ctx, `
		with recursive chain as (
			select id, chat_type, migrated_to from users where chat_id = $1
			union
			select u.id, u.chat_type, u.migrated_to
			from users u
			join chain c on u.id = c.migrated_to
		)
		select id, chat_type from chain where migrated_to is null`,
		chatID).Scan(&raw, &storedType)
	if err == nil {
		if storedType == nil && chatType != "" {
			_, err := q.Exec(ctx, "update users set chat_type = $1 where id = $2", chatType, raw)
			checkErr(err)
		}
		return UserID(raw), false
	}
	if err != pgx.ErrNoRows {
		checkErr(err)
	}
	// (xmax = 0) tells a fresh insert from an on-conflict update:
	// Postgres zeroes xmax on insert and stamps it with the txid on update.
	// A documented, stable signal, fine to rely on:
	// the referral follower bonus gates on this created flag.
	checkErr(q.QueryRow(ctx, `
		insert into users (chat_id, max_subs, created_at, chat_type)
		values ($1, $2, $3, nullif($4, ''))
		on conflict (chat_id) do update
		set chat_type = case when users.chat_type is null then excluded.chat_type else users.chat_type end
		returning id, (xmax = 0)`,
		chatID, maxSubs, now, chatType).Scan(&raw, &created))
	return UserID(raw), created
}

// EnsureUser returns chatID's surrogate id,
// creating the user (defaultMaxSubs) if missing.
// It is the single create-if-missing path.
func (d *Database) EnsureUser(chatID int64) UserID {
	id, _ := d.AddUser(chatID, d.defaultMaxSubs, int(time.Now().Unix()), "")
	return id
}

// ChatIDForUser returns the current chat_id for a user's surrogate id,
// and false if the user has no row.
// It never panics on a missing user,
// so a dispatch-time lookup can't crash the send goroutine.
//
// It follows migrated_to so a queued send for a tombstoned source
// reaches the live destination chat, not the dead old one.
// union bounds the walk; without it a cycle would loop forever.
func (d *Database) ChatIDForUser(userID UserID) (int64, bool) {
	defer d.Measure("db: chat id for user")()
	var chatID int64
	// The walk is repeated in full in each resolver by design, not shared.
	// migrated_to is acyclic (the group-to-supergroup upgrade is one-way),
	// so the chain has a live end to select; a cycle would select nothing.
	err := d.db.QueryRow(context.Background(), `
		with recursive chain as (
			select id, chat_id, migrated_to from users where id = $1
			union
			select u.id, u.chat_id, u.migrated_to
			from users u
			join chain c on u.id = c.migrated_to
		)
		select chat_id from chain where migrated_to is null`,
		int64(userID)).Scan(&chatID)
	if err == pgx.ErrNoRows {
		return 0, false
	}
	checkErr(err)
	return chatID, true
}

// LiveUserID follows migrated_to from a user's surrogate id
// to the live user it merged into,
// so bookkeeping for an in-flight send lands on the live row
// even if a migrate tombstoned the id between dispatch and result.
// It returns the input id if the user has no row (defensive).
func (d *Database) LiveUserID(userID UserID) UserID {
	defer d.Measure("db: live user id")()
	var id int64
	// The walk is repeated in full in each resolver by design, not shared.
	// migrated_to is acyclic (the group-to-supergroup upgrade is one-way),
	// so the chain has a live end to select; a cycle would select nothing.
	err := d.db.QueryRow(context.Background(), `
		with recursive chain as (
			select id, migrated_to from users where id = $1
			union
			select u.id, u.migrated_to
			from users u
			join chain c on u.id = c.migrated_to
		)
		select id from chain where migrated_to is null`,
		int64(userID)).Scan(&id)
	if err == pgx.ErrNoRows {
		return userID
	}
	checkErr(err)
	return UserID(id)
}

// ChatMigration is the outcome of a chat migration, for the caller to log.
// Renamed means the source was simply renamed to the new id;
// otherwise the destination already existed and the source was tombstoned,
// with the counts of the history rows moved to it.
type ChatMigration struct {
	Renamed   bool
	Feedback  int64
	Payments  int64
	Referrals int64
}

// MigrateChat moves a chat to a new ID after a group-to-supergroup upgrade.
// chat_id is a mutable field on users, so a rename is one update,
// and every row keyed on user_id follows automatically.
//
// If the destination already exists (the same chat recorded twice),
// its limit is raised to the larger of the two.
// The source's operational rows are dropped, its small history
// (feedback, payments, referrals) moves to the destination,
// and the source is kept as a tombstone for its BRIN message logs,
// linked via migrated_to.
//
// It returns the outcome, or nil when there was nothing to do: invalid ids,
// or the migration was already applied (a redelivery).
// A non-nil result lets the caller log it
// and write one received_message_log event per migration.
func (d *Database) MigrateChat(fromID, toID int64) *ChatMigration {
	if fromID == toID || fromID == 0 || toID == 0 {
		return nil
	}
	defer d.Measure("db: migrate chat")()
	ctx := context.Background()
	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Already gone, or already tombstoned by a prior apply:
	// a redelivery, nothing to do.
	// A rename clears the source's chat_id, so it reads as gone here;
	// a tombstone keeps it, so migrated_to marks an applied one.
	var srcID int64
	var migratedTo *int64
	err = tx.QueryRow(ctx, "select id, migrated_to from users where chat_id = $1", fromID).Scan(&srcID, &migratedTo)
	if err == pgx.ErrNoRows {
		return nil
	}
	checkErr(err)
	if migratedTo != nil {
		return nil
	}

	var dstID int64
	err = tx.QueryRow(ctx, "select id from users where chat_id = $1", toID).Scan(&dstID)
	if err == pgx.ErrNoRows {
		// The destination is new: just rename the chat.
		// The new chat is always a supergroup,
		// and every child row follows by its user_id.
		_, err = tx.Exec(ctx,
			"update users set chat_id = $1, chat_type = 'supergroup' where id = $2", toID, srcID)
		checkErr(err)
		checkErr(tx.Commit(ctx))
		return &ChatMigration{Renamed: true}
	}
	checkErr(err)

	// The destination is an active chat (the same chat recorded twice).
	// Raise its limit and drop the source's operational rows.
	// Move its small history (feedback, payments, referrals) to the destination,
	// then keep the source as a tombstone for its BRIN message logs,
	// linked via migrated_to.
	_, err = tx.Exec(ctx, `
		update users d set max_subs = greatest(d.max_subs, s.max_subs)
		from users s
		where d.id = $1 and s.id = $2`,
		dstID, srcID)
	checkErr(err)
	del := func(q string) {
		_, err := tx.Exec(ctx, q, srcID)
		checkErr(err)
	}
	del("delete from subscriptions where user_id = $1")
	del("delete from pending_subscriptions where user_id = $1")
	del("delete from block where user_id = $1")
	// Keep an in-flight notification (sending = 1): a send is mid-delivery
	// for it and the resend carries its id, so dropping the row here
	// would leave a crash or an overflowed resend with nothing to re-arm.
	// The tombstone still resolves to the destination chat,
	// so a re-armed row redelivers there.
	del("delete from notification_queue where user_id = $1 and sending = 0")
	// Keep the source's referral key when the destination has none:
	// move it, so links shared for the old chat still credit the merged user.
	// Otherwise drop it, since a user has a single key.
	_, err = tx.Exec(ctx, `
		update referrals set user_id = $1
		where user_id = $2 and not exists (select 1 from referrals where user_id = $1)`,
		dstID, srcID)
	checkErr(err)
	del("delete from referrals where user_id = $1")
	move := func(q string) int64 {
		tag, err := tx.Exec(ctx, q, dstID, srcID)
		checkErr(err)
		return tag.RowsAffected()
	}
	feedback := move("update feedback set user_id = $1 where user_id = $2")
	payments := move("update star_payments set user_id = $1 where user_id = $2")
	referrals := move("update referral_events set referrer_user_id = $1 where referrer_user_id = $2") +
		move("update referral_events set follower_user_id = $1 where follower_user_id = $2")
	_, err = tx.Exec(ctx, "update users set migrated_to = $1 where id = $2", dstID, srcID)
	checkErr(err)
	checkErr(tx.Commit(ctx))
	return &ChatMigration{Feedback: feedback, Payments: payments, Referrals: referrals}
}

// MaybeStreamer returns a streamer if exists
func (d *Database) MaybeStreamer(nickname string) *Streamer {
	var result Streamer
	if d.MaybeRecord(`
		select
			id,
			nickname,
			confirmed_status,
			unconfirmed_status,
			unconfirmed_timestamp,
			prev_unconfirmed_status,
			prev_unconfirmed_timestamp
		from streamers
		where nickname = $1`,
		QueryParams{nickname},
		ScanTo{
			&result.ID,
			&result.Nickname,
			&result.ConfirmedStatus,
			&result.UnconfirmedStatus,
			&result.UnconfirmedTimestamp,
			&result.PrevUnconfirmedStatus,
			&result.PrevUnconfirmedTimestamp,
		}) {
		return &result
	}
	return nil
}

// ChangesFromToForStreamers returns all changes for multiple streamers in specified period
func (d *Database) ChangesFromToForStreamers(streamerIDs []int, from int, to int) map[int][]StatusChange {
	result := make(map[int][]StatusChange)
	var streamerID int
	var change StatusChange
	d.MustQuery(`
		with last_before as (
			select lb.streamer_id, lb.status, lb.timestamp
			from unnest($1::integer[]) as s(id)
			cross join lateral (
				select streamer_id, status, timestamp
				from status_changes
				where streamer_id = s.id
				and timestamp < $2
				order by timestamp desc
				limit 1
			) lb
		)
		select streamer_id, status, timestamp
		from (
			select streamer_id, status, timestamp
			from status_changes
			where streamer_id = any($1)
			and timestamp >= $2
			and timestamp <= $3
			union all
			select streamer_id, status, timestamp
			from last_before
		) combined
		order by streamer_id, timestamp`,
		QueryParams{streamerIDs, from, to},
		ScanTo{&streamerID, &change.Status, &change.Timestamp},
		func() {
			result[streamerID] = append(result[streamerID], change)
		})
	for _, id := range streamerIDs {
		result[id] = append(result[id], StatusChange{Timestamp: to})
	}
	return result
}

// SetLimit updates a particular user with its max subs limit
func (d *Database) SetLimit(userID UserID, maxSubs int) {
	d.MustExec("update users set max_subs = $1 where id = $2", maxSubs, int64(userID))
}

// GrantStarPaymentSubs records the charge and bumps max_subs
// in one transaction, returning the new max_subs.
// A duplicate charge returns (false, 0) and changes nothing.
// The user row is created if the chat has none, so a charge is always credited.
func (d *Database) GrantStarPaymentSubs(
	chatID int64,
	endpoint string,
	chargeID string,
	stars int,
	product string,
	quantity int,
	payload string,
	now int,
) (added bool, maxSubs int, userID UserID) {
	defer d.Measure("db: grant star payment subs")()
	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Resolve the user in the charge tx, so a rollback undoes any new row.
	// The charge is credited even when the chat never started the bot normally.
	// The id comes back from here so the caller resolves no user
	// for a rejected payload or a duplicate.
	userID, _ = d.AddUserInTx(tx, chatID, d.defaultMaxSubs, now, "")

	tag, err := tx.Exec(context.Background(), `
		insert into star_payments (
			user_id, endpoint, telegram_payment_charge_id,
			stars_amount, product, quantity, payload, timestamp)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		on conflict (telegram_payment_charge_id) do nothing`,
		userID, endpoint, chargeID, stars, product, quantity, payload, now)
	checkErr(err)
	if tag.RowsAffected() == 0 {
		// A genuine duplicate means the first payment already committed the user,
		// so AddUserInTx found it and the id is valid despite the rollback.
		return false, 0, userID
	}
	err = tx.QueryRow(context.Background(),
		"update users set max_subs = max_subs + $1 where id = $2 returning max_subs",
		quantity, userID).Scan(&maxSubs)
	checkErr(err)
	checkErr(tx.Commit(context.Background()))
	return true, maxSubs, userID
}

// ConfirmSub confirms a pending subscription by upserting the streamer,
// moving it from pending_subscriptions to subscriptions,
// and deleting the pending entry. Returns the streamer ID.
func (d *Database) ConfirmSub(sub PendingSubscription) int {
	defer d.Measure("db: confirm sub")()
	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// "do nothing" suppresses returning on conflict,
	// so we fall back to a plain select via union all.
	// The outer query uses the pre-insert snapshot,
	// so the two branches never overlap.
	// new_nickname inserts into the nicknames table
	// only when a new streamer is created.
	var streamerID int
	err = tx.QueryRow(context.Background(), `
		with new_streamer as (
			insert into streamers (nickname)
			values ($1)
			on conflict(nickname) do nothing
			returning id
		),
		new_nickname as (
			insert into nicknames (nickname)
			select $1 where exists (select 1 from new_streamer)
		)
		select id from new_streamer
		union all
		select id from streamers where nickname = $1
		limit 1`,
		sub.Nickname).Scan(&streamerID)
	checkErr(err)
	_, err = tx.Exec(context.Background(), `
		insert into subscriptions (user_id, streamer_id, endpoint)
		values ($1, $2, $3)`,
		int64(sub.UserID), streamerID, sub.Endpoint)
	checkErr(err)
	_, err = tx.Exec(context.Background(), `
		delete from pending_subscriptions
		where user_id = $1 and endpoint = $2 and nickname = $3`,
		int64(sub.UserID), sub.Endpoint, sub.Nickname)
	checkErr(err)
	checkErr(tx.Commit(context.Background()))
	return streamerID
}

// DenySub denies a pending subscription
func (d *Database) DenySub(sub PendingSubscription) {
	d.MustExec(`
		delete from pending_subscriptions
		where user_id = $1 and endpoint = $2 and nickname = $3`,
		int64(sub.UserID), sub.Endpoint, sub.Nickname)
}

// UnconfirmedStatusesForStreamers returns unconfirmed statuses for specific streamers
func (d *Database) UnconfirmedStatusesForStreamers(nicknames []string) map[string]StatusChange {
	statusChanges := map[string]StatusChange{}
	var statusChange StatusChange
	d.MustQuery(`
		select nickname, unconfirmed_status, unconfirmed_timestamp
		from streamers
		where nickname = any($1)`,
		QueryParams{nicknames},
		ScanTo{&statusChange.Nickname, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.Nickname] = statusChange })
	return statusChanges
}

// StreamersToPoll returns nicknames flagged for per-streamer polling.
func (d *Database) StreamersToPoll() []string {
	var streamers []string
	var nickname string
	d.MustQuery(
		`select nickname from streamers where poll`,
		nil,
		ScanTo{&nickname},
		func() { streamers = append(streamers, nickname) })
	return streamers
}

// PolledStreamersWithStatus returns full streamer rows flagged for
// per-streamer polling, ordered by nickname.
func (d *Database) PolledStreamersWithStatus() []Streamer {
	var out []Streamer
	var iter Streamer
	d.MustQuery(`
		select
			id,
			nickname,
			confirmed_status,
			unconfirmed_status,
			unconfirmed_timestamp,
			prev_unconfirmed_status,
			prev_unconfirmed_timestamp
		from streamers
		where poll
		order by nickname`,
		nil,
		ScanTo{
			&iter.ID,
			&iter.Nickname,
			&iter.ConfirmedStatus,
			&iter.UnconfirmedStatus,
			&iter.UnconfirmedTimestamp,
			&iter.PrevUnconfirmedStatus,
			&iter.PrevUnconfirmedTimestamp,
		},
		func() { out = append(out, iter) })
	return out
}

// IncrementPollErrors bumps poll_error_count for the given nicknames.
// Used by the bot to surface streamers whose polled checks fail
// repeatedly so admins can spot typos or sites that block them.
func (d *Database) IncrementPollErrors(nicknames []string) {
	d.MustExec(
		`update streamers set poll_error_count = poll_error_count + 1 where nickname = any($1)`,
		nicknames)
}

// SetPoll toggles the poll flag, upserting the streamer when on=true.
// Disabling a missing streamer returns false so admins catch typos.
func (d *Database) SetPoll(nickname string, on bool) bool {
	if !on {
		return d.MustExec(`update streamers set poll = false where nickname = $1`, nickname) > 0
	}
	d.MustExec(`
		with new_streamer as (
			insert into streamers (nickname, poll) values ($1, true)
			on conflict(nickname) do update set poll = true
			returning (xmax = 0) as is_new
		)
		insert into nicknames (nickname)
		select $1 from new_streamer where is_new`,
		nickname)
	return true
}

// SubscribedStreamers returns all subscribed streamers
func (d *Database) SubscribedStreamers() map[string]bool {
	streamers := map[string]bool{}
	var nickname string
	d.MustQuery(`
		select distinct s.nickname
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id`,
		nil,
		ScanTo{&nickname},
		func() { streamers[nickname] = true })
	return streamers
}

// QueryLastSubscriptionStatuses returns latest statuses for subscriptions
func (d *Database) QueryLastSubscriptionStatuses() map[string]cmdlib.StatusKind {
	statuses := map[string]cmdlib.StatusKind{}
	var nickname string
	var status cmdlib.StatusKind
	d.MustQuery(`
		select s.nickname, s.unconfirmed_status
		from (select distinct streamer_id from subscriptions) sub
		join streamers s on s.id = sub.streamer_id`,
		nil,
		ScanTo{&nickname, &status},
		func() { statuses[nickname] = status })
	return statuses
}

// QueryLastOnlineStreamers queries latest online streamers
func (d *Database) QueryLastOnlineStreamers() map[string]bool {
	onlineStreamers := map[string]bool{}
	var nickname string
	d.MustQuery(
		`select nickname from streamers where unconfirmed_status = $1`,
		QueryParams{cmdlib.StatusOnline},
		ScanTo{&nickname},
		func() { onlineStreamers[nickname] = true })
	return onlineStreamers
}

// KnownStreamers returns all streamers with known status (not unknown).
func (d *Database) KnownStreamers() map[string]bool {
	streamers := map[string]bool{}
	var nickname string
	d.MustQuery(
		`select nickname from streamers where unconfirmed_status != 0`,
		nil,
		ScanTo{&nickname},
		func() { streamers[nickname] = true })
	return streamers
}

// SearchStreamers returns nicknames matching a search term.
// Uses a temp table with multiple search strategies:
// exact match, LIKE infix (GIN), word similarity
// (%>, forced GIN bitmap), and functional index legs
// for patterns with long non-alnum or repeated char runs.
// Results are deduplicated and sorted by trigram distance.
func (d *Database) SearchStreamers(term string) []string {
	done := d.Measure("db: search streamers")
	defer done()

	escaped := strings.NewReplacer(
		`\`, `\\`, `%`, `\%`, `_`, `\_`,
	).Replace(term)

	alnumCount := 0
	maxAlnumRun := 0
	alnumRun := 0
	maxRepeatedAlnumRun := 0
	repeatedAlnumRun := 0
	maxNonalnumRun := 0
	nonalnumRun := 0
	var prev byte
	for i := 0; i < len(term); i++ {
		c := term[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			alnumCount++
			alnumRun++
			if alnumRun > maxAlnumRun {
				maxAlnumRun = alnumRun
			}
			nonalnumRun = 0
		case c == '_', c == '-', c == '@':
			alnumRun = 0
			nonalnumRun++
			if nonalnumRun > maxNonalnumRun {
				maxNonalnumRun = nonalnumRun
			}
		default:
			return nil
		}
		isAlnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if c == prev && isAlnum {
			repeatedAlnumRun++
		} else if isAlnum {
			repeatedAlnumRun = 1
		} else {
			repeatedAlnumRun = 0
		}
		if repeatedAlnumRun > maxRepeatedAlnumRun {
			maxRepeatedAlnumRun = repeatedAlnumRun
		}
		prev = c
	}

	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Exact match via pkey — guarantees the exact streamer
	// appears in results even if other legs miss it
	_, err = tx.Exec(context.Background(),
		`
			create temp table _search_results on commit drop as
			select nickname from streamers
			where nickname = $1
		`,
		pgx.QueryExecModeExec, term)
	checkErr(err)

	// GIN LIKE infix — substring search via GIN trigram index.
	// Needs longest alnum run >= 3 for selective trigrams —
	// shorter inputs produce only space-padded trigrams
	// ("  a", " aa", "aa ") that match over half the table
	// (1.8M/3.3M), producing a huge bitmap of candidate
	// rows that GIN must scan (8s vs 8ms).
	// Long repeated-char runs are handled by the
	// repeated-alnum-runs and nonalnum-runs legs instead.
	// Forcing GIN bitmap prevents the planner from picking
	// a slow btree index-only scan.
	if maxAlnumRun >= 3 && maxRepeatedAlnumRun < 5 {
		_, err = tx.Exec(context.Background(),
			`
				set local enable_seqscan = off;
				set local enable_indexscan = off;
				set local enable_indexonlyscan = off;
				set local enable_bitmapscan = on;
				insert into _search_results
				select nickname from nicknames
				where nickname like '%' || $1 || '%'
				limit 100
			`,
			pgx.QueryExecModeSimpleProtocol, escaped)
		checkErr(err)
	}

	// Word similarity via forced GIN bitmap —
	// finds fuzzy matches (typos, partial names).
	// Planner settings force GIN bitmap scan because
	// the default planner picks pkey over GIN for %>.
	// Needs 2+ alnum chars and 4+ total chars for
	// meaningful similarity scores — single alnum chars
	// produce non-selective trigrams.
	if alnumCount >= 2 && len(term) >= 4 {
		_, err = tx.Exec(context.Background(),
			`
				set local enable_seqscan = off;
				set local enable_indexscan = off;
				set local enable_indexonlyscan = off;
				set local enable_bitmapscan = on;
				set local pg_trgm.word_similarity_threshold = 0.5;
				insert into _search_results
				select nickname from nicknames
				where nickname %> $1
				limit 100
			`,
			pgx.QueryExecModeSimpleProtocol, term)
		checkErr(err)
	}

	// Repeated-alnum-runs leg — substring search for patterns
	// with long repeated alnum runs (aaaaa, eeeeee).
	// This is a fallback because GIN doesn't count
	// occurrences of the same trigram, so GIN suggests
	// every row with the "aaa" trigram, but most don't
	// contain "aaaaaa".
	// The planner can still narrow results using BitmapAnd
	// with the GIN trigram index very effectively,
	// since alnum patterns have useful trigrams.
	if maxRepeatedAlnumRun >= 5 {
		_, err = tx.Exec(context.Background(),
			`
				set local enable_seqscan = off;
				set local enable_indexscan = off;
				set local enable_indexonlyscan = off;
				set local enable_bitmapscan = on;
				insert into _search_results
				select nickname from nicknames
				where max_repeated_alnum_run(nickname) >= $1
				and nickname like '%' || $2 || '%'
				limit 100
			`,
			pgx.QueryExecModeSimpleProtocol,
			maxRepeatedAlnumRun, escaped)
		checkErr(err)
	}

	// Nonalnum-runs leg — substring search for patterns
	// with nonalnum runs (___, _____, __________).
	// Partial covering index on max_nonalnum_run enables
	// index-only scan without heap access.
	// Disabling bitmap scan prevents GIN from being used.
	if maxNonalnumRun >= 3 {
		_, err = tx.Exec(context.Background(),
			`
				set local enable_seqscan = off;
				set local enable_indexscan = off;
				set local enable_indexonlyscan = on;
				set local enable_bitmapscan = off;
				insert into _search_results
				select nickname from nicknames
				where max_nonalnum_run(nickname) >= $1
				and nickname like '%' || $2 || '%'
				limit 100
			`,
			pgx.QueryExecModeSimpleProtocol,
			maxNonalnumRun, escaped)
		checkErr(err)
	}

	// Prefix match via btree collate "C" on streamers —
	// fallback for patterns with short alnum runs
	// (aa, ab, b__, _a_, __) where GIN trigrams are too
	// non-selective for infix and the functional index
	// isn't applicable (short runs).
	if maxAlnumRun < 3 && maxNonalnumRun < 3 {
		_, err = tx.Exec(context.Background(),
			`
				set local enable_seqscan = off;
				set local enable_indexscan = off;
				set local enable_indexonlyscan = on;
				set local enable_bitmapscan = off;
				insert into _search_results
				select nickname from streamers
				where nickname like $1 || '%'
				limit 100
			`,
			pgx.QueryExecModeSimpleProtocol, escaped)
		checkErr(err)
	}

	// Results are deduplicated and sorted by a weighted sum of
	// trigram and Levenshtein distances.
	// No planner reset needed — _search_results has no indexes.
	rows, err := tx.Query(context.Background(),
		`
			select nickname from (
				select distinct nickname from _search_results
			) sub
			order by
				2 * (nickname <-> $1)
				+ levenshtein(left(nickname, 255), left($1, 255))::float
					/ greatest(length(nickname), length($1), 1)
			limit 7
		`,
		pgx.QueryExecModeExec, term)
	checkErr(err)
	defer rows.Close()

	var streamers []string
	for rows.Next() {
		var nickname string
		checkErr(rows.Scan(&nickname))
		streamers = append(streamers, nickname)
	}
	checkErr(rows.Err())

	checkErr(tx.Commit(context.Background()))
	return streamers
}

// ReferralID returns referral identifier
func (d *Database) ReferralID(userID UserID) *string {
	var referralID string
	if !d.MaybeRecord(
		"select referral_id from referrals where user_id = $1",
		QueryParams{int64(userID)},
		ScanTo{&referralID}) {
		return nil
	}
	return &referralID
}

// UserForReferralID returns the user id for a particular referral id
func (d *Database) UserForReferralID(referralID string) *UserID {
	var id int64
	if !d.MaybeRecord(
		"select user_id from referrals where referral_id = $1",
		QueryParams{referralID},
		ScanTo{&id}) {
		return nil
	}
	userID := UserID(id)
	return &userID
}

// IncrementBlock increments blocking count for particular chat ID
func (d *Database) IncrementBlock(endpoint string, userID UserID) {
	d.MustExec(`
		insert into block as b (endpoint, user_id, block)
		values ($1, $2, 1)
		on conflict (user_id, endpoint) do update set block = b.block + 1`,
		endpoint,
		int64(userID))
}

// ResetBlock resets blocking count for particular user
func (d *Database) ResetBlock(endpoint string, userID UserID) {
	d.MustExec(
		"update block set block = 0 where endpoint = $1 and user_id = $2",
		endpoint, int64(userID))
}

// UpsertUnconfirmedTimings holds per-phase timing for UpsertUnconfirmedStatusChanges.
type UpsertUnconfirmedTimings struct {
	UpsertStreamersMs     int
	InsertNicknamesMs     int
	InsertStatusChangesMs int
	CommitMs              int
	SummarizeBrinMs       int
}

// UpsertUnconfirmedStatusChanges upserts streamers to obtain integer IDs,
// then bulk inserts into status_changes with those IDs.
func (d *Database) UpsertUnconfirmedStatusChanges(
	changedStatuses []StatusChange,
	timestamp int,
) UpsertUnconfirmedTimings {
	statusDone := d.Measure("db: insert unconfirmed status updates")
	defer statusDone()

	if len(changedStatuses) == 0 {
		return UpsertUnconfirmedTimings{}
	}

	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Upsert streamers and get integer IDs
	upsertStart := time.Now()
	nicknames := make([]string, len(changedStatuses))
	statuses := make([]int, len(changedStatuses))
	for i, sc := range changedStatuses {
		nicknames[i] = sc.Nickname
		statuses[i] = int(sc.Status)
	}
	rows, err := tx.Query(
		context.Background(),
		`
			insert into streamers (nickname, unconfirmed_status, unconfirmed_timestamp)
			select unnest($1::text[]), unnest($2::int[]), $3
			on conflict(nickname) do update set
				prev_unconfirmed_status = streamers.unconfirmed_status,
				prev_unconfirmed_timestamp = streamers.unconfirmed_timestamp,
				unconfirmed_status = excluded.unconfirmed_status,
				unconfirmed_timestamp = excluded.unconfirmed_timestamp
			returning id, nickname, (xmax = 0) as is_new
		`,
		nicknames, statuses, timestamp,
	)
	checkErr(err)
	idMap := make(map[string]int, len(changedStatuses))
	var newNicknames []string
	for rows.Next() {
		var id int
		var nickname string
		var isNew bool
		checkErr(rows.Scan(&id, &nickname, &isNew))
		idMap[nickname] = id
		if isNew {
			newNicknames = append(newNicknames, nickname)
		}
	}
	checkErr(rows.Err())
	rows.Close()
	var timings UpsertUnconfirmedTimings
	timings.UpsertStreamersMs = int(time.Since(upsertStart).Milliseconds())

	// Insert only new nicknames into the nicknames table.
	// The nicknames table holds search indexes (GIN trigram, etc.)
	// separately from streamers to avoid GIN write overhead on upserts.
	if len(newNicknames) > 0 {
		insertNicknamesStart := time.Now()
		_, err = tx.Exec(
			context.Background(),
			`insert into nicknames (nickname) select unnest($1::text[])`,
			newNicknames)
		checkErr(err)
		timings.InsertNicknamesMs = int(time.Since(insertNicknamesStart).Milliseconds())
	}

	// Use CopyFrom for fast bulk insert into status_changes
	insertStart := time.Now()
	copyRows := make([][]interface{}, len(changedStatuses))
	for i, sc := range changedStatuses {
		copyRows[i] = []interface{}{idMap[sc.Nickname], sc.Status, timestamp}
	}
	_, err = tx.CopyFrom(
		context.Background(),
		pgx.Identifier{"status_changes"},
		[]string{"streamer_id", "status", "timestamp"},
		pgx.CopyFromRows(copyRows),
	)
	checkErr(err)
	timings.InsertStatusChangesMs = int(time.Since(insertStart).Milliseconds())

	commitStart := time.Now()
	checkErr(tx.Commit(context.Background()))
	timings.CommitMs = int(time.Since(commitStart).Milliseconds())
	summarizeStart := time.Now()
	d.MustExec("select brin_summarize_new_values('ix_status_changes_timestamp')")
	timings.SummarizeBrinMs = int(time.Since(summarizeStart).Milliseconds())
	return timings
}

// AddSubscription inserts a confirmed subscription
func (d *Database) AddSubscription(userID UserID, streamerID int, endpoint string) {
	d.MustExec(`
		insert into subscriptions (user_id, streamer_id, endpoint)
		values ($1, $2, $3)`,
		int64(userID),
		streamerID,
		endpoint)
}

// AddPendingSubscription inserts a pending subscription for an unknown streamer
func (d *Database) AddPendingSubscription(
	userID UserID,
	nickname string,
	endpoint string,
	referral bool,
	command string,
	replySeq int,
) {
	d.MustExec(`
		insert into pending_subscriptions (user_id, nickname, endpoint, referral, command, reply_seq)
		values ($1, $2, $3, $4, $5, $6)`,
		int64(userID),
		nickname,
		endpoint,
		referral,
		nullableCommand(command),
		replySeq)
}

// SetShowImages updates the show_images setting for a user
func (d *Database) SetShowImages(userID UserID, showImages bool) {
	d.MustExec("update users set show_images = $1 where id = $2", showImages, int64(userID))
}

// SetOfflineNotifications updates the offline_notifications setting for a user
func (d *Database) SetOfflineNotifications(userID UserID, offlineNotifications bool) {
	d.MustExec("update users set offline_notifications = $1 where id = $2", offlineNotifications, int64(userID))
}

// SetShowSubject updates the show_subject setting for a user
func (d *Database) SetShowSubject(userID UserID, showSubject bool) {
	d.MustExec("update users set show_subject = $1 where id = $2", showSubject, int64(userID))
}

// SetSilentMessages updates the silent_messages setting for a user
func (d *Database) SetSilentMessages(userID UserID, silentMessages bool) {
	d.MustExec("update users set silent_messages = $1 where id = $2", silentMessages, int64(userID))
}

// UpdateMemberCount updates the member_count for a user
func (d *Database) UpdateMemberCount(userID UserID, memberCount int) {
	d.MustExec("update users set member_count = $1 where id = $2", memberCount, int64(userID))
}

// RemoveSubscription deletes a specific subscription
// and any pending subscription
func (d *Database) RemoveSubscription(userID UserID, nickname string, endpoint string) {
	defer d.Measure("db: remove subscription")()
	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		delete from subscriptions sub
		using streamers s
		where sub.streamer_id = s.id
		and sub.user_id = $1 and s.nickname = $2 and sub.endpoint = $3`,
		int64(userID), nickname, endpoint)
	checkErr(err)
	_, err = tx.Exec(context.Background(), `
		delete from pending_subscriptions ps
		where ps.user_id = $1 and ps.nickname = $2 and ps.endpoint = $3`,
		int64(userID), nickname, endpoint)
	checkErr(err)
	checkErr(tx.Commit(context.Background()))
}

// RemoveAllSubscriptions deletes all subscriptions and pending subscriptions
// for a user and endpoint
func (d *Database) RemoveAllSubscriptions(userID UserID, endpoint string) {
	defer d.Measure("db: remove all subscriptions")()
	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		delete from subscriptions where user_id = $1 and endpoint = $2`,
		int64(userID), endpoint)
	checkErr(err)
	_, err = tx.Exec(context.Background(), `
		delete from pending_subscriptions where user_id = $1 and endpoint = $2`,
		int64(userID), endpoint)
	checkErr(err)
	checkErr(tx.Commit(context.Background()))
}

// AddFeedback stores user feedback
func (d *Database) AddFeedback(endpoint string, userID UserID, text string, timestamp int) {
	d.MustExec(`
		insert into feedback (endpoint, user_id, text, timestamp)
		values ($1, $2, $3, $4)`,
		endpoint,
		int64(userID),
		text,
		timestamp)
}

// BlacklistUser sets the blacklist flag for a user
func (d *Database) BlacklistUser(userID UserID) {
	d.MustExec("update users set blacklist = true where id = $1", int64(userID))
}

// AddReferrerBonus adds a bonus to a referrer's max_subs
func (d *Database) AddReferrerBonus(userID UserID, bonus int) {
	d.MustExec("update users set max_subs = max_subs + $1 where id = $2", bonus, int64(userID))
}

// IncrementReferredUsers increments the referred_users count for a referral
func (d *Database) IncrementReferredUsers(userID UserID) {
	d.MustExec(
		"update referrals set referred_users = referred_users + 1 where user_id = $1",
		int64(userID))
}

// AddReferralEvent adds a referral event
func (d *Database) AddReferralEvent(timestamp int, referrerUserID *UserID, followerUserID UserID, streamerID *int) {
	var referrer *int64
	if referrerUserID != nil {
		r := int64(*referrerUserID)
		referrer = &r
	}
	d.MustExec(`
		insert into referral_events (timestamp, referrer_user_id, follower_user_id, streamer_id)
		values ($1, $2, $3, $4)`,
		timestamp,
		referrer,
		int64(followerUserID),
		streamerID)
}

// AddReferral adds a new referral record
func (d *Database) AddReferral(userID UserID, referralID string) {
	d.MustExec(
		"insert into referrals (user_id, referral_id) values ($1, $2)",
		int64(userID), referralID)
}

// LogReceivedMessage records a received message.
// The caller resolves the user id, so logging never hides a create.
// An untracked command stores a null,
// leaving the row counted with no name against it.
func (d *Database) LogReceivedMessage(timestamp int, endpoint string, userID UserID, command string) {
	d.MustExec(`
		insert into received_message_log (timestamp, endpoint, user_id, command)
		values ($1, $2, $3, $4)`,
		timestamp,
		endpoint,
		int64(userID),
		nullableCommand(command))
}

// LogPerformance logs performance data for queries and updates
func (d *Database) LogPerformance(timestamp int, kind PerformanceLogKind, durationMs int, data map[string]any) {
	jsonData, err := json.Marshal(data)
	checkErr(err)
	d.MustExec(
		"insert into performance_log (timestamp, kind, duration_ms, data) values ($1, $2, $3, $4)",
		timestamp,
		kind,
		durationMs,
		jsonData)
}

// MaintainBrinIndexes summarizes new values for BRIN indexes
func (d *Database) MaintainBrinIndexes() {
	d.MustExec("select brin_summarize_new_values('ix_sent_message_log_timestamp')")
	d.MustExec("select brin_summarize_new_values('ix_received_message_log_timestamp')")
	d.MustExec("select brin_summarize_new_values('ix_performance_log_timestamp')")
}

// MarkUnconfirmedAsChecking marks pending subscriptions as checking
func (d *Database) MarkUnconfirmedAsChecking() {
	d.MustExec("update pending_subscriptions set checking = true where not checking")
}

// ResetCheckingToUnconfirmed resets checking pending subscriptions back to not checking
func (d *Database) ResetCheckingToUnconfirmed() {
	d.MustExec("update pending_subscriptions set checking = false where checking")
}

// ResetNotificationSending resets all sending notifications to not sending
func (d *Database) ResetNotificationSending() {
	d.MustExec("update notification_queue set sending=0")
}

// LogSentMessage logs a sent message.
// An unprompted send stores a null command,
// matching how received_message_log spells an absent one.
func (d *Database) LogSentMessage(
	timestamp int,
	userID UserID,
	result int,
	endpoint string,
	priority Priority,
	latency int,
	kind PacketKind,
	command string,
	replySeq int,
) {
	d.MustExec(`
		insert into sent_message_log (
			timestamp, user_id, result, endpoint, priority, latency, kind, command, reply_seq)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		timestamp,
		int64(userID),
		result,
		endpoint,
		priority,
		latency,
		kind,
		nullableCommand(command),
		replySeq)
}

// DeleteNotification deletes a notification by ID
func (d *Database) DeleteNotification(id int) {
	d.MustExec("delete from notification_queue where id = $1", id)
}

// RequeueNotification puts a notification back in the queue,
// so a later fetch picks it up again.
func (d *Database) RequeueNotification(id int) {
	d.MustExec("update notification_queue set sending = 0 where id = $1", id)
}

// IncrementReports increments the reports count for a user
func (d *Database) IncrementReports(userID UserID) {
	d.MustExec("update users set reports=reports+1 where id = $1", int64(userID))
}

// ConfirmStatusChanges finds streamers needing confirmation and updates them.
// Returns the confirmed status changes with previous status.
func (d *Database) ConfirmStatusChanges(
	now int,
	onlineSeconds int,
	offlineSeconds int,
) []ConfirmedStatusChange {
	// PostgreSQL uses ix_streamers_status_mismatch partial index
	// for select but not for update — we use a temp table to work around this.
	done := d.Measure("db: confirm status changes")
	defer done()

	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(
		context.Background(),
		`
			create temp table to_confirm on commit drop as
			select id, nickname, unconfirmed_status, confirmed_status
			from streamers
			where confirmed_status != unconfirmed_status
			and (
				(unconfirmed_status = 2 and $1 - unconfirmed_timestamp >= $2)
				or (unconfirmed_status = 1 and $1 - unconfirmed_timestamp >= $3)
				or unconfirmed_status = 0
			)
		`,
		now, onlineSeconds, offlineSeconds)
	checkErr(err)

	_, err = tx.Exec(
		context.Background(),
		`
			update streamers c
			set confirmed_status = tc.unconfirmed_status
			from to_confirm tc
			where c.id = tc.id
		`)
	checkErr(err)

	rows, err := tx.Query(
		context.Background(),
		`select id, nickname, unconfirmed_status, confirmed_status from to_confirm`,
	)
	checkErr(err)
	defer rows.Close()

	var result []ConfirmedStatusChange
	for rows.Next() {
		var change ConfirmedStatusChange
		checkErr(rows.Scan(&change.StreamerID, &change.Nickname, &change.Status, &change.PrevStatus))
		change.Timestamp = now
		result = append(result, change)
	}
	checkErr(rows.Err())

	checkErr(tx.Commit(context.Background()))
	return result
}
