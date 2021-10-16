package main

import (
	"time"

	"github.com/bcmk/siren/lib"
	"github.com/bcmk/siren/payments"
	"github.com/google/uuid"
)

func (w *worker) newNotifications() []notification {
	var nots []notification
	var iter notification
	w.mustQuery(
		`select id, endpoint, chat_id, model_id, status, time_diff, image_url, social, priority, sound, kind
		from notification_queue
		where sending=0
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
	w.mustExec("update notification_queue set sending=1 where sending=0")
	return nots
}

func (w *worker) storeNotifications(nots []notification) {
	tx, err := w.db.Begin()
	checkErr(err)
	stmt, err := tx.Prepare(storeNotification)
	checkErr(err)
	for _, n := range nots {
		w.mustExecPrepared(storeNotification, stmt, n.endpoint, n.chatID, n.modelID, n.status, n.timeDiff, n.imageURL, n.social, n.priority, n.sound, n.kind)
	}
	checkErr(stmt.Close())
	checkErr(tx.Commit())
}

func (w *worker) lastSeenInfo(modelID string, now int) (begin int, end int, prevStatus lib.StatusKind) {
	var maybeEnd *int
	var maybePrevStatus *lib.StatusKind
	if !w.maybeRecord(`
		select timestamp, end, prev_status from (
			select
				*,
				lead(timestamp) over (order by timestamp) as end,
				lag(status) over (order by timestamp) as prev_status
			from status_changes
			where model_id=?)
		where status=?
		order by timestamp desc limit 1`,
		queryParams{modelID, lib.StatusOnline},
		scanTo{&begin, &maybeEnd, &maybePrevStatus}) {

		return 0, 0, lib.StatusUnknown
	}
	if maybeEnd == nil {
		zero := 0
		maybeEnd = &zero
	}
	if maybePrevStatus == nil {
		unknown := lib.StatusUnknown
		maybePrevStatus = &unknown
	}
	return begin, *maybeEnd, *maybePrevStatus
}

func (w *worker) modelsToPoll() (models []string) {
	var modelID string
	w.mustQuery(`
		select distinct model_id from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where block.block is null or block.block<?
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
		join users on users.chat_id=signals.chat_id`,
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
		`select distinct chat_id from signals where endpoint=? order by chat_id`,
		queryParams{endpoint},
		scanTo{&chatID},
		func() { chats = append(chats, chatID) })
	return
}

func (w *worker) modelsForChat(endpoint string, chatID int64) (models []string) {
	var modelID string
	w.mustQuery(
		`select model_id from signals where chat_id=? and endpoint=? order by model_id`,
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
		where signals.chat_id=? and signals.endpoint=?
		order by models.model_id`,
		queryParams{chatID, endpoint},
		scanTo{&iter.modelID, &iter.status},
		func() { statuses = append(statuses, iter) })
	return
}

func (w *worker) subscriptionExists(endpoint string, chatID int64, modelID string) bool {
	count := w.mustInt("select count(*) from signals where chat_id=? and model_id=? and endpoint=?", chatID, modelID, endpoint)
	return count != 0
}

func (w *worker) subscriptionsNumber(endpoint string, chatID int64) int {
	return w.mustInt("select count(*) from signals where chat_id=? and endpoint=?", chatID, endpoint)
}

func (w *worker) user(chatID int64) (user user, found bool) {
	found = w.maybeRecord("select chat_id, max_models, reports, blacklist, show_images, offline_notifications from users where chat_id=?",
		queryParams{chatID},
		scanTo{&user.chatID, &user.maxModels, &user.reports, &user.blacklist, &user.showImages, &user.offlineNotifications})
	return
}

func (w *worker) addUser(endpoint string, chatID int64) {
	w.mustExec(`insert or ignore into users (chat_id, max_models) values (?, ?)`, chatID, w.cfg.MaxModels)
	w.mustExec(`insert or ignore into emails (endpoint, chat_id, email) values (?, ?, ?)`, endpoint, chatID, uuid.New())
}

func (w *worker) maybeModel(modelID string) *model {
	var result model
	if w.maybeRecord("select model_id, status from models where model_id=?", queryParams{modelID}, scanTo{&result.modelID, &result.status}) {
		return &result
	}
	return nil
}

func (w *worker) email(endpoint string, chatID int64) string {
	username := w.mustString("select email from emails where endpoint=? and chat_id=?", endpoint, chatID)
	return username + "@" + w.cfg.Mail.Host
}

func (w *worker) transaction(uuid string) (status payments.StatusKind, chatID int64, endpoint string, found bool) {
	found = w.maybeRecord("select status, chat_id, endpoint from transactions where local_id=?",
		queryParams{uuid},
		scanTo{&status, &chatID, &endpoint})
	return
}

func (w *worker) changesFromTo(modelID string, from int, to int) []statusChange {
	var changes []statusChange
	first := true
	var change statusChange
	var firstStatus *lib.StatusKind
	var firstTimestamp *int
	w.mustQuery(`
		select status, timestamp, prev_status, prev_timestamp
		from(
			select
				*,
				lag(status) over (order by timestamp) as prev_status,
				lag(timestamp) over (order by timestamp) as prev_timestamp
			from status_changes
			where model_id=?)
		where timestamp>=?
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
		insert into users (chat_id, max_models) values (?, ?)
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
		"select result, count(*) from interactions indexed by ix_interactions_timestamp where endpoint=? and timestamp>? group by result",
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
		"select kind, count(*) from interactions indexed by ix_interactions_timestamp where endpoint=? and timestamp>? and result=200 group by kind",
		queryParams{endpoint, timestamp},
		scanTo{&kind, &count},
		func() { results[kind] = count })
	return results
}

func (w *worker) usersCount(endpoint string) int {
	return w.mustInt("select count(distinct chat_id) from signals where endpoint=?", endpoint)
}

func (w *worker) groupsCount(endpoint string) int {
	return w.mustInt("select count(distinct chat_id) from signals where endpoint=? and chat_id < 0", endpoint)
}

func (w *worker) activeUsersOnEndpointCount(endpoint string) int {
	return w.mustInt(`
		select count(distinct signals.chat_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0) and signals.endpoint=?`,
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
	return w.mustInt("select count(distinct model_id) from signals where endpoint=?", endpoint)
}

func (w *worker) modelsToPollOnEndpointCount(endpoint string) int {
	return w.mustInt(`
		select count(distinct signals.model_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < ?) and signals.endpoint=?`,
		w.cfg.BlockThreshold,
		endpoint)
}

func (w *worker) modelsToPollTotalCount() int {
	return w.mustInt(`
		select count(distinct signals.model_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < ?)`,
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
			where (block.block is null or block.block = 0) and signals.endpoint=?
			group by signals.chat_id
			having count(*) >= ?);`,
		endpoint,
		w.cfg.MaxModels-w.cfg.HeavyUserRemainder)
}

func (w *worker) transactionsOnEndpoint(endpoint string) int {
	return w.mustInt("select count(*) from transactions where endpoint=?", endpoint)
}

func (w *worker) transactionsOnEndpointFinished(endpoint string) int {
	return w.mustInt("select count(*) from transactions where endpoint=? and status=?", endpoint, payments.StatusFinished)
}

func (w *worker) recordForEmail(username string) *email {
	email := email{email: username}
	if w.maybeRecord(
		`select chat_id, endpoint from emails where email=?`,
		queryParams{username},
		scanTo{&email.chatID, &email.endpoint}) {

		return &email
	}
	return nil
}

func (w *worker) confirmSub(sub subscription) {
	w.mustExec("insert or ignore into models (model_id) values (?)", sub.modelID)
	w.mustExec("update signals set confirmed=1 where endpoint=? and chat_id=? and model_id=?", sub.endpoint, sub.chatID, sub.modelID)
}

func (w *worker) denySub(sub subscription) {
	w.mustExec("delete from signals where endpoint=? and chat_id=? and model_id=?", sub.endpoint, sub.chatID, sub.modelID)
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
	var status lib.StatusKind
	var special bool
	w.mustQuery(
		"select model_id, status, special from models",
		nil,
		scanTo{&modelID, &status, &special},
		func() {
			if status == lib.StatusOnline {
				statuses[modelID] = true
			}
			if special {
				specialModels[modelID] = true
			}
		})
	return statuses, specialModels
}

func (w *worker) referralID(chatID int64) *string {
	var referralID string
	if !w.maybeRecord("select referral_id from referrals where chat_id=?", queryParams{chatID}, scanTo{&referralID}) {
		return nil
	}
	return &referralID
}

func (w *worker) chatForReferralID(referralID string) *int64 {
	var chatID int64
	if !w.maybeRecord("select chat_id from referrals where referral_id=?", queryParams{referralID}, scanTo{&chatID}) {
		return nil
	}
	return &chatID
}

func (w *worker) incrementBlock(endpoint string, chatID int64) {
	w.mustExec(`
		insert into block (endpoint, chat_id, block) values (?,?,1)
		on conflict(chat_id, endpoint) do update set block=block+1`,
		endpoint,
		chatID)
}

func (w *worker) resetBlock(endpoint string, chatID int64) {
	w.mustExec("update block set block=0 where endpoint=? and chat_id=?", endpoint, chatID)
}
