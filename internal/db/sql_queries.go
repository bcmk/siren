package db

import (
	"context"

	"github.com/bcmk/siren/lib/cmdlib"
	"github.com/jackc/pgx/v5"
)

// NewNotifications retuns new notifications
func (d *Database) NewNotifications() []Notification {
	var nots []Notification
	var iter Notification
	d.MustQuery(
		`select id, endpoint, chat_id, channel_id, status, time_diff, image_url, social, priority, sound, kind
		from notification_queue
		where sending = 0
		order by id`,
		nil,
		ScanTo{
			&iter.ID,
			&iter.Endpoint,
			&iter.ChatID,
			&iter.ChannelID,
			&iter.Status,
			&iter.TimeDiff,
			&iter.ImageURL,
			&iter.Social,
			&iter.Priority,
			&iter.Sound,
			&iter.Kind,
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
					channel_id,
					status,
					time_diff,
					image_url,
					social,
					priority,
					sound,
					kind
				)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			`,
			n.Endpoint, n.ChatID, n.ChannelID, n.Status, n.TimeDiff, n.ImageURL, n.Social, n.Priority, n.Sound, n.Kind,
		)
	}
	d.SendBatch(batch)

}

// UsersForChannels returns users subscribed to a particular channel
func (d *Database) UsersForChannels(channelIDs []string) (users map[string][]User, endpoints map[string][]string) {
	users = map[string][]User{}
	endpoints = make(map[string][]string)
	var channelID string
	var chatID int64
	var endpoint string
	var offlineNotifications bool
	var showImages bool
	d.MustQuery(`
		select signals.channel_id, signals.chat_id, signals.endpoint, users.offline_notifications, users.show_images
		from signals
		join users on users.chat_id = signals.chat_id
		where signals.channel_id = any($1)`,
		QueryParams{channelIDs},
		ScanTo{&channelID, &chatID, &endpoint, &offlineNotifications, &showImages},
		func() {
			users[channelID] = append(users[channelID], User{ChatID: chatID, OfflineNotifications: offlineNotifications, ShowImages: showImages})
			endpoints[channelID] = append(endpoints[channelID], endpoint)
		})
	return
}

// BroadcastChats returns chats having subscriptions
func (d *Database) BroadcastChats(endpoint string) (chats []int64) {
	var chatID int64
	d.MustQuery(
		`select distinct chat_id from signals where endpoint = $1 order by chat_id`,
		QueryParams{endpoint},
		ScanTo{&chatID},
		func() { chats = append(chats, chatID) })
	return
}

// ChannelsForChat returns channels that particular chat is subscribed to
func (d *Database) ChannelsForChat(endpoint string, chatID int64) (channels []string) {
	var channelID string
	d.MustQuery(
		`select channel_id from signals where chat_id = $1 and endpoint = $2 order by channel_id`,
		QueryParams{chatID, endpoint},
		ScanTo{&channelID},
		func() { channels = append(channels, channelID) })
	return
}

// ConfirmedStatusesForChat returns channels that particular chat is subscribed to and their statuses
func (d *Database) ConfirmedStatusesForChat(endpoint string, chatID int64) (statuses []Channel) {
	var iter Channel
	d.MustQuery(`
		select channels.channel_id, channels.confirmed_status
		from channels
		join signals on signals.channel_id = channels.channel_id
		where signals.chat_id = $1 and signals.endpoint = $2
		order by channels.channel_id`,
		QueryParams{chatID, endpoint},
		ScanTo{&iter.ChannelID, &iter.ConfirmedStatus},
		func() { statuses = append(statuses, iter) })
	return
}

// UnconfirmedStatusesForChat returns channels with their unconfirmed statuses from channels table
func (d *Database) UnconfirmedStatusesForChat(endpoint string, chatID int64) (statuses []Channel) {
	var iter Channel
	d.MustQuery(`
		select
			s.channel_id,
			coalesce(m.unconfirmed_status, 0),
			coalesce(m.unconfirmed_timestamp, 0),
			coalesce(m.prev_unconfirmed_status, 0),
			coalesce(m.prev_unconfirmed_timestamp, 0)
		from signals s
		left join channels m on m.channel_id = s.channel_id
		where s.chat_id = $1 and s.endpoint = $2
		order by s.channel_id`,
		QueryParams{chatID, endpoint},
		ScanTo{
			&iter.ChannelID,
			&iter.UnconfirmedStatus,
			&iter.UnconfirmedTimestamp,
			&iter.PrevUnconfirmedStatus,
			&iter.PrevUnconfirmedTimestamp,
		},
		func() { statuses = append(statuses, iter) })
	return
}

// SubscriptionExists checks if subscription exists
func (d *Database) SubscriptionExists(endpoint string, chatID int64, channelID string) bool {
	count := d.MustInt("select count(*) from signals where chat_id = $1 and channel_id = $2 and endpoint = $3", chatID, channelID, endpoint)
	return count != 0
}

// SubscriptionsNumber return the number of subscriptions of a particular chat
func (d *Database) SubscriptionsNumber(endpoint string, chatID int64) int {
	return d.MustInt("select count(*) from signals where chat_id = $1 and endpoint = $2", chatID, endpoint)
}

// User queries a user with particular ID
func (d *Database) User(chatID int64) (user User, found bool) {
	found = d.MaybeRecord("select chat_id, max_channels, reports, blacklist, show_images, offline_notifications from users where chat_id = $1",
		QueryParams{chatID},
		ScanTo{&user.ChatID, &user.MaxChannels, &user.Reports, &user.Blacklist, &user.ShowImages, &user.OfflineNotifications})
	return
}

// AddUser inserts a user
func (d *Database) AddUser(chatID int64, maxChannels int) {
	d.MustExec(`
		insert into users (chat_id, max_channels)
		values ($1, $2)
		on conflict(chat_id) do nothing`,
		chatID,
		maxChannels)
}

// MaybeChannel returns a channel if exists
func (d *Database) MaybeChannel(channelID string) *Channel {
	var result Channel
	if d.MaybeRecord(
		`select
			channel_id,
			confirmed_status,
			unconfirmed_status,
			unconfirmed_timestamp,
			prev_unconfirmed_status,
			prev_unconfirmed_timestamp
		from channels
		where channel_id = $1`,
		QueryParams{channelID},
		ScanTo{
			&result.ChannelID,
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

// ChangesFromToForChannels returns all changes for multiple channels in specified period
func (d *Database) ChangesFromToForChannels(channelIDs []string, from int, to int) map[string][]StatusChange {
	result := make(map[string][]StatusChange)
	beforeRangeAdded := make(map[string]bool)
	var change StatusChange
	var beforeRangeStatus *cmdlib.StatusKind
	var beforeRangeTimestamp *int
	d.MustQuery(`
		select channel_id, status, timestamp, before_range_status, before_range_timestamp
		from (
			select
				*,
				lag(status) over w as before_range_status,
				lag(timestamp) over w as before_range_timestamp
			from status_changes
			where channel_id = any($1)
			window w as (partition by channel_id order by timestamp)
		) sub
		where timestamp >= $2
		order by channel_id, timestamp`,
		QueryParams{channelIDs, from},
		ScanTo{&change.ChannelID, &change.Status, &change.Timestamp, &beforeRangeStatus, &beforeRangeTimestamp},
		func() {
			if !beforeRangeAdded[change.ChannelID] && beforeRangeStatus != nil && beforeRangeTimestamp != nil {
				result[change.ChannelID] = append(result[change.ChannelID], StatusChange{
					ChannelID: change.ChannelID,
					Status:    *beforeRangeStatus,
					Timestamp: *beforeRangeTimestamp,
				})
				beforeRangeAdded[change.ChannelID] = true
			}
			result[change.ChannelID] = append(result[change.ChannelID], change)
		})
	for _, channelID := range channelIDs {
		result[channelID] = append(result[channelID], StatusChange{ChannelID: channelID, Timestamp: to})
	}
	return result
}

// SetLimit updates a particular user with its max channels limit
func (d *Database) SetLimit(chatID int64, maxChannels int) {
	d.MustExec(`
		insert into users (chat_id, max_channels) values ($1, $2)
		on conflict(chat_id) do update set max_channels=excluded.max_channels`,
		chatID,
		maxChannels)
}

// ConfirmSub confirms subscription
func (d *Database) ConfirmSub(sub Subscription) {
	d.MustExec(`
		insert into channels (channel_id)
		values ($1)
		on conflict(channel_id) do nothing`,
		sub.ChannelID)
	d.MustExec("update signals set confirmed=1 where endpoint = $1 and chat_id = $2 and channel_id = $3", sub.Endpoint, sub.ChatID, sub.ChannelID)
}

// DenySub denies subscription
func (d *Database) DenySub(sub Subscription) {
	d.MustExec("delete from signals where endpoint = $1 and chat_id = $2 and channel_id = $3", sub.Endpoint, sub.ChatID, sub.ChannelID)
}

// QueryLastStatusChangesForChannels returns all known latest status changes for specific channels
func (d *Database) QueryLastStatusChangesForChannels(channelIDs []string) map[string]StatusChange {
	statusChanges := map[string]StatusChange{}
	var statusChange StatusChange
	d.MustQuery(
		`
			select channel_id, unconfirmed_status, unconfirmed_timestamp
			from channels
			where channel_id = any($1) and unconfirmed_timestamp > 0
		`,
		QueryParams{channelIDs},
		ScanTo{&statusChange.ChannelID, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.ChannelID] = statusChange })
	return statusChanges
}

// SubscribedChannels returns all confirmed subscribed channels
func (d *Database) SubscribedChannels() map[string]bool {
	channels := map[string]bool{}
	var channelID string
	d.MustQuery(
		`select distinct channel_id from signals where confirmed = 1`,
		nil,
		ScanTo{&channelID},
		func() { channels[channelID] = true })
	return channels
}

// QueryLastSubscriptionStatuses returns latest statuses for subscriptions
func (d *Database) QueryLastSubscriptionStatuses() map[string]cmdlib.StatusKind {
	statuses := map[string]cmdlib.StatusKind{}
	var channelID string
	var status cmdlib.StatusKind
	d.MustQuery(
		`
			select s.channel_id, coalesce(m.unconfirmed_status, $1) as status
			from (select distinct channel_id from signals where confirmed = 1) s
			left join channels m on m.channel_id = s.channel_id
		`,
		QueryParams{cmdlib.StatusUnknown},
		ScanTo{&channelID, &status},
		func() { statuses[channelID] = status })
	return statuses
}

// QueryLastOnlineChannels queries latest online channels
func (d *Database) QueryLastOnlineChannels() map[string]bool {
	onlineChannels := map[string]bool{}
	var channelID string
	d.MustQuery(
		`select channel_id from channels where unconfirmed_status = $1`,
		QueryParams{cmdlib.StatusOnline},
		ScanTo{&channelID},
		func() { onlineChannels[channelID] = true })
	return onlineChannels
}

// SetKnownUnsubscribedToUnknown sets known unsubscribed channels to unknown status
// and returns the affected channel IDs.
func (d *Database) SetKnownUnsubscribedToUnknown(now int) []string {
	var channels []string
	var channelID string
	d.MustQuery(
		`
			update channels set
				prev_unconfirmed_status = unconfirmed_status,
				prev_unconfirmed_timestamp = unconfirmed_timestamp,
				unconfirmed_status = 0,
				unconfirmed_timestamp = $1
			where unconfirmed_status != 0
			and channel_id not in (select distinct channel_id from signals where confirmed = 1)
			returning channel_id
		`,
		QueryParams{now},
		ScanTo{&channelID},
		func() { channels = append(channels, channelID) })
	return channels
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

// InsertStatusChanges inserts status changes using a bulk method
func (d *Database) InsertStatusChanges(changedStatuses []StatusChange) {
	statusDone := d.Measure("db: insert unconfirmed status updates")
	defer statusDone()

	if len(changedStatuses) == 0 {
		return
	}

	tx, err := d.Begin()
	checkErr(err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Use CopyFrom for fast bulk insert into status_changes
	rows := make([][]interface{}, len(changedStatuses))
	for i, sc := range changedStatuses {
		rows[i] = []interface{}{sc.ChannelID, sc.Status, sc.Timestamp}
	}
	_, err = tx.CopyFrom(
		context.Background(),
		pgx.Identifier{"status_changes"},
		[]string{"channel_id", "status", "timestamp"},
		pgx.CopyFromRows(rows),
	)
	checkErr(err)

	// Use batch for channels upserts within the same transaction
	batch := &pgx.Batch{}
	for _, sc := range changedStatuses {
		batch.Queue(
			`
				insert into channels (channel_id, unconfirmed_status, unconfirmed_timestamp)
				values ($1, $2, $3)
				on conflict(channel_id) do update set
					prev_unconfirmed_status = channels.unconfirmed_status,
					prev_unconfirmed_timestamp = channels.unconfirmed_timestamp,
					unconfirmed_status = excluded.unconfirmed_status,
					unconfirmed_timestamp = excluded.unconfirmed_timestamp
			`,
			sc.ChannelID, sc.Status, sc.Timestamp)
	}
	br := tx.SendBatch(context.Background(), batch)
	checkErr(br.Close())

	checkErr(tx.Commit(context.Background()))
}

// ConfirmStatusChanges finds channels needing confirmation and updates them.
// Returns the confirmed status changes.
func (d *Database) ConfirmStatusChanges(now int, onlineSeconds int, offlineSeconds int) []StatusChange {
	// We use two queries instead of one because
	// PostgreSQL uses ix_channels_status_mismatch partial index for select but not for update.
	done := d.Measure("db: confirm status changes")
	defer done()
	var result []StatusChange
	var change StatusChange
	d.MustQuery(
		`
			select channel_id, unconfirmed_status
			from channels
			where confirmed_status != unconfirmed_status
			and (
				(unconfirmed_status = 2 and $1 - unconfirmed_timestamp >= $2)
				or (unconfirmed_status = 1 and $1 - unconfirmed_timestamp >= $3)
				or unconfirmed_status = 0
			)
		`,
		QueryParams{now, onlineSeconds, offlineSeconds},
		ScanTo{&change.ChannelID, &change.Status},
		func() {
			change.Timestamp = now
			result = append(result, change)
		})
	if len(result) > 0 {
		channelIDs := make([]string, len(result))
		statuses := make([]int, len(result))
		for i, c := range result {
			channelIDs[i] = c.ChannelID
			statuses[i] = int(c.Status)
		}
		d.MustExec(
			`
				update channels c
				set confirmed_status = v.status
				from unnest($1::text[], $2::int[]) as v(channel_id, status)
				where c.channel_id = v.channel_id
			`,
			channelIDs,
			statuses)
	}
	return result
}
