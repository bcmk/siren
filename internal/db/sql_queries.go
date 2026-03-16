package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/jackc/pgx/v5"
)

// NewNotifications returns new notifications
func (d *Database) NewNotifications() []Notification {
	var nots []Notification
	var iter Notification
	d.MustQuery(
		`
		select
			n.id, n.endpoint, n.chat_id, n.nickname, n.status, n.time_diff, n.image_url,
			n.viewers, n.show_kind, n.social, n.priority, n.sound, n.kind, n.subject,
			u.silent_messages
		from notification_queue n
		join users u on u.chat_id = n.chat_id
		where n.sending = 0
		order by n.id
		`,
		nil,
		ScanTo{
			&iter.ID,
			&iter.Endpoint,
			&iter.ChatID,
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
		batch.Queue(
			`
				insert into notification_queue (
					endpoint,
					chat_id,
					nickname,
					status,
					time_diff,
					image_url,
					viewers,
					show_kind,
					social,
					priority,
					sound,
					kind,
					subject
				)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			`,
			n.Endpoint, n.ChatID, n.Nickname, n.Status, n.TimeDiff, n.ImageURL, n.Viewers, n.ShowKind, n.Social, n.Priority, n.Sound, n.Kind, n.Subject,
		)
	}
	d.SendBatch(batch)
}

// UsersForStreamers returns users subscribed to a particular streamer
func (d *Database) UsersForStreamers(nicknames []string) (users map[string][]User, endpoints map[string][]string) {
	users = map[string][]User{}
	endpoints = make(map[string][]string)
	var nickname string
	var chatID int64
	var endpoint string
	var offlineNotifications bool
	var showImages bool
	var showSubject bool
	d.MustQuery(`
		select
			subscriptions.nickname,
			subscriptions.chat_id,
			subscriptions.endpoint,
			users.offline_notifications,
			users.show_images,
			users.show_subject
		from subscriptions
		join users on users.chat_id = subscriptions.chat_id
		where subscriptions.nickname = any($1)`,
		QueryParams{nicknames},
		ScanTo{&nickname, &chatID, &endpoint, &offlineNotifications, &showImages, &showSubject},
		func() {
			users[nickname] = append(users[nickname], User{
				ChatID:               chatID,
				OfflineNotifications: offlineNotifications,
				ShowImages:           showImages,
				ShowSubject:          showSubject,
			})
			endpoints[nickname] = append(endpoints[nickname], endpoint)
		})
	return
}

// BroadcastChats returns private chats having subscriptions
func (d *Database) BroadcastChats(endpoint string) (chats []int64) {
	var chatID int64
	d.MustQuery(
		`select distinct chat_id from subscriptions where endpoint = $1 and chat_id > 0 order by chat_id`,
		QueryParams{endpoint},
		ScanTo{&chatID},
		func() { chats = append(chats, chatID) })
	return
}

// StreamersForChat returns streamers that particular chat is subscribed to
func (d *Database) StreamersForChat(endpoint string, chatID int64) (streamers []Streamer) {
	var iter Streamer
	d.MustQuery(`
		select s.id, sub.nickname
		from subscriptions sub
		join streamers s on s.nickname = sub.nickname
		where sub.chat_id = $1 and sub.endpoint = $2
		order by sub.nickname`,
		QueryParams{chatID, endpoint},
		ScanTo{&iter.ID, &iter.Nickname},
		func() { streamers = append(streamers, iter) })
	return
}

// UnconfirmedStatusesForChat returns streamers with their unconfirmed statuses from streamers table
func (d *Database) UnconfirmedStatusesForChat(endpoint string, chatID int64) (statuses []Streamer) {
	var iter Streamer
	d.MustQuery(`
		select
			s.nickname,
			coalesce(m.unconfirmed_status, 0),
			coalesce(m.unconfirmed_timestamp, 0),
			coalesce(m.prev_unconfirmed_status, 0),
			coalesce(m.prev_unconfirmed_timestamp, 0)
		from subscriptions s
		left join streamers m on m.nickname = s.nickname
		where s.chat_id = $1 and s.endpoint = $2
		order by s.nickname`,
		QueryParams{chatID, endpoint},
		ScanTo{
			&iter.Nickname,
			&iter.UnconfirmedStatus,
			&iter.UnconfirmedTimestamp,
			&iter.PrevUnconfirmedStatus,
			&iter.PrevUnconfirmedTimestamp,
		},
		func() { statuses = append(statuses, iter) })
	return
}

// SubscriptionExists checks if subscription exists
func (d *Database) SubscriptionExists(endpoint string, chatID int64, nickname string) bool {
	count := d.MustInt("select count(*) from subscriptions where chat_id = $1 and nickname = $2 and endpoint = $3", chatID, nickname, endpoint)
	return count != 0
}

// SubscriptionsNumber return the number of subscriptions of a particular chat
func (d *Database) SubscriptionsNumber(endpoint string, chatID int64) int {
	return d.MustInt("select count(*) from subscriptions where chat_id = $1 and endpoint = $2", chatID, endpoint)
}

// User queries a user with particular ID
func (d *Database) User(chatID int64) (user User, found bool) {
	found = d.MaybeRecord(`
		select
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
		where chat_id = $1
	`,
		QueryParams{chatID},
		ScanTo{
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

// AddUser inserts a user
func (d *Database) AddUser(chatID int64, maxSubs int, now int, chatType string) {
	d.MustExec(`
		insert into users (chat_id, max_subs, created_at, chat_type)
		values ($1, $2, $3, $4)
		on conflict(chat_id) do nothing`,
		chatID,
		maxSubs,
		now,
		chatType)
}

// MaybeStreamer returns a streamer if exists
func (d *Database) MaybeStreamer(nickname string) *Streamer {
	var result Streamer
	if d.MaybeRecord(
		`select
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
	beforeRangeAdded := make(map[int]bool)
	var streamerID int
	var change StatusChange
	var beforeRangeStatus *cmdlib.StatusKind
	var beforeRangeTimestamp *int
	d.MustQuery(`
		select
			sub.streamer_id, sub.status, sub.timestamp,
			sub.before_range_status, sub.before_range_timestamp
		from (
			select
				sc.*,
				lag(sc.status) over w as before_range_status,
				lag(sc.timestamp) over w as before_range_timestamp
			from status_changes sc
			where sc.streamer_id = any($1)
			window w as (partition by sc.streamer_id order by sc.timestamp)
		) sub
		where sub.timestamp >= $2
		order by sub.streamer_id, sub.timestamp`,
		QueryParams{streamerIDs, from},
		ScanTo{&streamerID, &change.Status, &change.Timestamp, &beforeRangeStatus, &beforeRangeTimestamp},
		func() {
			if !beforeRangeAdded[streamerID] && beforeRangeStatus != nil && beforeRangeTimestamp != nil {
				result[streamerID] = append(result[streamerID], StatusChange{
					Status:    *beforeRangeStatus,
					Timestamp: *beforeRangeTimestamp,
				})
				beforeRangeAdded[streamerID] = true
			}
			result[streamerID] = append(result[streamerID], change)
		})
	for _, id := range streamerIDs {
		result[id] = append(result[id], StatusChange{Timestamp: to})
	}
	return result
}

// SetLimit updates a particular user with its max subs limit
func (d *Database) SetLimit(chatID int64, maxSubs int) {
	d.MustExec(`
		insert into users (chat_id, max_subs) values ($1, $2)
		on conflict(chat_id) do update set max_subs=excluded.max_subs`,
		chatID,
		maxSubs)
}

// ConfirmSub confirms subscription
func (d *Database) ConfirmSub(sub Subscription) {
	d.MustExec(`
		insert into streamers (nickname)
		values ($1)
		on conflict(nickname) do nothing`,
		sub.Nickname)
	d.MustExec("update subscriptions set confirmed=1 where endpoint = $1 and chat_id = $2 and nickname = $3", sub.Endpoint, sub.ChatID, sub.Nickname)
}

// DenySub denies subscription
func (d *Database) DenySub(sub Subscription) {
	d.MustExec("delete from subscriptions where endpoint = $1 and chat_id = $2 and nickname = $3", sub.Endpoint, sub.ChatID, sub.Nickname)
}

// UnconfirmedStatusesForStreamers returns unconfirmed statuses for specific streamers
func (d *Database) UnconfirmedStatusesForStreamers(nicknames []string) map[string]StatusChange {
	statusChanges := map[string]StatusChange{}
	var statusChange StatusChange
	d.MustQuery(
		`
			select nickname, unconfirmed_status, unconfirmed_timestamp
			from streamers
			where nickname = any($1)
		`,
		QueryParams{nicknames},
		ScanTo{&statusChange.Nickname, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.Nickname] = statusChange })
	return statusChanges
}

// SubscribedStreamers returns all confirmed subscribed streamers
func (d *Database) SubscribedStreamers() map[string]bool {
	streamers := map[string]bool{}
	var nickname string
	d.MustQuery(
		`select distinct nickname from subscriptions where confirmed = 1`,
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
	d.MustQuery(
		`
			select s.nickname, coalesce(m.unconfirmed_status, $1) as status
			from (select distinct nickname from subscriptions where confirmed = 1) s
			left join streamers m on m.nickname = s.nickname
		`,
		QueryParams{cmdlib.StatusUnknown},
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
				select nickname from streamers
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
				select nickname from streamers
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
				select nickname from streamers
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
				select nickname from streamers
				where max_nonalnum_run(nickname) >= $1
				and nickname like '%' || $2 || '%'
				limit 100
			`,
			pgx.QueryExecModeSimpleProtocol,
			maxNonalnumRun, escaped)
		checkErr(err)
	}

	// Prefix match via btree text_pattern_ops —
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

	// Results are deduplicated and sorted by trigram distance.
	// No planner reset needed — _search_results has no indexes.
	rows, err := tx.Query(context.Background(),
		`
			select nickname from (
				select distinct nickname from _search_results
			) sub
			order by nickname <-> $1
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
func (d *Database) ReferralID(chatID int64) *string {
	var referralID string
	if !d.MaybeRecord("select referral_id from referrals where chat_id = $1", QueryParams{chatID}, ScanTo{&referralID}) {
		return nil
	}
	return &referralID
}

// ChatForReferralID returns a chat ID for particular referral ID
func (d *Database) ChatForReferralID(referralID string) *int64 {
	var chatID int64
	if !d.MaybeRecord("select chat_id from referrals where referral_id = $1", QueryParams{referralID}, ScanTo{&chatID}) {
		return nil
	}
	return &chatID
}

// IncrementBlock increments blocking count for particular chat ID
func (d *Database) IncrementBlock(endpoint string, chatID int64) {
	d.MustExec(`
		insert into block as included (endpoint, chat_id, block) values ($1, $2, 1)
		on conflict(chat_id, endpoint) do update set block = included.block + 1`,
		endpoint,
		chatID)
}

// ResetBlock resets blocking count for particular chat ID
func (d *Database) ResetBlock(endpoint string, chatID int64) {
	d.MustExec("update block set block=0 where endpoint = $1 and chat_id = $2", endpoint, chatID)
}

// InsertStatusChanges inserts status changes using a bulk method.
// Upserts streamers first to obtain integer IDs,
// then bulk inserts into status_changes with those IDs.
func (d *Database) InsertStatusChanges(changedStatuses []StatusChange, timestamp int) {
	statusDone := d.Measure("db: insert unconfirmed status updates")
	defer statusDone()

	if len(changedStatuses) == 0 {
		return
	}

	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Upsert streamers and get integer IDs
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
			returning id, nickname
		`,
		nicknames, statuses, timestamp,
	)
	checkErr(err)
	idMap := make(map[string]int, len(changedStatuses))
	for rows.Next() {
		var id int
		var nickname string
		checkErr(rows.Scan(&id, &nickname))
		idMap[nickname] = id
	}
	checkErr(rows.Err())
	rows.Close()

	// Use CopyFrom for fast bulk insert into status_changes
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

	checkErr(tx.Commit(context.Background()))
}

// AddSubscription inserts a subscription with the given confirmed status
func (d *Database) AddSubscription(chatID int64, nickname string, endpoint string, confirmed int) {
	d.MustExec(
		"insert into subscriptions (chat_id, nickname, endpoint, confirmed) values ($1, $2, $3, $4)",
		chatID,
		nickname,
		endpoint,
		confirmed)
}

// SetShowImages updates the show_images setting for a user
func (d *Database) SetShowImages(chatID int64, showImages bool) {
	d.MustExec("update users set show_images = $1 where chat_id = $2", showImages, chatID)
}

// SetOfflineNotifications updates the offline_notifications setting for a user
func (d *Database) SetOfflineNotifications(chatID int64, offlineNotifications bool) {
	d.MustExec("update users set offline_notifications = $1 where chat_id = $2", offlineNotifications, chatID)
}

// SetShowSubject updates the show_subject setting for a user
func (d *Database) SetShowSubject(chatID int64, showSubject bool) {
	d.MustExec("update users set show_subject = $1 where chat_id = $2", showSubject, chatID)
}

// SetSilentMessages updates the silent_messages setting for a user
func (d *Database) SetSilentMessages(chatID int64, silentMessages bool) {
	d.MustExec("update users set silent_messages = $1 where chat_id = $2", silentMessages, chatID)
}

// UpdateMemberCount updates the member_count for a user
func (d *Database) UpdateMemberCount(chatID int64, memberCount int) {
	d.MustExec("update users set member_count = $1 where chat_id = $2", memberCount, chatID)
}

// UpdateChatType updates the chat_type for a user
// TODO: remove after 2026-05-01,
// we need to backfill chat_type for users who joined before we started storing it.
func (d *Database) UpdateChatType(chatID int64, chatType string) {
	d.MustExec("update users set chat_type = $1 where chat_id = $2", chatType, chatID)
}

// RemoveSubscription deletes a specific subscription
func (d *Database) RemoveSubscription(chatID int64, nickname string, endpoint string) {
	d.MustExec(
		"delete from subscriptions where chat_id = $1 and nickname = $2 and endpoint = $3",
		chatID,
		nickname,
		endpoint)
}

// RemoveAllSubscriptions deletes all subscriptions for a chat and endpoint
func (d *Database) RemoveAllSubscriptions(chatID int64, endpoint string) {
	d.MustExec("delete from subscriptions where chat_id = $1 and endpoint = $2", chatID, endpoint)
}

// AddFeedback stores user feedback
func (d *Database) AddFeedback(endpoint string, chatID int64, text string, timestamp int) {
	d.MustExec(
		"insert into feedback (endpoint, chat_id, text, timestamp) values ($1, $2, $3, $4)",
		endpoint,
		chatID,
		text,
		timestamp)
}

// BlacklistUser sets the blacklist flag for a user
func (d *Database) BlacklistUser(chatID int64) {
	d.MustExec("update users set blacklist=1 where chat_id = $1", chatID)
}

// AddUserWithBonus inserts a user with a specific max_subs value
func (d *Database) AddUserWithBonus(chatID int64, maxSubs int, now int, chatType string) {
	d.MustExec(`
		insert into users (chat_id, max_subs, created_at, chat_type)
		values ($1, $2, $3, $4)
	`, chatID, maxSubs, now, chatType)
}

// AddOrUpdateReferrer inserts or updates a referrer's max_subs
func (d *Database) AddOrUpdateReferrer(chatID int64, maxSubs int, bonus int) {
	d.MustExec(`
		insert into users as included (chat_id, max_subs) values ($1, $2)
		on conflict(chat_id) do update set max_subs=included.max_subs + $3`,
		chatID,
		maxSubs,
		bonus)
}

// IncrementReferredUsers increments the referred_users count for a referral
func (d *Database) IncrementReferredUsers(chatID int64) {
	d.MustExec("update referrals set referred_users=referred_users+1 where chat_id = $1", chatID)
}

// AddReferralEvent adds a referral event
func (d *Database) AddReferralEvent(timestamp int, referrerChatID *int64, followerChatID int64, nickname *string) {
	d.MustExec(
		"insert into referral_events (timestamp, referrer_chat_id, follower_chat_id, nickname) values ($1, $2, $3, $4)",
		timestamp,
		referrerChatID,
		followerChatID,
		nickname)
}

// AddReferral adds a new referral record
func (d *Database) AddReferral(chatID int64, referralID string) {
	d.MustExec("insert into referrals (chat_id, referral_id) values ($1, $2)", chatID, referralID)
}

// LogReceivedMessage logs a received message
func (d *Database) LogReceivedMessage(timestamp int, endpoint string, chatID int64, command *string) {
	d.MustExec(
		"insert into received_message_log (timestamp, endpoint, chat_id, command) values ($1, $2, $3, $4)",
		timestamp,
		endpoint,
		chatID,
		command)
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

// MarkUnconfirmedAsChecking marks unconfirmed subscriptions as checking (0 -> 2)
func (d *Database) MarkUnconfirmedAsChecking() {
	d.MustExec("update subscriptions set confirmed = 2 where confirmed = 0")
}

// ResetCheckingToUnconfirmed resets checking subscriptions back to unconfirmed (2 -> 0)
func (d *Database) ResetCheckingToUnconfirmed() {
	d.MustExec("update subscriptions set confirmed = 0 where confirmed = 2")
}

// ResetNotificationSending resets all sending notifications to not sending
func (d *Database) ResetNotificationSending() {
	d.MustExec("update notification_queue set sending=0")
}

// LogSentMessage logs a sent message
func (d *Database) LogSentMessage(timestamp int, chatID int64, result int, endpoint string, priority int, delay int, kind PacketKind) {
	d.MustExec(
		"insert into sent_message_log (timestamp, chat_id, result, endpoint, priority, delay, kind) values ($1, $2, $3, $4, $5, $6, $7)",
		timestamp,
		chatID,
		result,
		endpoint,
		priority,
		delay,
		kind)
}

// DeleteNotification deletes a notification by ID
func (d *Database) DeleteNotification(id int) {
	d.MustExec("delete from notification_queue where id = $1", id)
}

// IncrementReports increments the reports count for a user
func (d *Database) IncrementReports(chatID int64) {
	d.MustExec("update users set reports=reports+1 where chat_id = $1", chatID)
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
		`select nickname, unconfirmed_status, confirmed_status from to_confirm`,
	)
	checkErr(err)
	defer rows.Close()

	var result []ConfirmedStatusChange
	for rows.Next() {
		var change ConfirmedStatusChange
		checkErr(rows.Scan(&change.Nickname, &change.Status, &change.PrevStatus))
		change.Timestamp = now
		result = append(result, change)
	}
	checkErr(rows.Err())

	checkErr(tx.Commit(context.Background()))
	return result
}
