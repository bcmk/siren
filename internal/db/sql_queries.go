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
	measureDone := d.Measure("db: insert notifications")
	defer measureDone()
	batch := &pgx.Batch{}
	for _, n := range nots {
		batch.Queue(
			`
				insert into notification_queue (
					endpoint,
					chat_id,
					model_id,
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
			n.Endpoint, n.ChatID, n.ModelID, n.Status, n.TimeDiff, n.ImageURL, n.Social, n.Priority, n.Sound, n.Kind,
		)
	}
	d.SendBatch(batch)

}

// UsersForModels returns users subscribed to a particular model
func (d *Database) UsersForModels(modelIDs []string) (users map[string][]User, endpoints map[string][]string) {
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
		join users on users.chat_id = signals.chat_id
		where signals.model_id = any($1)`,
		QueryParams{modelIDs},
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

// ConfirmedStatusesForChat returns models that particular chat is subscribed to and their statuses
func (d *Database) ConfirmedStatusesForChat(endpoint string, chatID int64) (statuses []Model) {
	var iter Model
	d.MustQuery(`
		select models.model_id, models.confirmed_status
		from models
		join signals on signals.model_id = models.model_id
		where signals.chat_id = $1 and signals.endpoint = $2
		order by models.model_id`,
		QueryParams{chatID, endpoint},
		ScanTo{&iter.ModelID, &iter.ConfirmedStatus},
		func() { statuses = append(statuses, iter) })
	return
}

// UnconfirmedStatusesForChat returns models with their unconfirmed statuses from models table
func (d *Database) UnconfirmedStatusesForChat(endpoint string, chatID int64) (statuses []Model) {
	var iter Model
	d.MustQuery(`
		select
			s.model_id,
			coalesce(m.unconfirmed_status, 0),
			coalesce(m.unconfirmed_timestamp, 0),
			coalesce(m.prev_unconfirmed_status, 0),
			coalesce(m.prev_unconfirmed_timestamp, 0)
		from signals s
		left join models m on m.model_id = s.model_id
		where s.chat_id = $1 and s.endpoint = $2
		order by s.model_id`,
		QueryParams{chatID, endpoint},
		ScanTo{
			&iter.ModelID,
			&iter.UnconfirmedStatus,
			&iter.UnconfirmedTimestamp,
			&iter.PrevUnconfirmedStatus,
			&iter.PrevUnconfirmedTimestamp,
		},
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
	if d.MaybeRecord(
		`select
			model_id,
			confirmed_status,
			unconfirmed_status,
			unconfirmed_timestamp,
			prev_unconfirmed_status,
			prev_unconfirmed_timestamp
		from models
		where model_id = $1`,
		QueryParams{modelID},
		ScanTo{
			&result.ModelID,
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

// QueryLastStatusChangesForModels returns all known latest status changes for specific models
func (d *Database) QueryLastStatusChangesForModels(modelIDs []string) map[string]StatusChange {
	statusChanges := map[string]StatusChange{}
	var statusChange StatusChange
	d.MustQuery(
		`
			select model_id, unconfirmed_status, unconfirmed_timestamp
			from models
			where model_id = any($1) and unconfirmed_timestamp > 0
		`,
		QueryParams{modelIDs},
		ScanTo{&statusChange.ModelID, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.ModelID] = statusChange })
	return statusChanges
}

// QueryLastSubscriptionStatuses returns latest statuses for subscriptions
func (d *Database) QueryLastSubscriptionStatuses() map[string]cmdlib.StatusKind {
	statuses := map[string]cmdlib.StatusKind{}
	var modelID string
	var status cmdlib.StatusKind
	d.MustQuery(
		`
			select s.model_id, coalesce(m.unconfirmed_status, $1) as status
			from (select distinct model_id from signals where confirmed = 1) s
			left join models m on m.model_id = s.model_id
		`,
		QueryParams{cmdlib.StatusUnknown},
		ScanTo{&modelID, &status},
		func() { statuses[modelID] = status })
	return statuses
}

// QueryLastOnlineModels queries latest online models
func (d *Database) QueryLastOnlineModels() map[string]bool {
	onlineModels := map[string]bool{}
	var modelID string
	d.MustQuery(
		`select model_id from models where unconfirmed_status = $1`,
		QueryParams{cmdlib.StatusOnline},
		ScanTo{&modelID},
		func() { onlineModels[modelID] = true })
	return onlineModels
}

// QueryConfirmedModels returns all known confirmed models
func (d *Database) QueryConfirmedModels() map[string]bool {
	statuses := map[string]bool{}
	var modelID string
	d.MustQuery(
		"select model_id from models where confirmed_status = $1",
		QueryParams{cmdlib.StatusOnline},
		ScanTo{&modelID},
		func() { statuses[modelID] = true })
	return statuses
}

// QuerySpecialModels returns all known special models
func (d *Database) QuerySpecialModels() map[string]bool {
	specialModels := map[string]bool{}
	var modelID string
	d.MustQuery("select model_id from models where special = true", nil, ScanTo{&modelID}, func() { specialModels[modelID] = true })
	return specialModels
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
		rows[i] = []interface{}{sc.ModelID, sc.Status, sc.Timestamp}
	}
	_, err = tx.CopyFrom(
		context.Background(),
		pgx.Identifier{"status_changes"},
		[]string{"model_id", "status", "timestamp"},
		pgx.CopyFromRows(rows),
	)
	checkErr(err)

	// Use batch for models upserts within the same transaction
	batch := &pgx.Batch{}
	for _, sc := range changedStatuses {
		batch.Queue(
			`
				insert into models (model_id, unconfirmed_status, unconfirmed_timestamp)
				values ($1, $2, $3)
				on conflict(model_id) do update set
					prev_unconfirmed_status = models.unconfirmed_status,
					prev_unconfirmed_timestamp = models.unconfirmed_timestamp,
					unconfirmed_status = excluded.unconfirmed_status,
					unconfirmed_timestamp = excluded.unconfirmed_timestamp
			`,
			sc.ModelID, sc.Status, sc.Timestamp)
	}
	br := tx.SendBatch(context.Background(), batch)
	checkErr(br.Close())

	checkErr(tx.Commit(context.Background()))
}

// InsertConfirmedStatusChanges inserts status changes using a bulk method
func (d *Database) InsertConfirmedStatusChanges(changedStatuses []StatusChange) {
	confirmationsDone := d.Measure("db: insert confirmed status changes")
	defer confirmationsDone()
	batch := &pgx.Batch{}
	for _, i := range changedStatuses {
		batch.Queue(
			`
				insert into models (model_id, confirmed_status)
				values ($1, $2)
				on conflict(model_id) do update set confirmed_status = excluded.confirmed_status
			`,
			i.ModelID,
			i.Status)
	}
	d.SendBatch(batch)
}
