package db

import (
	"context"
	"time"

	"github.com/bcmk/siren/lib/cmdlib"
	"github.com/jackc/pgx/v5"
)

// InsertStatusChange is a SQL query inserting a status change
var InsertStatusChange = "insert into status_changes (model_id, status, timestamp) values ($1, $2, $3)"

// UpdateLastStatusChange is a SQL query updating last status change
var UpdateLastStatusChange = `
	insert into last_status_changes (model_id, status, timestamp)
	values ($1, $2, $3)
	on conflict(model_id) do update set status = excluded.status, timestamp = excluded.timestamp`

// UpdateModelStatus is a SQL query updating model status
var UpdateModelStatus = `
	insert into models (model_id, status)
	values ($1, $2)
	on conflict(model_id) do update set status = excluded.status`

// StoreNotification is a SQL query inserting a notification
var StoreNotification = `
	insert into notification_queue (endpoint, chat_id, model_id, status, time_diff, image_url, social, priority, sound, kind)
	values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

// NewNotifications retuns new notifications
func (d *Database) NewNotifications() []Notification {
	var nots []Notification
	var iter Notification
	d.MustQuery(
		`select id, endpoint, chat_id, model_id, status, time_diff, image_url, social, priority, sound, kind
		from notification_queue
		where sending = 0
		order by id`,
		nil,
		ScanTo{
			&iter.ID,
			&iter.Endpoint,
			&iter.ChatID,
			&iter.ModelID,
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
	tx, err := d.Begin()
	checkErr(err)
	stmt, err := tx.Prepare(context.Background(), "store_notifications", StoreNotification)
	checkErr(err)
	for _, n := range nots {
		d.MustExecPrepared(stmt, n.Endpoint, n.ChatID, n.ModelID, n.Status, n.TimeDiff, n.ImageURL, n.Social, n.Priority, n.Sound, n.Kind)
	}
	checkErr(tx.Commit(context.Background()))
}

// LastSeenInfo returns last seen info for a model
func (d *Database) LastSeenInfo(modelID string) (begin int, end int, prevStatus cmdlib.StatusKind) {
	var maybeEnd *int
	var maybePrevStatus *cmdlib.StatusKind
	if !d.MaybeRecord(`
		select timestamp, "end", prev_status from (
			select
				*,
				lead(timestamp) over (order by timestamp) as "end",
				lag(status) over (order by timestamp) as prev_status
			from status_changes
			where model_id = $1)
		where status = $2
		order by timestamp desc limit 1`,
		QueryParams{modelID, cmdlib.StatusOnline},
		ScanTo{&begin, &maybeEnd, &maybePrevStatus}) {

		return 0, 0, cmdlib.StatusUnknown
	}
	if maybeEnd == nil {
		zero := 0
		maybeEnd = &zero
	}
	if maybePrevStatus == nil {
		unknown := cmdlib.StatusUnknown
		maybePrevStatus = &unknown
	}
	return begin, *maybeEnd, *maybePrevStatus
}

// ModelsToPoll returns models to poll
func (d *Database) ModelsToPoll(blockThreshold int) (models []string) {
	var modelID string
	d.MustQuery(`
		select distinct model_id from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where block.block is null or block.block < $1
		order by model_id`,
		QueryParams{blockThreshold},
		ScanTo{&modelID},
		func() { models = append(models, modelID) })
	return
}

// UsersForModels returns users subscribed to a particular model
func (d *Database) UsersForModels() (users map[string][]User, endpoints map[string][]string) {
	users = map[string][]User{}
	endpoints = make(map[string][]string)
	var modelID string
	var chatID int64
	var endpoint string
	var offlineNotifications bool
	var showImages bool
	d.MustQuery(`
		select signals.model_id, signals.chat_id, signals.endpoint, users.offline_notifications, users.show_images
		from signals
		join users on users.chat_id = signals.chat_id`,
		QueryParams{},
		ScanTo{&modelID, &chatID, &endpoint, &offlineNotifications, &showImages},
		func() {
			users[modelID] = append(users[modelID], User{ChatID: chatID, OfflineNotifications: offlineNotifications, ShowImages: showImages})
			endpoints[modelID] = append(endpoints[modelID], endpoint)
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

// ModelsForChat returns models that particular chat is subscribed to
func (d *Database) ModelsForChat(endpoint string, chatID int64) (models []string) {
	var modelID string
	d.MustQuery(
		`select model_id from signals where chat_id = $1 and endpoint = $2 order by model_id`,
		QueryParams{chatID, endpoint},
		ScanTo{&modelID},
		func() { models = append(models, modelID) })
	return
}

// StatusesForChat returns models that particular chat is subscribed to and their statuses
func (d *Database) StatusesForChat(endpoint string, chatID int64) (statuses []Model) {
	var iter Model
	d.MustQuery(`
		select models.model_id, models.status
		from models
		join signals on signals.model_id=models.model_id
		where signals.chat_id = $1 and signals.endpoint = $2
		order by models.model_id`,
		QueryParams{chatID, endpoint},
		ScanTo{&iter.ModelID, &iter.Status},
		func() { statuses = append(statuses, iter) })
	return
}

// SubscriptionExists checks if subscription exists
func (d *Database) SubscriptionExists(endpoint string, chatID int64, modelID string) bool {
	count := d.MustInt("select count(*) from signals where chat_id = $1 and model_id = $2 and endpoint = $3", chatID, modelID, endpoint)
	return count != 0
}

// SubscriptionsNumber return the number of subscriptions of a particular chat
func (d *Database) SubscriptionsNumber(endpoint string, chatID int64) int {
	return d.MustInt("select count(*) from signals where chat_id = $1 and endpoint = $2", chatID, endpoint)
}

// User queries a user with particular ID
func (d *Database) User(chatID int64) (user User, found bool) {
	found = d.MaybeRecord("select chat_id, max_models, reports, blacklist, show_images, offline_notifications from users where chat_id = $1",
		QueryParams{chatID},
		ScanTo{&user.ChatID, &user.MaxModels, &user.Reports, &user.Blacklist, &user.ShowImages, &user.OfflineNotifications})
	return
}

// AddUser inserts a user
func (d *Database) AddUser(chatID int64, maxModels int) {
	d.MustExec(`
		insert into users (chat_id, max_models)
		values ($1, $2)
		on conflict(chat_id) do nothing`,
		chatID,
		maxModels)
}

// MaybeModel returns a model if exists
func (d *Database) MaybeModel(modelID string) *Model {
	var result Model
	if d.MaybeRecord("select model_id, status from models where model_id = $1", QueryParams{modelID}, ScanTo{&result.ModelID, &result.Status}) {
		return &result
	}
	return nil
}

// ChangesFromTo returns all changes for a particular model in specified period
func (d *Database) ChangesFromTo(modelID string, from int, to int) []StatusChange {
	var changes []StatusChange
	first := true
	var change StatusChange
	var firstStatus *cmdlib.StatusKind
	var firstTimestamp *int
	d.MustQuery(`
		select status, timestamp, prev_status, prev_timestamp
		from(
			select
				*,
				lag(status) over (order by timestamp) as prev_status,
				lag(timestamp) over (order by timestamp) as prev_timestamp
			from status_changes
			where model_id = $1)
		where timestamp >= $2
		order by timestamp`,
		QueryParams{modelID, from},
		ScanTo{&change.Status, &change.Timestamp, &firstStatus, &firstTimestamp},
		func() {
			if first && firstStatus != nil && firstTimestamp != nil {
				changes = append(changes, StatusChange{Status: *firstStatus, Timestamp: *firstTimestamp})
				first = false
			}
			changes = append(changes, change)
		})
	changes = append(changes, StatusChange{Timestamp: to})
	return changes
}

// SetLimit updates a particular user with its max models limit
func (d *Database) SetLimit(chatID int64, maxModels int) {
	d.MustExec(`
		insert into users (chat_id, max_models) values ($1, $2)
		on conflict(chat_id) do update set max_models=excluded.max_models`,
		chatID,
		maxModels)
}

// UserReferralsCount returns a count of referrals of a particular user
func (d *Database) UserReferralsCount() int {
	return d.MustInt("select coalesce(sum(referred_users), 0) from referrals")
}

// ModelReferralsCount returns a count of referrals of a particular model
func (d *Database) ModelReferralsCount() int {
	return d.MustInt("select coalesce(sum(referred_users), 0) from models")
}

// Reports returns the total number of reports
func (d *Database) Reports() int {
	return d.MustInt("select coalesce(sum(reports), 0) from users")
}

// InteractionsByResultToday return the number of interactions grouped by result today
func (d *Database) InteractionsByResultToday(endpoint string) map[int]int {
	timestamp := time.Now().Add(time.Hour * -24).Unix()
	results := map[int]int{}
	var result int
	var count int
	d.MustQuery(
		"select result, count(*) from interactions where endpoint = $1 and timestamp > $2 group by result",
		QueryParams{endpoint, timestamp},
		ScanTo{&result, &count},
		func() { results[result] = count })
	return results
}

// InteractionsByKindToday return the number of interactions grouped by kind today
func (d *Database) InteractionsByKindToday(endpoint string) map[PacketKind]int {
	timestamp := time.Now().Add(time.Hour * -24).Unix()
	results := map[PacketKind]int{}
	var kind PacketKind
	var count int
	d.MustQuery(
		"select kind, count(*) from interactions where endpoint = $1 and timestamp > $2 and result=200 group by kind",
		QueryParams{endpoint, timestamp},
		ScanTo{&kind, &count},
		func() { results[kind] = count })
	return results
}

// UsersCount returns the count of users for particular endpoint
func (d *Database) UsersCount(endpoint string) int {
	return d.MustInt("select count(distinct chat_id) from signals where endpoint = $1", endpoint)
}

// GroupsCount returns the count of groups for particular endpoint
func (d *Database) GroupsCount(endpoint string) int {
	return d.MustInt("select count(distinct chat_id) from signals where endpoint = $1 and chat_id < 0", endpoint)
}

// ActiveUsersOnEndpointCount returns the number of not blocked users for particular endpoint
func (d *Database) ActiveUsersOnEndpointCount(endpoint string) int {
	return d.MustInt(`
		select count(distinct signals.chat_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0) and signals.endpoint = $1`,
		endpoint)
}

// ActiveUsersTotalCount returns the total number of not blocked users
func (d *Database) ActiveUsersTotalCount() int {
	return d.MustInt(`
		select count(distinct signals.chat_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0)`)
}

// ModelsCount returns the total number of known streamers
func (d *Database) ModelsCount(endpoint string) int {
	return d.MustInt("select count(distinct model_id) from signals where endpoint = $1", endpoint)
}

// ModelsToPollOnEndpointCount returns what it says
func (d *Database) ModelsToPollOnEndpointCount(endpoint string, blockThreshold int) int {
	return d.MustInt(`
		select count(distinct signals.model_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < $1) and signals.endpoint = $2`,
		blockThreshold,
		endpoint)
}

// ModelsToPollTotalCount returns what it says
func (d *Database) ModelsToPollTotalCount(blockThreshold int) int {
	return d.MustInt(`
		select count(distinct signals.model_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < $1)`,
		blockThreshold)
}

// StatusChangesCount returns the total number of stored status changes
func (d *Database) StatusChangesCount() int {
	return d.MustInt("select reltuples::bigint as estimate from pg_class where relname = 'status_changes'")
}

// HeavyUsersCount returns the number of heavy users for the endpoint
func (d *Database) HeavyUsersCount(endpoint string, maxModels int, heavyUserRemainder int) int {
	return d.MustInt(`
		select count(*) from (
			select 1 from signals
			left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
			where (block.block is null or block.block = 0) and signals.endpoint = $1
			group by signals.chat_id
			having count(*) >= $2);`,
		endpoint,
		maxModels-heavyUserRemainder)
}

// ConfirmSub confirms subscription
func (d *Database) ConfirmSub(sub Subscription) {
	d.MustExec(`
		insert into models (model_id)
		values ($1)
		on conflict(model_id) do nothing`,
		sub.ModelID)
	d.MustExec("update signals set confirmed=1 where endpoint = $1 and chat_id = $2 and model_id = $3", sub.Endpoint, sub.ChatID, sub.ModelID)
}

// DenySub denies subscription
func (d *Database) DenySub(sub Subscription) {
	d.MustExec("delete from signals where endpoint = $1 and chat_id = $2 and model_id = $3", sub.Endpoint, sub.ChatID, sub.ModelID)
}

// QueryLastStatusChanges returns all known latest status changes
func (d *Database) QueryLastStatusChanges() map[string]StatusChange {
	statusChanges := map[string]StatusChange{}
	var statusChange StatusChange
	d.MustQuery(
		`select model_id, status, timestamp from last_status_changes`,
		nil,
		ScanTo{&statusChange.ModelID, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.ModelID] = statusChange })
	return statusChanges
}

// QueryConfirmedModels returns all known confirmed models
func (d *Database) QueryConfirmedModels() (map[string]bool, map[string]bool) {
	statuses := map[string]bool{}
	specialModels := map[string]bool{}
	var modelID string
	d.MustQuery("select model_id from models where status = $1", QueryParams{cmdlib.StatusOnline}, ScanTo{&modelID}, func() { statuses[modelID] = true })
	d.MustQuery("select model_id from models where special = true", nil, ScanTo{&modelID}, func() { specialModels[modelID] = true })
	return statuses, specialModels
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
		insert into block (endpoint, chat_id, block) values ($1, $2, 1)
		on conflict(chat_id, endpoint) do update set block = block.block + 1`,
		endpoint,
		chatID)
}

// ResetBlock resets blocking count for particular chat ID
func (d *Database) ResetBlock(endpoint string, chatID int64) {
	d.MustExec("update block set block=0 where endpoint = $1 and chat_id = $2", endpoint, chatID)
}

// InsertStatusChanges inserts status changes using a bulk method
func (d *Database) InsertStatusChanges(tx pgx.Tx, statusChanges []StatusChange) {
	statusChangeRows := [][]interface{}{}
	for _, statusChange := range statusChanges {
		statusChangeRows = append(statusChangeRows, []interface{}{
			statusChange.ModelID,
			statusChange.Status,
			statusChange.Timestamp,
		})
	}
	_, err := tx.CopyFrom(
		context.Background(),
		[]string{"status_changes"},
		[]string{"model_id", "status", "timestamp"},
		pgx.CopyFromRows(statusChangeRows),
	)
	checkErr(err)
}
