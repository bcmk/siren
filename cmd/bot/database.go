package main

import "github.com/bcmk/siren/v6/internal/db"

// database is what the bot asks of db.Database, so tests can run it on a mock
type database interface {
	AddFeedback(endpoint string, userID db.UserID, text string, timestamp int)
	AddPendingSubscription(userID db.UserID, nickname string, endpoint string, referral bool, command string, replySeq int)
	AddReferralEvent(timestamp int, referrerUserID *db.UserID, followerUserID db.UserID, streamerID *int)
	AddSubscription(userID db.UserID, streamerID int, endpoint string)
	AddUserAndChat(endpoint string, chatID int64, now int, chatType string) (id db.UserID, userCreated bool)
	ApplyMigrations()
	BlacklistUser(userID db.UserID)
	BroadcastChats(endpoint string) []db.Chat
	ChangesFromToForStreamers(streamerIDs []int, from int, to int) map[int][]db.StatusChange
	Chat(endpoint string, userID db.UserID) (chat db.Chat, found bool)
	ChatIDForUser(userID db.UserID) (int64, bool)
	ChatsForStreamers(streamerIDs []int) map[int][]db.Chat
	CheckingSubs(nicknames []string) map[string][]db.PendingSubscription
	ConfirmStatusChanges(now int, onlineSeconds int, offlineSeconds int) []db.ConfirmedStatusChange
	ConfirmSub(sub db.PendingSubscription) int
	CreditReferrer(endpoint string, userID db.UserID, bonus int)
	DeleteNotification(id int)
	DenySub(sub db.PendingSubscription)
	EnsureUser(chatID int64) db.UserID
	EnsureUserAndChat(endpoint string, chatID int64) db.UserID
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
	IncrementReports(nots []db.Notification)
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
	MarkGreeted(endpoint string, userID db.UserID) (firstGreeting bool)
	MarkUnconfirmedAsChecking()
	MaybeStreamer(nickname string) *db.Streamer
	MigrateChat(fromID, toID int64) *db.ChatMigration
	NewNotifications() []db.Notification
	OnlineStreamers() map[string]bool
	PolledStreamersWithStatus() []db.Streamer
	ProfilePhotoUploaded(endpoint string, hash string) bool
	QueryDurations() map[string]db.QueryDurationsData
	ReferralIDExists(endpoint string, referralID string) bool
	RemoveAllSubscriptions(userID db.UserID, endpoint string)
	RemoveSubscription(userID db.UserID, nickname string, endpoint string)
	RequeueNotification(id int)
	ResetBlock(endpoint string, userID db.UserID)
	ResetCheckingToUnconfirmed()
	ResetNotificationSending()
	ResetQueryStats()
	ResumeGIDCheck()
	SearchStreamers(term string) []string
	SetAffiliateParams(endpoint string, userID db.UserID, params map[string]string)
	SetLimit(endpoint string, userID db.UserID, maxSubs int) (found bool)
	SetMemberSubscriptions(endpoint string, userID db.UserID, memberSubscriptions bool)
	SetOfflineNotifications(endpoint string, userID db.UserID, offlineNotifications bool)
	SetPoll(nickname string, on bool) bool
	SetProfilePhotoHash(endpoint string, hash string)
	SetReferralID(endpoint string, userID db.UserID, referralID string)
	SetShowImages(endpoint string, userID db.UserID, showImages bool)
	SetShowSubject(endpoint string, userID db.UserID, showSubject bool)
	SetSilentMessages(endpoint string, userID db.UserID, silentMessages bool)
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
	UserByReferralID(endpoint string, referralID string) *db.UserID
}
