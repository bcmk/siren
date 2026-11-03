package main

import "github.com/bcmk/siren/v6/internal/db"

// database is what the bot asks of db.Database, so tests can run it on a mock
type database interface {
	AddFeedback(endpoint string, userID db.UserID, text string, timestamp int)
	AddPendingSubscription(userID db.UserID, nickname string, endpoint string, referral bool, command string, replySeq int)
	AddReferral(userID db.UserID, referralID string)
	AddReferralEvent(timestamp int, referrerUserID *db.UserID, followerUserID db.UserID, streamerID *int)
	AddReferrerBonus(userID db.UserID, bonus int)
	AddSubscription(userID db.UserID, streamerID int, endpoint string)
	AddUser(chatID int64, maxSubs int, now int, chatType string) (id db.UserID, created bool)
	ApplyMigrations()
	BlacklistUser(userID db.UserID)
	BroadcastUsers(endpoint string) []db.User
	ChangesFromToForStreamers(streamerIDs []int, from int, to int) map[int][]db.StatusChange
	ChatIDForUser(userID db.UserID) (int64, bool)
	CheckingSubs(nicknames []string) map[string][]db.PendingSubscription
	ConfirmStatusChanges(now int, onlineSeconds int, offlineSeconds int) []db.ConfirmedStatusChange
	ConfirmSub(sub db.PendingSubscription) int
	DeleteNotification(id int)
	DenySub(sub db.PendingSubscription)
	EnsureUser(chatID int64) db.UserID
	GrantStarPaymentSubs(
		chatID int64,
		endpoint string,
		chargeID string,
		stars int,
		product string,
		quantity int,
		payload string,
		now int,
	) (added bool, maxSubs int, userID db.UserID)
	IncrementBlock(endpoint string, userID db.UserID)
	IncrementPollErrors(nicknames []string)
	IncrementReferredUsers(userID db.UserID)
	IncrementReports(userIDs []db.UserID)
	KnownStreamers() map[string]bool
	LiveUserID(userID db.UserID) db.UserID
	LogDiceSkippedSends(timestamp int, result int, priority db.Priority, kind db.PacketKind, skips []db.SkippedSend)
	LogPerformance(timestamp int, kind db.PerformanceLogKind, durationMs int, data map[string]any)
	LogReceivedMessage(timestamp int, endpoint string, userID db.UserID, command string)
	LogSentMessage(
		timestamp int,
		userID db.UserID,
		result int,
		endpoint string,
		priority db.Priority,
		latency int,
		kind db.PacketKind,
		command string,
		replySeq int,
		imageMode db.ImageMode,
	)
	MarkUnconfirmedAsChecking()
	MaybeStreamer(nickname string) *db.Streamer
	MigrateChat(fromID, toID int64) *db.ChatMigration
	NewNotifications() []db.Notification
	OnlineStreamers() map[string]bool
	PolledStreamersWithStatus() []db.Streamer
	ProfilePhotoUploaded(endpoint string, hash string) bool
	QueryDurations() map[string]db.QueryDurationsData
	ReferralID(userID db.UserID) *string
	ReferralIDExists(referralID string) bool
	RemoveAllSubscriptions(userID db.UserID, endpoint string)
	RemoveSubscription(userID db.UserID, nickname string, endpoint string)
	RequeueNotification(id int)
	ResetBlock(endpoint string, userID db.UserID)
	ResetCheckingToUnconfirmed()
	ResetNotificationSending()
	ResetQueryStats()
	ResumeGIDCheck()
	SearchStreamers(term string) []string
	SetAffiliateParams(userID db.UserID, params map[string]string)
	SetLimit(userID db.UserID, maxSubs int)
	SetMemberSubscriptions(userID db.UserID, memberSubscriptions bool)
	SetOfflineNotifications(userID db.UserID, offlineNotifications bool)
	SetPoll(nickname string, on bool) bool
	SetProfilePhotoHash(endpoint string, hash string)
	SetShowImages(userID db.UserID, showImages bool)
	SetShowSubject(userID db.UserID, showSubject bool)
	SetSilentMessages(userID db.UserID, silentMessages bool)
	SetTimezone(userID db.UserID, timezone string)
	StoreNotifications(nots []db.Notification)
	StreamersForUser(endpoint string, userID db.UserID) []db.Streamer
	StreamersToPoll() []string
	SubscribedOrPending(endpoint string, userID db.UserID, nickname string) bool
	SubscribedOrPendingCount(endpoint string, userID db.UserID) int
	SubscribedOrPendingNicknames(endpoint string, userID db.UserID) []string
	SubscribedStreamers() map[string]bool
	SuspendGIDCheck()
	UnconfirmedStatusesForStreamers(nicknames []string) map[string]db.StatusChange
	UnconfirmedStatusesForUser(endpoint string, userID db.UserID) []db.Streamer
	UnconfirmedSubs() map[string]bool
	UpdateMemberCount(userID db.UserID, memberCount int)
	UpsertUnconfirmedStatusChanges(changedStatuses []db.StatusChange, timestamp int) db.UpsertUnconfirmedTimings
	UserByChatID(chatID int64) (user db.User, found bool)
	UserByID(userID db.UserID) (user db.User, found bool)
	UserByReferralID(referralID string) *db.UserID
	UsersForStreamers(streamerIDs []int) (users map[int][]db.User, endpoints map[int][]string)
}
