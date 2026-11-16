package db

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/bcmk/siren/v7/lib/cmdlib"
	"github.com/jackc/pgerrcode"
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
			n.id, c.endpoint, u.id, n.streamer_id, s.nickname, n.status,
			n.time_diff, n.image_url, n.profile_image_url, n.missing_image, n.viewers, n.show_kind, n.social,
			n.priority, n.sound, n.kind, coalesce(n.command, ''), n.reply_seq, n.fields_hint,
			n.subject, c.silent_messages, u.tg_chat_id, u.chat_type, c.affiliate_params, c.reports
		from notification_queue n
		-- A plain join lets the planner read all of chats and merge join it.
		-- The lateral with offset 0 keeps each id a primary key lookup.
		join lateral (
			select c.endpoint, c.user_id, c.silent_messages, c.affiliate_params, c.reports
			from chats c
			where c.id = n.chat_id
			offset 0
		) c on true
		join users u on u.id = c.user_id
		join streamers s on s.id = n.streamer_id
		where n.sending = 0
		order by n.id
		/*name='new_notifications'*/`,
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
			&iter.ProfileImageURL,
			&iter.MissingImage,
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
			&iter.ChatID,
			&iter.ChatType,
			&iter.AffiliateParams,
			&iter.Reports,
		},
		func() { nots = append(nots, iter) },
	)
	d.MustExec(`
		update notification_queue set sending = 1 where sending = 0
		/*name='new_notifications_mark_sending'*/`)
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
				chat_id,
				streamer_id,
				status,
				time_diff,
				image_url,
				profile_image_url,
				missing_image,
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
			values (
				(select id from chats where endpoint = $1 and user_id = $2),
				$3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
			/*name='store_notifications'*/`,
			n.Endpoint, int64(n.UserID), n.StreamerID, n.Status, n.TimeDiff, n.ImageURL, n.ProfileImageURL,
			n.MissingImage, n.Viewers, n.ShowKind, n.Social, n.Priority, n.Sound, n.Kind, nullableCommand(n.Command),
			n.ReplySeq, n.FieldsHint, n.Subject,
		)
	}
	d.sendBatch(batch)
}

// ChatsForStreamers returns the chats subscribed to particular streamers,
// filling only UserID, Endpoint, OfflineNotifications, ShowImages, ShowSubject and Blocked.
func (d *Database) ChatsForStreamers(streamerIDs []int) map[int][]Chat {
	chats := map[int][]Chat{}
	var streamerID int
	var iter Chat
	d.MustQuery(`
		select
			sub.streamer_id,
			c.user_id,
			c.endpoint,
			c.offline_notifications,
			c.show_images,
			c.show_subject,
			c.block
		from subscriptions sub
		join chats c on c.id = sub.chat_id
		where sub.streamer_id = any($1)
		/*name='chats_for_streamers'*/`,
		QueryParams{streamerIDs},
		ScanTo{
			&streamerID,
			&iter.UserID,
			&iter.Endpoint,
			&iter.OfflineNotifications,
			&iter.ShowImages,
			&iter.ShowSubject,
			&iter.Blocked,
		},
		func() { chats[streamerID] = append(chats[streamerID], iter) })
	return chats
}

// BroadcastChats returns the chats to broadcast to on an endpoint:
// its private subscribers (tg_chat_id > 0 excludes groups and channels).
// It fills only UserID and Blocked; trySend resolves each user's current chat id at dispatch.
func (d *Database) BroadcastChats(endpoint string) (chats []Chat) {
	var iter Chat
	d.MustQuery(`
		select c.user_id, c.block
		from chats c
		join users u on u.id = c.user_id
		where c.endpoint = $1 and u.tg_chat_id > 0
		and exists (select 1 from subscriptions sub where sub.chat_id = c.id)
		order by c.user_id
		/*name='broadcast_chats'*/`,
		QueryParams{endpoint},
		ScanTo{&iter.UserID, &iter.Blocked},
		func() { chats = append(chats, iter) })
	return
}

// StreamersForUser returns streamers that a particular user is subscribed to
func (d *Database) StreamersForUser(endpoint string, userID UserID) (streamers []Streamer) {
	var iter Streamer
	d.MustQuery(`
		select s.id, s.nickname
		from subscriptions sub
		join chats c on c.id = sub.chat_id
		join streamers s on s.id = sub.streamer_id
		where c.user_id = $1 and c.endpoint = $2
		order by s.nickname
		/*name='streamers_for_user'*/`,
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
		join chats c on c.id = sub.chat_id
		join streamers s on s.id = sub.streamer_id
		where c.user_id = $1 and c.endpoint = $2
		order by s.nickname
		/*name='unconfirmed_statuses_for_user'*/`,
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
			join chats c on c.id = sub.chat_id
			join streamers s on s.id = sub.streamer_id
			where c.user_id = $1 and s.nickname = $2 and c.endpoint = $3
			union all
			select 1 from pending_subscriptions ps
			join chats c on c.id = ps.chat_id
			where c.user_id = $1 and ps.nickname = $2 and c.endpoint = $3
		)
		/*name='subscribed_or_pending'*/`,
		int64(userID), nickname, endpoint)
}

// SubscribedOrPendingNicknames returns the nicknames a user can remove:
// subscriptions and pending subscriptions alike, as RemoveSubscription deletes both
func (d *Database) SubscribedOrPendingNicknames(endpoint string, userID UserID) (nicknames []string) {
	var iter string
	d.MustQuery(`
		select s.nickname
		from subscriptions sub
		join chats c on c.id = sub.chat_id
		join streamers s on s.id = sub.streamer_id
		where c.user_id = $1 and c.endpoint = $2
		union
		select ps.nickname
		from pending_subscriptions ps
		join chats c on c.id = ps.chat_id
		where c.user_id = $1 and c.endpoint = $2
		order by nickname
		/*name='subscribed_or_pending_nicknames'*/`,
		QueryParams{int64(userID), endpoint},
		ScanTo{&iter},
		func() { nicknames = append(nicknames, iter) })
	return
}

// SubscribedOrPendingCount returns the total number of subscriptions
// and pending subscriptions of a particular user
func (d *Database) SubscribedOrPendingCount(endpoint string, userID UserID) int {
	return d.MustInt(`
		select
			(select count(*) from subscriptions sub
			join chats c on c.id = sub.chat_id
			where c.user_id = $1 and c.endpoint = $2) +
			(select count(*) from pending_subscriptions ps
			join chats c on c.id = ps.chat_id
			where c.user_id = $1 and c.endpoint = $2)
			/*name='subscribed_or_pending_count'*/`,
		int64(userID), endpoint)
}

// UserByChatID queries a user by its chat id
func (d *Database) UserByChatID(chatID int64) (user User, found bool) {
	// Follow migrated_to like EnsureUser, so a tombstoned user reads as its live one.
	// The walk is repeated in full in each resolver by design, not shared.
	// migrated_to is acyclic (the group-to-supergroup upgrade is one-way),
	// so the chain has a live end to select; a cycle would select nothing.
	found = d.MaybeRecord(`
		with recursive chain as (
			select id, migrated_to from users where tg_chat_id = $1
			union
			select u.id, u.migrated_to from users u join chain c on u.id = c.migrated_to
		)
		select
			id,
			tg_chat_id,
			blacklist,
			chat_type,
			timezone
		from users
		where id = (select id from chain where migrated_to is null)
		/*name='user_by_tg_chat_id'*/
	`,
		QueryParams{chatID},
		ScanTo{
			&user.UserID,
			&user.ChatID,
			&user.Blacklist,
			&user.ChatType,
			&user.Timezone,
		})
	return
}

// UserByID queries a user by its surrogate id.
func (d *Database) UserByID(userID UserID) (user User, found bool) {
	found = d.MaybeRecord(`
		select
			id,
			tg_chat_id,
			blacklist,
			chat_type,
			timezone
		from users
		where id = $1
		/*name='user_by_id'*/
	`,
		QueryParams{int64(userID)},
		ScanTo{
			&user.UserID,
			&user.ChatID,
			&user.Blacklist,
			&user.ChatType,
			&user.Timezone,
		})
	return
}

// querier runs queries on either the pool or a caller's transaction,
// so user resolution can join a caller's transaction or run on its own.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// EnsureUser returns chatID's surrogate id, creating the user if missing.
// It reads first rather than upserting,
// so a known user costs one indexed select with no write,
// no row lock, no dead tuple and no burned sequence value.
func (d *Database) EnsureUser(chatID int64) UserID {
	defer d.Measure("db: ensure user")()
	var raw int64
	// Follow migrated_to, so a tombstoned user's old id names its live user.
	// The walk is repeated in full in each resolver by design, not shared.
	// migrated_to is acyclic (the group-to-supergroup upgrade is one-way),
	// so the chain has a live end to select; a cycle would select nothing.
	err := d.db.QueryRow(context.Background(), `
		with recursive chain as (
			select id, migrated_to from users where tg_chat_id = $1
			union
			select u.id, u.migrated_to
			from users u
			join chain c on u.id = c.migrated_to
		)
		select id from chain where migrated_to is null
		/*name='ensure_user_resolve'*/`,
		chatID).Scan(&raw)
	if err == pgx.ErrNoRows {
		id, _ := d.insertUser(d.db, chatID, int(time.Now().Unix()), "")
		return id
	}
	checkErr(err)
	return UserID(raw)
}

// AddUserAndChat ensures chatID's user and its chat on endpoint, with defaultMaxSubs if new.
// userCreated reports whether the user was inserted now, which gates the referee bonus.
func (d *Database) AddUserAndChat(
	endpoint string,
	chatID int64,
	now int,
	chatType string,
) (id UserID, userCreated bool) {
	defer d.Measure("db: add user and chat")()
	return d.addUserAndChat(d.db, endpoint, chatID, now, chatType)
}

// EnsureUserAndChat returns chatID's surrogate id,
// creating the user and its chat on endpoint if missing
func (d *Database) EnsureUserAndChat(endpoint string, chatID int64) UserID {
	id, _ := d.AddUserAndChat(endpoint, chatID, int(time.Now().Unix()), "")
	return id
}

// addUserAndChat is EnsureUser that also backfills a chat type
// and ensures the user's chat on endpoint.
// A known user with that chat and type costs one select.
func (d *Database) addUserAndChat(
	q querier,
	endpoint string,
	chatID int64,
	now int,
	chatType string,
) (id UserID, userCreated bool) {
	var raw int64
	var storedType *string
	var hasChat bool
	// EnsureUser's walk, also reading the chat type and whether the user has the chat.
	// Like ChatIDForUser on the send path,
	// an in-flight update to a tombstoned user resolves to the destination user,
	// one identity whether we write its data or reply.
	err := q.QueryRow(context.Background(), `
		with recursive chain as (
			select id, chat_type, migrated_to from users where tg_chat_id = $1
			union
			select u.id, u.chat_type, u.migrated_to
			from users u
			join chain c on u.id = c.migrated_to
		)
		select id, chat_type, exists (select 1 from chats where user_id = chain.id and endpoint = $2)
		from chain
		where migrated_to is null
		/*name='add_user_and_chat_resolve'*/`,
		chatID, endpoint).Scan(&raw, &storedType, &hasChat)
	if err == pgx.ErrNoRows {
		id, userCreated = d.insertUser(q, chatID, now, chatType)
		d.addChat(q, endpoint, id)
		return id, userCreated
	}
	checkErr(err)
	if storedType == nil && chatType != "" {
		_, err := q.Exec(context.Background(), `
			update users set chat_type = $1 where id = $2
			/*name='add_user_and_chat_set_chat_type'*/`,
			chatType, raw)
		checkErr(err)
	}
	if !hasChat {
		d.addChat(q, endpoint, UserID(raw))
	}
	return UserID(raw), false
}

// insertUser inserts chatID's user, its on conflict guarding a concurrent insert
// and backfilling a chat_type left null by an earlier resolve with no type.
// created reports whether the row was inserted now.
func (d *Database) insertUser(q querier, chatID int64, now int, chatType string) (id UserID, created bool) {
	var raw int64
	// (xmax = 0) tells a fresh insert from an on-conflict update:
	// Postgres zeroes xmax on insert and stamps it with the txid on update,
	// a documented, stable signal.
	checkErr(q.QueryRow(context.Background(), `
		insert into users (tg_chat_id, created_at, chat_type)
		values ($1, $2, nullif($3, ''))
		on conflict (tg_chat_id) do update
		set chat_type = case when users.chat_type is null then excluded.chat_type else users.chat_type end
		returning id, (xmax = 0)
		/*name='insert_user'*/`,
		chatID, now, chatType).Scan(&raw, &created))
	return UserID(raw), created
}

func (d *Database) addChat(q querier, endpoint string, userID UserID) {
	_, err := q.Exec(context.Background(), `
		insert into chats (user_id, endpoint, max_subs)
		values ($1, $2, $3)
		on conflict do nothing
		/*name='add_chat'*/`,
		int64(userID), endpoint, d.defaultMaxSubs)
	checkErr(err)
}

// Chat queries the user's chat on endpoint
func (d *Database) Chat(endpoint string, userID UserID) (chat Chat, found bool) {
	found = d.MaybeRecord(`
		select
			user_id,
			endpoint,
			max_subs,
			show_images,
			offline_notifications,
			show_subject,
			silent_messages,
			affiliate_params,
			member_subscriptions,
			block,
			referral_id
		from chats
		where user_id = $1 and endpoint = $2
		/*name='chat_by_user_endpoint'*/`,
		QueryParams{int64(userID), endpoint},
		ScanTo{
			&chat.UserID,
			&chat.Endpoint,
			&chat.MaxSubs,
			&chat.ShowImages,
			&chat.OfflineNotifications,
			&chat.ShowSubject,
			&chat.SilentMessages,
			&chat.AffiliateParams,
			&chat.MemberSubscriptions,
			&chat.Blocked,
			&chat.ReferralID,
		})
	return
}

// MarkGreeted marks the user's chat on endpoint greeted,
// reporting whether this was its first greeting
func (d *Database) MarkGreeted(endpoint string, userID UserID) (firstGreeting bool) {
	return d.MustExec(`
		update chats set greeted = true
		where user_id = $1 and endpoint = $2 and not greeted
		/*name='mark_chat_greeted'*/`,
		int64(userID), endpoint) == 1
}

// ChatIDForUser returns the current tg_chat_id for a user's surrogate id,
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
			select id, tg_chat_id, migrated_to from users where id = $1
			union
			select u.id, u.tg_chat_id, u.migrated_to
			from users u
			join chain c on u.id = c.migrated_to
		)
		select tg_chat_id from chain where migrated_to is null
		/*name='tg_chat_id_for_user'*/`,
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
		select id from chain where migrated_to is null
		/*name='live_user_id'*/`,
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

// MigrateChat moves a user to a new Telegram chat id after a group-to-supergroup upgrade.
// tg_chat_id is a mutable field on users, so a rename is one update,
// and every chat follows by its user_id.
//
// If the destination already exists (the same user recorded twice), the source merges into it:
//   - Each source chat merges into the destination's chat on its endpoint, copied if there is none,
//     and the limit rises to fit what the chat then holds.
//     A merge takes the larger limit, the non-default value of each flag,
//     and the source's affiliate params and referral id if the destination has none.
//     The destination's counts stay.
//   - Subscriptions, pending subscriptions and queued notifications move;
//     those the destination's chat already has are dropped,
//     and so are idle alerts for streamers it subscribes to
//   - Feedback, payments and referral events move
//   - The timezone moves if the destination has none
//
// The source is kept as a tombstone, linked via migrated_to, holding its chats' message logs.
//
// It returns the outcome, or nil when there was nothing to do: a degenerate migration,
// or one already applied (a redelivery).
// A non-nil result lets the caller log it
// and write one received_message_log event per migration.
func (d *Database) MigrateChat(fromID, toID int64) *ChatMigration {
	if fromID == toID {
		// A degenerate migration; there is nothing to move.
		return nil
	}
	defer d.Measure("db: migrate chat")()
	ctx := context.Background()
	tx, err := d.begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Already gone, or already tombstoned by a prior apply:
	// a redelivery, nothing to do.
	// A rename clears the source's tg_chat_id, so it reads as gone here;
	// a tombstone keeps it, so migrated_to marks an applied one.
	var srcID int64
	var migratedTo *int64
	err = tx.QueryRow(ctx, `
		select id, migrated_to from users where tg_chat_id = $1
		/*name='migrate_chat_source'*/`,
		fromID).Scan(&srcID, &migratedTo)
	if err == pgx.ErrNoRows {
		return nil
	}
	checkErr(err)
	if migratedTo != nil {
		return nil
	}

	var dstID int64
	err = tx.QueryRow(ctx, `
		select id from users where tg_chat_id = $1
		/*name='migrate_chat_destination'*/`,
		toID).Scan(&dstID)
	if err == pgx.ErrNoRows {
		// The destination is new: just rename the user.
		// The new Telegram chat is always a supergroup,
		// and every chat follows by its user_id.
		_, err = tx.Exec(ctx, `
			update users set tg_chat_id = $1, chat_type = 'supergroup' where id = $2
			/*name='migrate_chat_rename'*/`,
			toID, srcID)
		checkErr(err)
		checkErr(tx.Commit(ctx))
		return &ChatMigration{Renamed: true}
	}
	checkErr(err)

	// The destination exists, so merge into it
	_, err = tx.Exec(ctx, `
		update users d set timezone = coalesce(d.timezone, s.timezone)
		from users s
		where d.id = $1 and s.id = $2
		/*name='migrate_chat_merge_timezone'*/`,
		dstID, srcID)
	checkErr(err)
	tag, err := tx.Exec(ctx, `
		update referral_events set referrer_user_id = $1 where referrer_user_id = $2
		/*name='migrate_chat_move_referrer_events'*/`,
		dstID, srcID)
	checkErr(err)
	referrals := tag.RowsAffected()
	tag, err = tx.Exec(ctx, `
		update referral_events set referee_user_id = $1 where referee_user_id = $2
		/*name='migrate_chat_move_referee_events'*/`,
		dstID, srcID)
	checkErr(err)
	referrals += tag.RowsAffected()
	// Drop what the destination's chat duplicates, and move the rest to it below.
	// An idle alert is dropped where the destination's chat subscribes to its streamer:
	// that subscription and the chat's settings decide the chat's own alerts.
	// An in-flight one (sending = 1) is kept, as its resend carries its id:
	// dropped, a crash or an overflowed resend would have nothing to re-arm.
	_, err = tx.Exec(ctx, `
		delete from subscriptions sub
		using chats s, chats d, subscriptions dsub
		where sub.chat_id = s.id and s.user_id = $2 and d.user_id = $1 and d.endpoint = s.endpoint
		and dsub.chat_id = d.id and dsub.streamer_id = sub.streamer_id
		/*name='migrate_chat_delete_duplicate_subscriptions'*/`,
		dstID, srcID)
	checkErr(err)
	_, err = tx.Exec(ctx, `
		delete from pending_subscriptions ps
		using chats s, chats d, pending_subscriptions dps
		where ps.chat_id = s.id and s.user_id = $2 and d.user_id = $1 and d.endpoint = s.endpoint
		and dps.chat_id = d.id and dps.nickname = ps.nickname
		/*name='migrate_chat_delete_duplicate_pending_subscriptions'*/`,
		dstID, srcID)
	checkErr(err)
	_, err = tx.Exec(ctx, `
		delete from notification_queue n
		using chats s, chats d, subscriptions dsub
		where n.chat_id = s.id and s.user_id = $2 and d.user_id = $1 and d.endpoint = s.endpoint
		and dsub.chat_id = d.id and dsub.streamer_id = n.streamer_id and n.sending = 0 and n.kind = $3
		/*name='migrate_chat_delete_duplicate_notifications'*/`,
		dstID, srcID, NotificationPacket)
	checkErr(err)
	// The source's chats stay with the tombstone, so the bounce of a send to the old id still logs
	_, err = tx.Exec(ctx, `
		insert into chats as d (
			user_id, endpoint, max_subs, reports, show_images, offline_notifications,
			show_subject, silent_messages, member_subscriptions, affiliate_params, block, greeted
		)
		select $1, endpoint, max_subs, reports, show_images, offline_notifications,
		show_subject, silent_messages, member_subscriptions, affiliate_params, block, greeted
		from chats
		where user_id = $2
		on conflict (user_id, endpoint) do update set
		max_subs = greatest(d.max_subs, excluded.max_subs),
		affiliate_params = coalesce(d.affiliate_params, excluded.affiliate_params),
		member_subscriptions = d.member_subscriptions or excluded.member_subscriptions,
		greeted = d.greeted or excluded.greeted,
		show_images = d.show_images and excluded.show_images,
		offline_notifications = d.offline_notifications and excluded.offline_notifications,
		show_subject = d.show_subject and excluded.show_subject,
		silent_messages = d.silent_messages or excluded.silent_messages
		/*name='migrate_chat_merge_chats'*/`,
		dstID, srcID)
	checkErr(err)
	// Links shared for a source chat keep crediting its merged chat, unless that has its own id.
	// The self-join returns the id from before it is cleared.
	// The unique index holds, as the source's chat drops the id before the destination's takes it.
	_, err = tx.Exec(ctx, `
		with taken as (
			update chats s set referral_id = null
			from chats o
			where o.id = s.id and s.user_id = $2 and s.referral_id is not null
			returning s.endpoint, o.referral_id
		)
		update chats d set referral_id = t.referral_id
		from taken t
		where d.user_id = $1 and d.endpoint = t.endpoint and d.referral_id is null
		/*name='migrate_chat_move_referral_ids'*/`,
		dstID, srcID)
	checkErr(err)
	_, err = tx.Exec(ctx, `
		update subscriptions sub set chat_id = d.id
		from chats s, chats d
		where sub.chat_id = s.id and s.user_id = $2 and d.user_id = $1 and d.endpoint = s.endpoint
		/*name='migrate_chat_move_subscriptions'*/`,
		dstID, srcID)
	checkErr(err)
	_, err = tx.Exec(ctx, `
		update pending_subscriptions ps set chat_id = d.id
		from chats s, chats d
		where ps.chat_id = s.id and s.user_id = $2 and d.user_id = $1 and d.endpoint = s.endpoint
		/*name='migrate_chat_move_pending_subscriptions'*/`,
		dstID, srcID)
	checkErr(err)
	_, err = tx.Exec(ctx, `
		update notification_queue n set chat_id = d.id
		from chats s, chats d
		where n.chat_id = s.id and s.user_id = $2 and d.user_id = $1 and d.endpoint = s.endpoint
		/*name='migrate_chat_move_notifications'*/`,
		dstID, srcID)
	checkErr(err)
	// Each merged chat keeps every subscription both had, so its limit rises to fit them
	_, err = tx.Exec(ctx, `
		update chats d set max_subs = greatest(
			d.max_subs,
			(select count(*) from subscriptions where chat_id = d.id)
			+ (select count(*) from pending_subscriptions where chat_id = d.id)
		)
		from chats s
		where d.user_id = $1 and s.user_id = $2 and s.endpoint = d.endpoint
		/*name='migrate_chat_fit_limit'*/`,
		dstID, srcID)
	checkErr(err)
	tag, err = tx.Exec(ctx, `
		update feedback f set chat_id = d.id
		from chats s, chats d
		where f.chat_id = s.id and s.user_id = $2 and d.user_id = $1 and d.endpoint = s.endpoint
		/*name='migrate_chat_move_feedback'*/`,
		dstID, srcID)
	checkErr(err)
	feedback := tag.RowsAffected()
	tag, err = tx.Exec(ctx, `
		update star_payments p set chat_id = d.id
		from chats s, chats d
		where p.chat_id = s.id and s.user_id = $2 and d.user_id = $1 and d.endpoint = s.endpoint
		/*name='migrate_chat_move_payments'*/`,
		dstID, srcID)
	checkErr(err)
	payments := tag.RowsAffected()
	_, err = tx.Exec(ctx, `
		update users set migrated_to = $1 where id = $2
		/*name='migrate_chat_tombstone'*/`,
		dstID, srcID)
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
		where nickname = $1
		/*name='streamer_by_nickname'*/`,
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

// changesFromToForStreamersSQL is shared with the test that checks its plan
const changesFromToForStreamersSQL = `
	select s.id, coalesce(sc.prev_status, s.unconfirmed_status), sc.status, sc.timestamp
	from unnest($1::integer[]) as ids(id)
	-- A plain join lets the planner read all of streamers and merge join it.
	-- The lateral with offset 0 keeps each id a primary key lookup.
	join lateral (
		select st.id, st.unconfirmed_status, st.unconfirmed_timestamp
		from streamers st
		where st.id = ids.id
		offset 0
	) s on true
	-- unconfirmed_timestamp is the newest change, so a streamer older than the window has none.
	-- offset 0 keeps the subquery from being flattened,
	-- which makes that a one-time filter and skips the index scan.
	-- Flattened, it would be a join filter checked after the lookup.
	left join lateral (
		select sc.prev_status, sc.status, sc.timestamp
		from status_changes sc
		where s.unconfirmed_timestamp >= $2
		and sc.streamer_id = s.id
		and sc.timestamp >= $2
		offset 0
	) sc on true
	order by s.id, sc.timestamp
	/*name='changes_from_to_for_streamers'*/`

// BotReachDays is past the furthest the bot reads status changes back, its month grid's 35 days.
// A compactor rewrite locks a chunk out, so it stays past this.
const BotReachDays = 36

// ChangesFromToForStreamers returns each streamer's status changes in [from, to],
// opening with the status the window starts in and closing with a sentinel at its end.
// to is assumed to be the present:
// a window holding no change opens on the streamer's current status,
// and a change stamped after to, ahead of the clock, lands at to.
func (d *Database) ChangesFromToForStreamers(streamerIDs []int, from int, to int) map[int][]StatusChange {
	result := make(map[int][]StatusChange)
	var streamerID int
	var opening cmdlib.StatusKind
	var status *cmdlib.StatusKind
	var timestamp *int
	// Planned per call, so it locks only the window's chunks: a generic plan locks them all,
	// and would wait out a compactor's rewrite of an old one
	d.MustQuery(
		changesFromToForStreamersSQL,
		QueryParams{pgx.QueryExecModeExec, streamerIDs, from},
		ScanTo{&streamerID, &opening, &status, &timestamp},
		func() {
			if result[streamerID] == nil {
				result[streamerID] = []StatusChange{{Status: opening, Timestamp: from}}
			}
			if timestamp != nil {
				result[streamerID] = append(
					result[streamerID],
					StatusChange{Status: *status, Timestamp: min(*timestamp, to)})
			}
		})
	for _, id := range streamerIDs {
		result[id] = append(result[id], StatusChange{Timestamp: to})
	}
	return result
}

// SetLimit sets the max subs of the user's chat on endpoint, reporting whether the chat exists
func (d *Database) SetLimit(endpoint string, userID UserID, maxSubs int) (found bool) {
	return d.MustExec(`
		update chats set max_subs = $1 where user_id = $2 and endpoint = $3
		/*name='set_limit'*/`,
		maxSubs, int64(userID), endpoint) == 1
}

// GrantStarPaymentSubs records the charge and bumps max_subs
// in one transaction, returning the new max_subs.
// A duplicate charge changes nothing,
// returning (false, 0) with the user the first charge credited.
// The user and its chat are created if missing, so a charge is always credited.
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
	tx, err := d.begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Resolve the user in the charge tx, so a rollback undoes any new row.
	// The charge is credited even when the chat never started the bot normally.
	userID, _ = d.addUserAndChat(tx, endpoint, chatID, now, "")

	tag, err := tx.Exec(context.Background(), `
		insert into star_payments (
			chat_id, telegram_payment_charge_id,
			stars_amount, product, quantity, payload, timestamp)
		values ((select id from chats where user_id = $1 and endpoint = $2), $3, $4, $5, $6, $7, $8)
		on conflict (telegram_payment_charge_id) do nothing
		/*name='grant_star_payment_subs_record_payment'*/`,
		userID, endpoint, chargeID, stars, product, quantity, payload, now)
	checkErr(err)
	if tag.RowsAffected() == 0 {
		// Reply to the user the first charge credited: after a group-to-supergroup upgrade,
		// chatID can resolve to a user created above, which the rollback removes
		var creditedUserID int64
		err = tx.QueryRow(context.Background(), `
			select c.user_id
			from star_payments p
			join chats c on c.id = p.chat_id
			where p.telegram_payment_charge_id = $1
			/*name='grant_star_payment_subs_credited_user'*/`,
			chargeID).Scan(&creditedUserID)
		checkErr(err)
		return false, 0, UserID(creditedUserID)
	}
	err = tx.QueryRow(context.Background(), `
		update chats set max_subs = max_subs + $1 where user_id = $2 and endpoint = $3 returning max_subs
		/*name='grant_star_payment_subs_add_subs'*/`,
		quantity, userID, endpoint).Scan(&maxSubs)
	checkErr(err)
	checkErr(tx.Commit(context.Background()))
	return true, maxSubs, userID
}

// ConfirmSub confirms a pending subscription by upserting the streamer,
// moving it from pending_subscriptions to subscriptions,
// and deleting the pending entry. Returns the streamer ID.
func (d *Database) ConfirmSub(sub PendingSubscription) int {
	defer d.Measure("db: confirm sub")()
	tx, err := d.begin()
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
		limit 1
		/*name='confirm_sub_ensure_streamer'*/`,
		sub.Nickname).Scan(&streamerID)
	checkErr(err)
	d.addSubscription(tx, sub.UserID, streamerID, sub.Endpoint)
	d.deletePendingSubscription(tx, sub)
	checkErr(tx.Commit(context.Background()))
	return streamerID
}

// DenySub denies a pending subscription
func (d *Database) DenySub(sub PendingSubscription) {
	defer d.Measure("db: deny sub")()
	d.deletePendingSubscription(d.db, sub)
}

func (d *Database) deletePendingSubscription(q querier, sub PendingSubscription) {
	_, err := q.Exec(context.Background(), `
		delete from pending_subscriptions ps
		using chats c
		where c.id = ps.chat_id and c.user_id = $1 and c.endpoint = $2 and ps.nickname = $3
		/*name='delete_pending_subscription'*/`,
		int64(sub.UserID), sub.Endpoint, sub.Nickname)
	checkErr(err)
}

// UnconfirmedStatusesForStreamers returns unconfirmed statuses for specific streamers
func (d *Database) UnconfirmedStatusesForStreamers(nicknames []string) map[string]StatusChange {
	statusChanges := map[string]StatusChange{}
	var statusChange StatusChange
	d.MustQuery(`
		select nickname, unconfirmed_status, unconfirmed_timestamp
		from streamers
		where nickname = any($1)
		/*name='unconfirmed_statuses_for_streamers'*/`,
		QueryParams{nicknames},
		ScanTo{&statusChange.Nickname, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.Nickname] = statusChange })
	return statusChanges
}

// StreamersToPoll returns nicknames flagged for per-streamer polling.
func (d *Database) StreamersToPoll() []string {
	var streamers []string
	var nickname string
	d.MustQuery(`
		select nickname from streamers where poll
		/*name='streamers_to_poll'*/`,
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
		order by nickname
		/*name='polled_streamers_with_status'*/`,
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
	d.MustExec(`
		update streamers set poll_error_count = poll_error_count + 1 where nickname = any($1)
		/*name='increment_poll_errors'*/`,
		nicknames)
}

// SetPoll toggles the poll flag, upserting the streamer when on=true.
// Disabling a missing streamer returns false so admins catch typos.
func (d *Database) SetPoll(nickname string, on bool) bool {
	if !on {
		return d.MustExec(`
			update streamers set poll = false where nickname = $1
			/*name='set_poll_off'*/`,
			nickname) > 0
	}
	d.MustExec(`
		with new_streamer as (
			insert into streamers (nickname, poll) values ($1, true)
			on conflict(nickname) do update set poll = true
			returning (xmax = 0) as is_new
		)
		insert into nicknames (nickname)
		select $1 from new_streamer where is_new
		/*name='set_poll_on'*/`,
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
		join streamers s on s.id = sub.streamer_id
		/*name='subscribed_streamers'*/`,
		nil,
		ScanTo{&nickname},
		func() { streamers[nickname] = true })
	return streamers
}

// OnlineStreamers returns the streamers whose unconfirmed status is online
func (d *Database) OnlineStreamers() map[string]bool {
	onlineStreamers := map[string]bool{}
	var nickname string
	d.MustQuery(`
		select nickname from streamers where unconfirmed_status = $1
		/*name='online_streamers'*/`,
		QueryParams{cmdlib.StatusOnline},
		ScanTo{&nickname},
		func() { onlineStreamers[nickname] = true })
	return onlineStreamers
}

// KnownStreamers returns all streamers with known status (not unknown).
func (d *Database) KnownStreamers() map[string]bool {
	streamers := map[string]bool{}
	var nickname string
	d.MustQuery(`
		select nickname from streamers where unconfirmed_status != 0
		/*name='known_streamers'*/`,
		nil,
		ScanTo{&nickname},
		func() { streamers[nickname] = true })
	return streamers
}

// SearchStreamers returns up to 7 nicknames matching a search term, closest first.
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

	tx, err := d.begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Exact match via ix_streamers_nickname —
	// guarantees the exact streamer appears in results even if other legs miss it
	_, err = tx.Exec(context.Background(),
		`
			create temp table _search_results on commit drop as
			select nickname from streamers
			where nickname = $1
			/*name='search_streamers_exact'*/
		`,
		pgx.QueryExecModeExec, term)
	checkErr(err)

	// Infix leg — substring search via the GIN trigram index.
	// Needs longest alnum run >= 3 for selective trigrams —
	// shorter inputs produce only space-padded trigrams ("  a", " aa", "aa ")
	// that match over half the table,
	// producing a huge bitmap of candidate rows that GIN must scan (8s vs 8ms).
	// Long repeated-char runs are handled by the repeated-alnum-runs and nonalnum-runs legs instead.
	// Forcing GIN bitmap keeps the planner off a seq scan of nicknames.
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
				/*name='search_streamers_infix'*/
			`,
			pgx.QueryExecModeSimpleProtocol, escaped)
		checkErr(err)
	}

	// Word similarity via forced GIN bitmap — finds fuzzy matches (typos, partial names).
	// Needs 2+ alnum chars and 4+ total chars for meaningful similarity scores —
	// single alnum chars produce non-selective trigrams.
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
				/*name='search_streamers_similarity'*/
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
				/*name='search_streamers_repeated_runs'*/
			`,
			pgx.QueryExecModeSimpleProtocol,
			maxRepeatedAlnumRun, escaped)
		checkErr(err)
	}

	// Nonalnum-runs leg — substring search for patterns with nonalnum runs (___, _____, __________).
	// Partial covering index on max_nonalnum_run enables index-only scan without heap access.
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
				/*name='search_streamers_nonalnum_runs'*/
			`,
			pgx.QueryExecModeSimpleProtocol,
			maxNonalnumRun, escaped)
		checkErr(err)
	}

	// Prefix match via btree collate "C" on streamers —
	// fallback for patterns with short alnum runs (aa, ab, b__, _a_, __)
	// where GIN trigrams are too non-selective for infix
	// and the functional index isn't applicable (short runs).
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
				/*name='search_streamers_prefix'*/
			`,
			pgx.QueryExecModeSimpleProtocol, escaped)
		checkErr(err)
	}

	// The legs' enable_seqscan = off would cost _search_results' seq scan past the JIT threshold.
	_, err = tx.Exec(context.Background(), "set local enable_seqscan = on")
	checkErr(err)

	// Results are deduplicated and sorted by a weighted sum of
	// trigram and Levenshtein distances.
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
			/*name='search_streamers_rank'*/
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

// UserByReferralID returns the user whose chat on endpoint has this referral id
func (d *Database) UserByReferralID(endpoint string, referralID string) *UserID {
	var id int64
	if !d.MaybeRecord(`
		select user_id from chats where endpoint = $1 and referral_id = $2
		/*name='user_by_referral_id'*/`,
		QueryParams{endpoint, referralID},
		ScanTo{&id}) {
		return nil
	}
	userID := UserID(id)
	return &userID
}

// ReferralIDExists reports whether a chat on endpoint has taken this referral id
func (d *Database) ReferralIDExists(endpoint string, referralID string) bool {
	return d.MustBool(`
		select exists(select 1 from chats where endpoint = $1 and referral_id = $2)
		/*name='referral_id_exists'*/`,
		endpoint, referralID)
}

// ProfilePhotoUploaded reports whether this hash is the endpoint's last uploaded profile photo
func (d *Database) ProfilePhotoUploaded(endpoint string, hash string) bool {
	return d.MustBool(`
		select exists(select 1 from profile_photos where endpoint = $1 and hash = $2)
		/*name='profile_photo_uploaded'*/`,
		endpoint, hash)
}

// SetProfilePhotoHash stores the hash of the profile photo uploaded for the endpoint
func (d *Database) SetProfilePhotoHash(endpoint string, hash string) {
	d.MustExec(`
		insert into profile_photos (endpoint, hash)
		values ($1, $2)
		on conflict (endpoint) do update set hash = excluded.hash
		/*name='set_profile_photo_hash'*/`,
		endpoint,
		hash)
}

// IncrementBlock increments the blocked-send count of the user's chat on endpoint
func (d *Database) IncrementBlock(endpoint string, userID UserID) {
	d.MustExec(`
		update chats set block = block + 1 where endpoint = $1 and user_id = $2
		/*name='increment_block'*/`,
		endpoint,
		int64(userID))
}

// ResetBlock resets the blocked-send count of the user's chat on endpoint.
// It skips a count already at zero, so the common case writes nothing.
func (d *Database) ResetBlock(endpoint string, userID UserID) {
	d.MustExec(`
		update chats set block = 0 where endpoint = $1 and user_id = $2 and block <> 0
		/*name='reset_block'*/`,
		endpoint, int64(userID))
}

// UpsertUnconfirmedTimings holds per-phase timing for UpsertUnconfirmedStatusChanges.
type UpsertUnconfirmedTimings struct {
	UpsertStreamersMs     int
	InsertStatusChangesMs int
	CommitMs              int
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

	tx, err := d.begin()
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
	// The update takes known streamers and the insert new ones,
	// as insert ... on conflict would waste an id on every known streamer.
	// greatest stamps a change after the streamer's previous one, even within the same second.
	rows, err := tx.Query(
		context.Background(),
		`
			with updated as (
				update streamers s set
					prev_unconfirmed_status = s.unconfirmed_status,
					prev_unconfirmed_timestamp = s.unconfirmed_timestamp,
					unconfirmed_status = c.status,
					unconfirmed_timestamp = greatest($3::int, s.unconfirmed_timestamp + 1)
				from unnest($1::text[], $2::int[]) as c(nickname, status)
				where s.nickname = c.nickname
				returning s.id, s.unconfirmed_timestamp, s.unconfirmed_status, s.prev_unconfirmed_status
			),
			inserted as (
				insert into streamers (nickname, unconfirmed_status, unconfirmed_timestamp)
				select c.nickname, c.status, $3::int
				from unnest($1::text[], $2::int[]) as c(nickname, status)
				where not exists (select from streamers s where s.nickname = c.nickname)
				returning id, nickname, unconfirmed_timestamp, unconfirmed_status, prev_unconfirmed_status
			),
			new_nicknames as (
				insert into nicknames (nickname) select nickname from inserted
			)
			select id, unconfirmed_timestamp, unconfirmed_status, prev_unconfirmed_status from inserted
			union all
			select id, unconfirmed_timestamp, unconfirmed_status, prev_unconfirmed_status from updated
			/*name='upsert_unconfirmed_statuses'*/
		`,
		nicknames, statuses, timestamp,
	)
	checkErr(err)
	// prev_unconfirmed_status is the status this change leaves behind:
	// the update has already moved it aside.
	copyRows := make([][]interface{}, 0, len(changedStatuses))
	for rows.Next() {
		var id, stamp int
		var status, prevStatus int16
		checkErr(rows.Scan(&id, &stamp, &status, &prevStatus))
		copyRows = append(copyRows, []interface{}{stamp, id, status, prevStatus})
	}
	checkErr(rows.Err())
	rows.Close()
	var timings UpsertUnconfirmedTimings
	timings.UpsertStreamersMs = int(time.Since(upsertStart).Milliseconds())

	// Use CopyFrom for fast bulk insert into status_changes
	insertStart := time.Now()
	_, err = tx.CopyFrom(
		context.Background(),
		pgx.Identifier{"status_changes"},
		[]string{"timestamp", "streamer_id", "status", "prev_status"},
		pgx.CopyFromRows(copyRows),
	)
	checkErr(err)
	timings.InsertStatusChangesMs = int(time.Since(insertStart).Milliseconds())

	commitStart := time.Now()
	checkErr(tx.Commit(context.Background()))
	timings.CommitMs = int(time.Since(commitStart).Milliseconds())
	return timings
}

// AddSubscription inserts a confirmed subscription
func (d *Database) AddSubscription(userID UserID, streamerID int, endpoint string) {
	defer d.Measure("db: add subscription")()
	d.addSubscription(d.db, userID, streamerID, endpoint)
}

// addSubscription does nothing for a streamer the chat subscribes to,
// as a merge can leave a chat both subscribed and pending on one.
func (d *Database) addSubscription(q querier, userID UserID, streamerID int, endpoint string) {
	_, err := q.Exec(context.Background(), `
		insert into subscriptions (chat_id, streamer_id)
		values ((select id from chats where user_id = $1 and endpoint = $3), $2)
		on conflict (chat_id, streamer_id) do nothing
		/*name='add_subscription'*/`,
		int64(userID), streamerID, endpoint)
	checkErr(err)
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
		insert into pending_subscriptions (chat_id, nickname, referral, command, reply_seq)
		values ((select id from chats where user_id = $1 and endpoint = $3), $2, $4, $5, $6)
		/*name='add_pending_subscription'*/`,
		int64(userID),
		nickname,
		endpoint,
		referral,
		nullableCommand(command),
		replySeq)
}

// SetShowImages updates the show_images setting of the user's chat on endpoint
func (d *Database) SetShowImages(endpoint string, userID UserID, showImages bool) {
	d.MustExec(`
		update chats set show_images = $1 where user_id = $2 and endpoint = $3
		/*name='set_show_images'*/`,
		showImages, int64(userID), endpoint)
}

// SetOfflineNotifications updates the offline_notifications setting of the user's chat on endpoint
func (d *Database) SetOfflineNotifications(endpoint string, userID UserID, offlineNotifications bool) {
	d.MustExec(`
		update chats set offline_notifications = $1 where user_id = $2 and endpoint = $3
		/*name='set_offline_notifications'*/`,
		offlineNotifications, int64(userID), endpoint)
}

// SetShowSubject updates the show_subject setting of the user's chat on endpoint
func (d *Database) SetShowSubject(endpoint string, userID UserID, showSubject bool) {
	d.MustExec(`
		update chats set show_subject = $1 where user_id = $2 and endpoint = $3
		/*name='set_show_subject'*/`,
		showSubject, int64(userID), endpoint)
}

// SetSilentMessages updates the silent_messages setting of the user's chat on endpoint
func (d *Database) SetSilentMessages(endpoint string, userID UserID, silentMessages bool) {
	d.MustExec(`
		update chats set silent_messages = $1 where user_id = $2 and endpoint = $3
		/*name='set_silent_messages'*/`,
		silentMessages, int64(userID), endpoint)
}

// SetMemberSubscriptions updates the member_subscriptions setting of the user's chat on endpoint
func (d *Database) SetMemberSubscriptions(endpoint string, userID UserID, memberSubscriptions bool) {
	d.MustExec(`
		update chats set member_subscriptions = $1 where user_id = $2 and endpoint = $3
		/*name='set_member_subscriptions'*/`,
		memberSubscriptions, int64(userID), endpoint)
}

// SetTimezone updates a user's IANA zone name, empty to clear it back to unset.
func (d *Database) SetTimezone(userID UserID, timezone string) {
	if timezone == "" {
		d.MustExec(`
			update users set timezone = null where id = $1
			/*name='clear_timezone'*/`,
			int64(userID))
		return
	}
	d.MustExec(`
		update users set timezone = $1 where id = $2
		/*name='set_timezone'*/`,
		timezone, int64(userID))
}

// SetAffiliateParams updates the custom affiliate params of the user's chat on endpoint,
// empty to clear.
func (d *Database) SetAffiliateParams(endpoint string, userID UserID, params map[string]string) {
	if len(params) == 0 {
		d.MustExec(`
			update chats set affiliate_params = null where user_id = $1 and endpoint = $2
			/*name='clear_affiliate_params'*/`,
			int64(userID), endpoint)
		return
	}
	d.MustExec(`
		update chats set affiliate_params = $1 where user_id = $2 and endpoint = $3
		/*name='set_affiliate_params'*/`,
		params, int64(userID), endpoint)
}

// UpdateMemberCount updates the member_count for a user
func (d *Database) UpdateMemberCount(userID UserID, memberCount int) {
	d.MustExec(`
		update users set member_count = $1 where id = $2
		/*name='update_member_count'*/`,
		memberCount, int64(userID))
}

// RemoveSubscription deletes a specific subscription
// and any pending subscription
func (d *Database) RemoveSubscription(userID UserID, nickname string, endpoint string) {
	defer d.Measure("db: remove subscription")()
	tx, err := d.begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		delete from subscriptions sub
		using chats c, streamers s
		where c.id = sub.chat_id and sub.streamer_id = s.id
		and c.user_id = $1 and s.nickname = $2 and c.endpoint = $3
		/*name='remove_subscription'*/`,
		int64(userID), nickname, endpoint)
	checkErr(err)
	d.deletePendingSubscription(tx, PendingSubscription{UserID: userID, Nickname: nickname, Endpoint: endpoint})
	checkErr(tx.Commit(context.Background()))
}

// RemoveAllSubscriptions deletes all subscriptions and pending subscriptions
// for a user and endpoint
func (d *Database) RemoveAllSubscriptions(userID UserID, endpoint string) {
	defer d.Measure("db: remove all subscriptions")()
	tx, err := d.begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		delete from subscriptions sub
		using chats c
		where c.id = sub.chat_id and c.user_id = $1 and c.endpoint = $2
		/*name='remove_all_subscriptions'*/`,
		int64(userID), endpoint)
	checkErr(err)
	_, err = tx.Exec(context.Background(), `
		delete from pending_subscriptions ps
		using chats c
		where c.id = ps.chat_id and c.user_id = $1 and c.endpoint = $2
		/*name='remove_all_subscriptions_pending'*/`,
		int64(userID), endpoint)
	checkErr(err)
	checkErr(tx.Commit(context.Background()))
}

// AddFeedback stores user feedback
func (d *Database) AddFeedback(endpoint string, userID UserID, text string, timestamp int) {
	d.MustExec(`
		insert into feedback (chat_id, text, timestamp)
		values ((select id from chats where endpoint = $1 and user_id = $2), $3, $4)
		/*name='add_feedback'*/`,
		endpoint,
		int64(userID),
		text,
		timestamp)
}

// BlacklistUser sets the blacklist flag for a user
func (d *Database) BlacklistUser(userID UserID) {
	d.MustExec(`
		update users set blacklist = true where id = $1
		/*name='blacklist_user'*/`,
		int64(userID))
}

// CreditReferrer adds the bonus to the referrer's limit on endpoint
func (d *Database) CreditReferrer(endpoint string, userID UserID, bonus int) {
	d.MustExec(`
		update chats set max_subs = max_subs + $3
		where user_id = $1 and endpoint = $2
		/*name='credit_referrer'*/`,
		int64(userID), endpoint, bonus)
}

// AddReferralEvent adds a referral event
func (d *Database) AddReferralEvent(timestamp int, referrerUserID *UserID, refereeUserID UserID, streamerID *int) {
	var referrer *int64
	if referrerUserID != nil {
		r := int64(*referrerUserID)
		referrer = &r
	}
	d.MustExec(`
		insert into referral_events (timestamp, referrer_user_id, referee_user_id, streamer_id)
		values ($1, $2, $3, $4)
		/*name='add_referral_event'*/`,
		timestamp,
		referrer,
		int64(refereeUserID),
		streamerID)
}

// ReferredUsersCount returns how many users the referrer's links brought
func (d *Database) ReferredUsersCount(referrerUserID UserID) int {
	return d.MustInt(`
		select count(*) from referral_events where referrer_user_id = $1
		/*name='referred_users_count'*/`,
		int64(referrerUserID))
}

// SetReferralID gives the user's chat on endpoint its referral id
func (d *Database) SetReferralID(endpoint string, userID UserID, referralID string) {
	d.MustExec(`
		update chats set referral_id = $3 where user_id = $1 and endpoint = $2
		/*name='set_referral_id'*/`,
		int64(userID), endpoint, referralID)
}

// LogReceivedMessage records a received message.
// The caller resolves the user id, so logging never hides a create.
// An untracked command stores a null,
// leaving the row counted with no name against it.
func (d *Database) LogReceivedMessage(timestamp int, endpoint string, userID UserID, command string) {
	d.MustExec(`
		insert into received_message_log (timestamp, chat_id, command)
		values ($1, (select id from chats where endpoint = $2 and user_id = $3), $4)
		/*name='log_received_message'*/`,
		timestamp,
		endpoint,
		int64(userID),
		nullableCommand(command))
}

// LogPerformance logs performance data for queries and updates
func (d *Database) LogPerformance(timestamp int, kind PerformanceLogKind, durationMs int, data map[string]any) {
	jsonData, err := json.Marshal(data)
	checkErr(err)
	d.MustExec(`
		insert into performance_log (timestamp, kind, duration_ms, data) values ($1, $2, $3, $4)
		/*name='log_performance'*/`,
		timestamp,
		kind,
		durationMs,
		jsonData)
}

// ShortOfflineRule says an offline period whose end is After seconds old goes
// when shorter than ShorterThan seconds, and Rewrite rewrites a chunk once the rule is done with it
type ShortOfflineRule struct {
	After       int
	ShorterThan int
	Rewrite     bool
}

// ErrLockBusy says status_changes_lock is held, so an editor skips its turn
var ErrLockBusy = errors.New("status_changes_lock is busy")

// ErrMigrating says a migration holds the migration lock
var ErrMigrating = errors.New("a migration is at work")

// lockStatusChanges takes status_changes_lock in exclusive mode for tx,
// before anything on status_changes, as docs/status-changes.md's Invariant asks of an editor,
// and the migration lock shared, so a migration waits for the commit.
// A held lock is ErrLockBusy, a held migration lock ErrMigrating.
func (d *Database) lockStatusChanges(ctx context.Context, tx pgx.Tx) error {
	// Exclusive, not access exclusive: pg_dump's access share lock must not conflict
	_, err := tx.Exec(ctx, `
		lock table status_changes_lock in exclusive mode nowait
		/*name='lock_status_changes_lock_nowait'*/`)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable {
		return ErrLockBusy
	}
	if err != nil {
		return err
	}
	var locked bool
	err = tx.QueryRow(ctx, `
		select pg_try_advisory_xact_lock_shared($1)
		/*name='lock_migrations_shared_xact_try'*/`,
		migrationLock).Scan(&locked)
	if err != nil {
		return err
	}
	if !locked {
		return ErrMigrating
	}
	return nil
}

// statusChangesChunk is a status_changes chunk: its name, quoted for a statement, and its range
type statusChangesChunk struct {
	name       string
	start, end int
}

// oldestStatusChange returns the oldest row's timestamp, or fallback with no rows
func (d *Database) oldestStatusChange(ctx context.Context, fallback int) (int, error) {
	var oldest int
	err := d.db.QueryRow(ctx, `
		select coalesce(min(timestamp), $1::integer) from status_changes
		/*name='oldest_status_change'*/`,
		fallback).Scan(&oldest)
	return oldest, err
}

// compactionCoverage is a status_changes_lock row: the stretch a rule examined,
// docs/status-changes.md's $c$
type compactionCoverage struct{ shorterThan, begin, end int }

// takeCompactionCoverages deletes the coverages in tx and returns them with the latest run's time
func (d *Database) takeCompactionCoverages(ctx context.Context, tx pgx.Tx) ([]compactionCoverage, int, error) {
	rows, err := tx.Query(ctx, `
		delete from status_changes_lock
		returning shorter_than, begin_timestamp, end_timestamp, run_timestamp
		/*name='take_compaction_coverages'*/`)
	if err != nil {
		return nil, 0, err
	}
	lastRun := 0
	coverages, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (compactionCoverage, error) {
		var c compactionCoverage
		var run int
		err := row.Scan(&c.shorterThan, &c.begin, &c.end, &run)
		lastRun = max(lastRun, run)
		return c, err
	})
	return coverages, lastRun, err
}

// deleteShortOfflinePeriods deletes in tx the offline periods shorter than shorterThan
// that end in [begin, end), docs/status-changes.md's Compaction, and returns the rows deleted
func (d *Database) deleteShortOfflinePeriods(
	ctx context.Context,
	tx pgx.Tx,
	shorterThan int,
	begin int,
	end int,
) (int64, error) {
	tag, err := tx.Exec(ctx, `
		with ends as (
			select streamer_id, timestamp
			from status_changes
			where timestamp >= $1::integer and timestamp < $2::integer
			and status = 2 and prev_status = 1
		),
		-- Materialized, or the planner joins the delete target in first and probes per pair
		periods as materialized (
			select e.streamer_id, a.timestamp as start_timestamp, e.timestamp as end_timestamp
			from ends e
			-- The band's own bounds too, so the probe is planned onto the band's chunks
			join lateral (
				select timestamp, prev_status
				from status_changes
				where streamer_id = e.streamer_id
				and timestamp > e.timestamp - $3::integer and timestamp < e.timestamp
				and timestamp > $1::integer - $3::integer and timestamp < $2::integer
				order by timestamp desc
				limit 1
			) a on true
			-- The lateral with offset 0 keeps each id a primary key lookup, made only for a probe's hit
			join lateral (
				select st.prev_unconfirmed_timestamp
				from streamers st
				where st.id = e.streamer_id and a.prev_status = 2
				offset 0
			) s on true
			where e.timestamp < s.prev_unconfirmed_timestamp
		)
		delete from status_changes sc
		using periods p
		where sc.streamer_id = p.streamer_id
		and sc.timestamp in (p.start_timestamp, p.end_timestamp)
		and sc.timestamp > $1::integer - $3::integer and sc.timestamp < $2::integer
		/*name='delete_short_offline_periods'*/`,
		pgx.QueryExecModeExec, begin, end, shorterThan)
	return tag.RowsAffected(), err
}

// storeCompactionCoverage stores in tx a rule's coverage, from base to end, as of the run's time
func (d *Database) storeCompactionCoverage(
	ctx context.Context,
	tx pgx.Tx,
	rule ShortOfflineRule,
	base int,
	end int,
	run int,
) error {
	_, err := tx.Exec(ctx, `
		insert into status_changes_lock (after, shorter_than, begin_timestamp, end_timestamp, run_timestamp)
		values ($1, $2, $3, $4, $5)
		/*name='store_compaction_coverage'*/`,
		rule.After, rule.ShorterThan, base, end, run)
	return err
}

// statusChangesChunks returns status_changes' chunks in tx, oldest first
func (d *Database) statusChangesChunks(ctx context.Context, tx pgx.Tx) ([]statusChangesChunk, error) {
	// The chunks come from the catalog, as a chunk interval change shapes new chunks only
	rows, err := tx.Query(ctx, `
		select format('%I.%I', chunk_schema, chunk_name), range_start_integer, range_end_integer
		from timescaledb_information.chunks
		where hypertable_name = 'status_changes'
		order by range_start_integer
		/*name='status_changes_chunks'*/`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (statusChangesChunk, error) {
		var c statusChangesChunk
		err := row.Scan(&c.name, &c.start, &c.end)
		return c, err
	})
}

// statusChangesVacuum is a chunk to vacuum, and whether to rewrite it first
type statusChangesVacuum struct {
	chunk   string
	rewrite bool
}

// queueStatusChangesVacuums queues vacuums in tx, each chunk at most once,
// so a queued vacuum survives the process as the deletes do.
// A chunk queued already keeps its place, rewritten if either asks.
func (d *Database) queueStatusChangesVacuums(ctx context.Context, tx pgx.Tx, vacuums []statusChangesVacuum) error {
	if len(vacuums) == 0 {
		return nil
	}
	chunks := make([]string, len(vacuums))
	rewrites := make([]bool, len(vacuums))
	for i, v := range vacuums {
		chunks[i], rewrites[i] = v.chunk, v.rewrite
	}
	_, err := tx.Exec(ctx, `
		insert into status_changes_vacuum_queue (chunk, rewrite)
		select * from unnest($1::text[], $2::boolean[])
		on conflict (chunk) do update
		set rewrite = status_changes_vacuum_queue.rewrite or excluded.rewrite
		/*name='queue_status_changes_vacuums'*/`,
		chunks, rewrites)
	return err
}

// vacuumNextStatusChangesChunk vacuums the chunk queued first, dequeues it and returns 1,
// rewriting it first when it was queued for a rewrite and rewrites says a rule still rewrites.
// It returns 0 with an empty queue, while a migration keeps the chunk queued,
// for a chunk another session holds, which goes to the back of the queue,
// and for a chunk gone since it was queued, which it dequeues.
// A chunk left with dead rows the vacuum could not remove goes to the back of the queue instead,
// as their pages stay out of the visibility map.
// It goes back for a plain vacuum, as a rewrite has given its space back already.
func (d *Database) vacuumNextStatusChangesChunk(ctx context.Context, rewrites bool) (int, error) {
	var id int64
	var name string
	var rewrite bool
	// Flushes the stats too: deletes just made on this connection, counted after the vacuum,
	// would requeue a clean chunk
	err := d.db.QueryRow(ctx, `
		select id, chunk, rewrite
		from status_changes_vacuum_queue, pg_stat_force_next_flush()
		order by id
		limit 1
		/*name='first_queued_vacuum_flushing_stats'*/`).Scan(&id, &name, &rewrite)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	done, err := d.vacuumStatusChangesChunk(ctx, id, name, rewrite && rewrites)
	// So the chunks behind it go on meanwhile
	if errors.Is(err, errChunkHeld) {
		_, err := d.db.Exec(ctx, `
			update status_changes_vacuum_queue
			set id = default
			where id = $1
			/*name='requeue_held_vacuum'*/`,
			id)
		return 0, err
	}
	if !done || err != nil {
		return 0, err
	}
	// The vacuum leaves the rows it could not remove in the chunk's dead tuple count
	var exists bool
	if err := d.db.QueryRow(ctx, `
		with dead as (
			select coalesce((select n_dead_tup from pg_stat_all_tables where relid = to_regclass($2)), 0) > 0 as remain
		),
		requeued as (
			update status_changes_vacuum_queue
			set id = default
			where id = $1 and (select remain from dead)
		),
		dequeued as (
			delete from status_changes_vacuum_queue
			where id = $1 and not (select remain from dead)
		)
		select to_regclass($2) is not null
		/*name='dequeue_or_requeue_vacuum'*/`,
		id, name).Scan(&exists); err != nil || !exists {
		return 0, err
	}
	return 1, nil
}

// queuedStatusChangesVacuums returns how many chunks wait for a vacuum
func (d *Database) queuedStatusChangesVacuums(ctx context.Context) (int, error) {
	var n int
	err := d.db.QueryRow(ctx, `
		select count(*)
		from status_changes_vacuum_queue
		/*name='count_queued_vacuums'*/`).Scan(&n)
	return n, err
}

// vacuumStatusChangesChunk vacuums queued chunk id, name, rewriting it first with rewrite,
// under the migration lock, shared, so a migration waits for one chunk at most.
// It returns false while a migration holds the lock,
// errChunkHeld while another session holds the chunk, and true for a chunk gone since it was named.
func (d *Database) vacuumStatusChangesChunk(ctx context.Context, id int64, name string, rewrite bool) (bool, error) {
	var locked bool
	if err := d.db.QueryRow(ctx, `
		select pg_try_advisory_lock_shared($1)
		/*name='lock_migrations_shared_try'*/`,
		migrationLock).Scan(&locked); err != nil || !locked {
		return false, err
	}
	_, err := d.db.PgConn().Exec(
		ctx,
		"set lock_timeout = "+strconv.FormatInt(d.vacuumLockTimeout.Milliseconds(), 10)).ReadAll()
	if err == nil && rewrite {
		err = d.rewriteStatusChangesChunk(ctx, id, name)
	}
	if err == nil {
		// The simple protocol, as vacuum takes no parameters; the name comes quoted from the catalog.
		// Index pass forced: PostgreSQL skips it under 2% of dead pages, which stay not all-visible.
		// After a rewrite, it fills the visibility map, which the rewrite leaves empty.
		_, err = d.db.PgConn().Exec(ctx, `
			vacuum (index_cleanup on) `+name+`
			/*name='vacuum_status_changes_chunk'*/`).ReadAll()
	}
	_, resetErr := d.db.PgConn().Exec(context.Background(), `set lock_timeout = default`).ReadAll()
	// A lock left held would block every migration for as long as this session lives,
	// so a failed unlock is an error, which ends the daemon and the session with it
	_, unlockErr := d.db.Exec(context.Background(), `
		select pg_advisory_unlock_shared($1)
		/*name='unlock_migrations_shared'*/`,
		migrationLock)
	if resetErr != nil || unlockErr != nil {
		return false, errors.Join(err, resetErr, unlockErr)
	}
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UndefinedTable:
		return true, nil
	case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable:
		return false, errChunkHeld
	}
	return err == nil, err
}

// errChunkHeld says another session held a chunk that a vacuum was to take
var errChunkHeld = errors.New("another session holds the chunk")

// rewriteStatusChangesChunk rewrites queued chunk id, name, with vacuum full,
// releasing its free space, then clears its flag, so a restart doesn't rewrite it again.
// It skips a chunk another session holds, returning errChunkHeld:
// waiting, it would queue the chunk's readers behind it and fail pg_dump's nowait locks.
// A full disk leaves the chunk to the plain vacuum.
func (d *Database) rewriteStatusChangesChunk(ctx context.Context, id int64, name string) error {
	fileNode := func() (node int64, err error) {
		err = d.db.QueryRow(ctx, `
			select pg_relation_filenode($1::text::regclass)::bigint
			/*name='status_changes_chunk_file_node'*/`,
			name).Scan(&node)
		return node, err
	}
	before, err := fileNode()
	if err != nil {
		return err
	}
	_, err = d.db.PgConn().Exec(ctx, `
		vacuum (full, skip_locked) `+name+`
		/*name='rewrite_status_changes_chunk'*/`).ReadAll()
	if err == nil {
		// A rewrite gives the chunk a new file, and skip_locked returns quietly without one
		after, err := fileNode()
		if err != nil {
			return err
		}
		if after == before {
			return errChunkHeld
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.DiskFull {
		lerr("no room to rewrite a chunk, vacuuming it instead: chunk = %s", name)
	} else if err != nil {
		return err
	}
	_, err = d.db.Exec(ctx, `
		update status_changes_vacuum_queue
		set rewrite = false
		where id = $1
		/*name='clear_queued_rewrite'*/`,
		id)
	return err
}

// UnconfirmedSubs returns the nicknames of pending subscriptions not yet being checked
func (d *Database) UnconfirmedSubs() map[string]bool {
	nicknames := map[string]bool{}
	var nickname string
	d.MustQuery(`
		select nickname from pending_subscriptions where not checking
		/*name='unconfirmed_subs'*/`,
		nil,
		ScanTo{&nickname},
		func() { nicknames[nickname] = true })
	return nicknames
}

// CheckingSubs returns the pending subscriptions under check for nicknames, keyed by nickname
func (d *Database) CheckingSubs(nicknames []string) map[string][]PendingSubscription {
	subs := map[string][]PendingSubscription{}
	var iter PendingSubscription
	d.MustQuery(`
		select
			c.endpoint, ps.nickname, c.user_id, u.tg_chat_id,
			ps.referral, coalesce(ps.command, ''), ps.reply_seq
		from pending_subscriptions ps
		join chats c on c.id = ps.chat_id
		join users u on u.id = c.user_id
		where ps.checking and ps.nickname = any($1)
		/*name='checking_subs'*/`,
		QueryParams{nicknames},
		ScanTo{
			&iter.Endpoint, &iter.Nickname, &iter.UserID, &iter.ChatID,
			&iter.Referral, &iter.Command, &iter.ReplySeq},
		func() { subs[iter.Nickname] = append(subs[iter.Nickname], iter) })
	return subs
}

// MarkUnconfirmedAsChecking marks pending subscriptions as checking
func (d *Database) MarkUnconfirmedAsChecking() {
	d.MustExec(`
		update pending_subscriptions set checking = true where not checking
		/*name='mark_unconfirmed_as_checking'*/`)
}

// ResetCheckingToUnconfirmed resets checking pending subscriptions back to not checking
func (d *Database) ResetCheckingToUnconfirmed() {
	d.MustExec(`
		update pending_subscriptions set checking = false where checking
		/*name='reset_checking_to_unconfirmed'*/`)
}

// ResetNotificationSending resets all sending notifications to not sending
func (d *Database) ResetNotificationSending() {
	d.MustExec(`
		update notification_queue set sending = 0 where sending <> 0
		/*name='reset_notification_sending'*/`)
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
	imageMode ImageMode,
) {
	d.MustExec(`
		insert into sent_message_log (
			timestamp, chat_id, result, priority, latency, kind, command, reply_seq, image_mode)
		values ($1, (select id from chats where user_id = $2 and endpoint = $4), $3, $5, $6, $7, $8, $9, $10)
		/*name='log_sent_message'*/`,
		timestamp,
		int64(userID),
		result,
		endpoint,
		priority,
		latency,
		kind,
		nullableCommand(command),
		replySeq,
		imageMode)
}

// SkippedSend names one dice-skipped recipient for LogDiceSkippedSends.
type SkippedSend struct {
	UserID   UserID
	Endpoint string
}

// LogDiceSkippedSends logs a batch of dice-skipped sends in one statement.
// Rows mirror LogSentMessage, with zero latency and no command.
// The left join leaves a missing chat's chat_id null, so the not-null constraint fails the insert.
func (d *Database) LogDiceSkippedSends(
	timestamp int,
	result int,
	priority Priority,
	kind PacketKind,
	skips []SkippedSend,
) {
	ids := make([]int64, len(skips))
	endpoints := make([]string, len(skips))
	for i, s := range skips {
		ids[i] = int64(s.UserID)
		endpoints[i] = s.Endpoint
	}
	d.MustExec(`
		insert into sent_message_log (timestamp, chat_id, result, priority, latency, kind, command, reply_seq)
		select $1, c.id, $2, $3, 0, $4, null, 0
		from unnest($5::bigint[], $6::text[]) as t(user_id, endpoint)
		left join chats c on c.user_id = t.user_id and c.endpoint = t.endpoint
		/*name='log_dice_skipped_sends'*/`,
		timestamp, result, priority, kind, ids, endpoints)
}

// DeleteNotification deletes a notification by ID
func (d *Database) DeleteNotification(id int) {
	d.MustExec(`
		delete from notification_queue where id = $1
		/*name='delete_notification'*/`,
		id)
}

// RequeueNotification puts a notification back in the queue,
// so a later fetch picks it up again.
func (d *Database) RequeueNotification(id int) {
	d.MustExec(`
		update notification_queue set sending = 0 where id = $1
		/*name='requeue_notification'*/`,
		id)
}

// IncrementReports counts each notification against its chat.
func (d *Database) IncrementReports(nots []Notification) {
	ids := make([]int64, len(nots))
	endpoints := make([]string, len(nots))
	for i, n := range nots {
		ids[i] = int64(n.UserID)
		endpoints[i] = n.Endpoint
	}
	d.MustExec(`
		update chats c
		set reports = c.reports + k.n::int
		from (
			select user_id, endpoint, count(*) as n
			from unnest($1::bigint[], $2::text[]) as t(user_id, endpoint)
			group by user_id, endpoint
		) k
		where c.user_id = k.user_id and c.endpoint = k.endpoint
		/*name='increment_reports'*/`,
		ids, endpoints)
}

// ConfirmStatusChanges finds streamers needing confirmation and updates them.
// Returns the confirmed status changes with previous status.
func (d *Database) ConfirmStatusChanges(
	now int,
	onlineSeconds int,
	offlineSeconds int,
) []ConfirmedStatusChange {
	// ix_streamers_status_mismatch serves the select but not an update, so the update goes by id
	var result []ConfirmedStatusChange
	var ids, statuses []int
	var change ConfirmedStatusChange
	d.MustQuery(`
		select id, nickname, unconfirmed_status, confirmed_status
		from streamers
		where confirmed_status != unconfirmed_status
		and (
			(unconfirmed_status = 2 and $1 - unconfirmed_timestamp >= $2)
			or (unconfirmed_status = 1 and $1 - unconfirmed_timestamp >= $3)
			or unconfirmed_status = 0
		)
		/*name='confirm_status_changes_collect'*/`,
		QueryParams{now, onlineSeconds, offlineSeconds},
		ScanTo{&change.StreamerID, &change.Nickname, &change.Status, &change.PrevStatus},
		func() {
			change.Timestamp = now
			result = append(result, change)
			ids = append(ids, change.StreamerID)
			statuses = append(statuses, int(change.Status))
		})
	if len(ids) > 0 {
		d.MustExec(`
			update streamers s
			set confirmed_status = c.status
			from unnest($1::int[], $2::int[]) as c(id, status)
			where s.id = c.id
			/*name='confirm_status_changes_update'*/`,
			ids, statuses)
	}
	return result
}
