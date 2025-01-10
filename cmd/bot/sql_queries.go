package main

import (
	"time"

	"github.com/bcmk/siren/lib/cmdlib"
)

var insertStatusChange = "insert into status_changes (model_id, status, timestamp) values ($1, $2, $3)"
var updateLastStatusChange = `
	insert into last_status_changes (model_id, status, timestamp)
	values ($1, $2, $3)
	on conflict(model_id) do update set status = excluded.status, timestamp = excluded.timestamp`
var updateModelStatus = `
	insert into models (model_id, status)
	values ($1, $2)
	on conflict(model_id) do update set status = excluded.status`
var storeNotification = `
	insert into notification_queue (endpoint, chat_id, model_id, status, time_diff, image_url, social, priority, sound, kind)
	values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

func (w *worker) newNotifications() []notification {
	var nots []notification
	var iter notification
	w.mustQuery(
		`select id, endpoint, chat_id, model_id, status, time_diff, image_url, social, priority, sound, kind
		from notification_queue
		where sending = 0
		order by id`,
		nil,
		scanTo{
			&iter.id,
			&iter.endpoint,
			&iter.chatID,
			&iter.modelID,
			&iter.status,
			&iter.timeDiff,
			&iter.imageURL,
			&iter.social,
			&iter.priority,
			&iter.sound,
			&iter.kind,
		},
		func() { nots = append(nots, iter) },
	)
	w.mustExec("update notification_queue set sending = 1 where sending = 0")
	return nots
}

func (w *worker) storeNotifications(nots []notification) {
	tx, err := w.db.Begin()
	checkErr(err)
	stmt, err := tx.Prepare(storeNotification)
	checkErr(err)
	for _, n := range nots {
		w.mustExecPrepared(stmt, n.endpoint, n.chatID, n.modelID, n.status, n.timeDiff, n.imageURL, n.social, n.priority, n.sound, n.kind)
	}
	checkErr(stmt.Close())
	checkErr(tx.Commit())
}

func (w *worker) lastSeenInfo(modelID string) (begin int, end int, prevStatus cmdlib.StatusKind) {
	var maybeEnd *int
	var maybePrevStatus *cmdlib.StatusKind
	if !w.maybeRecord(`
		select timestamp, "end", prev_status from (
			select
				*,
				lead(timestamp) over (order by timestamp) as "end",
				lag(status) over (order by timestamp) as prev_status
			from status_changes
			where model_id = $1)
		where status = $2
		order by timestamp desc limit 1`,
		queryParams{modelID, cmdlib.StatusOnline},
		scanTo{&begin, &maybeEnd, &maybePrevStatus}) {

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

func (w *worker) modelsToPoll() (models []string) {
	var modelID string
	w.mustQuery(`
		select distinct model_id from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where block.block is null or block.block < $1
		order by model_id`,
		queryParams{w.cfg.BlockThreshold},
		scanTo{&modelID},
		func() { models = append(models, modelID) })
	return
}

func (w *worker) usersForModels() (users map[string][]user, endpoints map[string][]string) {
	users = map[string][]user{}
	endpoints = make(map[string][]string)
	var modelID string
	var chatID int64
	var endpoint string
	var offlineNotifications bool
	var showImages bool
	w.mustQuery(`
		select signals.model_id, signals.chat_id, signals.endpoint, users.offline_notifications, users.show_images
		from signals
		join users on users.chat_id = signals.chat_id`,
		queryParams{},
		scanTo{&modelID, &chatID, &endpoint, &offlineNotifications, &showImages},
		func() {
			users[modelID] = append(users[modelID], user{chatID: chatID, offlineNotifications: offlineNotifications, showImages: showImages})
			endpoints[modelID] = append(endpoints[modelID], endpoint)
		})
	return
}

func (w *worker) broadcastChats(endpoint string) (chats []int64) {
	var chatID int64
	w.mustQuery(
		`select distinct chat_id from signals where endpoint = $1 order by chat_id`,
		queryParams{endpoint},
		scanTo{&chatID},
		func() { chats = append(chats, chatID) })
	return
}

func (w *worker) modelsForChat(endpoint string, chatID int64) (models []string) {
	var modelID string
	w.mustQuery(
		`select model_id from signals where chat_id = $1 and endpoint = $2 order by model_id`,
		queryParams{chatID, endpoint},
		scanTo{&modelID},
		func() { models = append(models, modelID) })
	return
}

func (w *worker) statusesForChat(endpoint string, chatID int64) (statuses []model) {
	var iter model
	w.mustQuery(`
		select models.model_id, models.status
		from models
		join signals on signals.model_id=models.model_id
		where signals.chat_id = $1 and signals.endpoint = $2
		order by models.model_id`,
		queryParams{chatID, endpoint},
		scanTo{&iter.modelID, &iter.status},
		func() { statuses = append(statuses, iter) })
	return
}

func (w *worker) subscriptionExists(endpoint string, chatID int64, modelID string) bool {
	count := w.mustInt("select count(*) from signals where chat_id = $1 and model_id = $2 and endpoint = $3", chatID, modelID, endpoint)
	return count != 0
}

func (w *worker) subscriptionsNumber(endpoint string, chatID int64) int {
	return w.mustInt("select count(*) from signals where chat_id = $1 and endpoint = $2", chatID, endpoint)
}

func (w *worker) user(chatID int64) (user user, found bool) {
	found = w.maybeRecord("select chat_id, max_models, reports, blacklist, show_images, offline_notifications from users where chat_id = $1",
		queryParams{chatID},
		scanTo{&user.chatID, &user.maxModels, &user.reports, &user.blacklist, &user.showImages, &user.offlineNotifications})
	return
}

func (w *worker) addUser(chatID int64) {
	w.mustExec(`
		insert into users (chat_id, max_models)
		values ($1, $2)
		on conflict(chat_id) do nothing`,
		chatID, w.cfg.MaxModels)
}

func (w *worker) maybeModel(modelID string) *model {
	var result model
	if w.maybeRecord("select model_id, status from models where model_id = $1", queryParams{modelID}, scanTo{&result.modelID, &result.status}) {
		return &result
	}
	return nil
}

func (w *worker) changesFromTo(modelID string, from int, to int) []statusChange {
	var changes []statusChange
	first := true
	var change statusChange
	var firstStatus *cmdlib.StatusKind
	var firstTimestamp *int
	w.mustQuery(`
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
		queryParams{modelID, from},
		scanTo{&change.status, &change.timestamp, &firstStatus, &firstTimestamp},
		func() {
			if first && firstStatus != nil && firstTimestamp != nil {
				changes = append(changes, statusChange{status: *firstStatus, timestamp: *firstTimestamp})
				first = false
			}
			changes = append(changes, change)
		})
	changes = append(changes, statusChange{timestamp: to})
	return changes
}

func (w *worker) setLimit(chatID int64, maxModels int) {
	w.mustExec(`
		insert into users (chat_id, max_models) values ($1, $2)
		on conflict(chat_id) do update set max_models=excluded.max_models`,
		chatID,
		maxModels)
}

func (w *worker) userReferralsCount() int {
	return w.mustInt("select coalesce(sum(referred_users), 0) from referrals")
}

func (w *worker) modelReferralsCount() int {
	return w.mustInt("select coalesce(sum(referred_users), 0) from models")
}

func (w *worker) reports() int {
	return w.mustInt("select coalesce(sum(reports), 0) from users")
}

func (w *worker) interactionsByResultToday(endpoint string) map[int]int {
	timestamp := time.Now().Add(time.Hour * -24).Unix()
	results := map[int]int{}
	var result int
	var count int
	w.mustQuery(
		"select result, count(*) from interactions where endpoint = $1 and timestamp > $2 group by result",
		queryParams{endpoint, timestamp},
		scanTo{&result, &count},
		func() { results[result] = count })
	return results
}

func (w *worker) interactionsByKindToday(endpoint string) map[packetKind]int {
	timestamp := time.Now().Add(time.Hour * -24).Unix()
	results := map[packetKind]int{}
	var kind packetKind
	var count int
	w.mustQuery(
		"select kind, count(*) from interactions where endpoint = $1 and timestamp > $2 and result=200 group by kind",
		queryParams{endpoint, timestamp},
		scanTo{&kind, &count},
		func() { results[kind] = count })
	return results
}

func (w *worker) usersCount(endpoint string) int {
	return w.mustInt("select count(distinct chat_id) from signals where endpoint = $1", endpoint)
}

func (w *worker) groupsCount(endpoint string) int {
	return w.mustInt("select count(distinct chat_id) from signals where endpoint = $2 and chat_id < 0", endpoint)
}

func (w *worker) activeUsersOnEndpointCount(endpoint string) int {
	return w.mustInt(`
		select count(distinct signals.chat_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0) and signals.endpoint = $1`,
		endpoint)
}

func (w *worker) activeUsersTotalCount() int {
	return w.mustInt(`
		select count(distinct signals.chat_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0)`)
}

func (w *worker) modelsCount(endpoint string) int {
	return w.mustInt("select count(distinct model_id) from signals where endpoint = $1", endpoint)
}

func (w *worker) modelsToPollOnEndpointCount(endpoint string) int {
	return w.mustInt(`
		select count(distinct signals.model_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < $1) and signals.endpoint = $2`,
		w.cfg.BlockThreshold,
		endpoint)
}

func (w *worker) modelsToPollTotalCount() int {
	return w.mustInt(`
		select count(distinct signals.model_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < $1)`,
		w.cfg.BlockThreshold)
}

func (w *worker) statusChangesCount() int {
	return w.mustInt("select coalesce(max(_rowid_), 0) from status_changes")
}

func (w *worker) heavyUsersCount(endpoint string) int {
	return w.mustInt(`
		select count(*) from (
			select 1 from signals
			left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
			where (block.block is null or block.block = 0) and signals.endpoint = $1
			group by signals.chat_id
			having count(*) >= $2);`,
		endpoint,
		w.cfg.MaxModels-w.cfg.HeavyUserRemainder)
}

func (w *worker) confirmSub(sub subscription) {
	w.mustExec(`
		insert into models (model_id)
		values ($1)
		on conflict(model_id) do nothing`,
		sub.modelID)
	w.mustExec("update signals set confirmed=1 where endpoint = $1 and chat_id = $2 and model_id = $3", sub.endpoint, sub.chatID, sub.modelID)
}

func (w *worker) denySub(sub subscription) {
	w.mustExec("delete from signals where endpoint = $1 and chat_id = $2 and model_id = $3", sub.endpoint, sub.chatID, sub.modelID)
}

func (w *worker) queryLastStatusChanges() map[string]statusChange {
	statusChanges := map[string]statusChange{}
	var statusChange statusChange
	w.mustQuery(
		`select model_id, status, timestamp from last_status_changes`,
		nil,
		scanTo{&statusChange.modelID, &statusChange.status, &statusChange.timestamp},
		func() { statusChanges[statusChange.modelID] = statusChange })
	return statusChanges
}

func (w *worker) queryConfirmedModels() (map[string]bool, map[string]bool) {
	statuses := map[string]bool{}
	specialModels := map[string]bool{}
	var modelID string
	w.mustQuery("select model_id from models where status = $1", queryParams{cmdlib.StatusOnline}, scanTo{&modelID}, func() { statuses[modelID] = true })
	w.mustQuery("select model_id from models where special=1", nil, scanTo{&modelID}, func() { specialModels[modelID] = true })
	return statuses, specialModels
}

func (w *worker) referralID(chatID int64) *string {
	var referralID string
	if !w.maybeRecord("select referral_id from referrals where chat_id = $1", queryParams{chatID}, scanTo{&referralID}) {
		return nil
	}
	return &referralID
}

func (w *worker) chatForReferralID(referralID string) *int64 {
	var chatID int64
	if !w.maybeRecord("select chat_id from referrals where referral_id = $1", queryParams{referralID}, scanTo{&chatID}) {
		return nil
	}
	return &chatID
}

func (w *worker) incrementBlock(endpoint string, chatID int64) {
	w.mustExec(`
		insert into block (endpoint, chat_id, block) values ($1, $2, 1)
		on conflict(chat_id, endpoint) do update set block = block.block + 1`,
		endpoint,
		chatID)
}

func (w *worker) resetBlock(endpoint string, chatID int64) {
	w.mustExec("update block set block=0 where endpoint = $1 and chat_id = $2", endpoint, chatID)
}
