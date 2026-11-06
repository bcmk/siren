package main

import (
	"fmt"
	"maps"
	"math"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// mockDB holds in memory the rows the bot's queries touch,
// answering them as db.Database does on the inputs the tests use.
// It panics where PostgreSQL would reject a write: a broken key, reference or check.
type mockDB struct {
	// A method no test reaches is left out, so calling it panics on this nil
	database

	mainGID int

	users                []*mockUser
	lastUserID           db.UserID
	streamers            []*mockStreamer
	subscriptions        []mockSubscription
	pendingSubscriptions []*mockPendingSubscription
	block                map[mockBlockKey]int
	notifications        []*mockNotification
	lastNotificationID   int
	statusChanges        []mockStatusChange
	referrals            []*mockReferral
	referralEvents       []mockReferralEvent
	starChargeIDs        []string
	// receivedMessageCommands holds each received message's command, empty for none
	receivedMessageCommands []string
	sentMessageLog          []mockSentMessageLog
	performanceLog          []mockPerformanceLog
}

type mockUser struct {
	db.User
	migratedTo *db.UserID
}

type mockStreamer struct {
	db.Streamer
	poll bool
}

type mockSubscription struct {
	userID     db.UserID
	streamerID int
	endpoint   string
}

type mockPendingSubscription struct {
	db.PendingSubscription
	checking bool
}

type mockBlockKey struct {
	userID   db.UserID
	endpoint string
}

type mockNotification struct {
	db.Notification
	sending bool
}

type mockStatusChange struct {
	streamerID int
	timestamp  int
	status     cmdlib.StatusKind
	prevStatus cmdlib.StatusKind
}

type mockReferral struct {
	userID        db.UserID
	referralID    string
	referredUsers int
}

type mockReferralEvent struct {
	referrer   *db.UserID
	follower   db.UserID
	streamerID *int
}

type mockSentMessageLog struct {
	timestamp int
	userID    db.UserID
	result    int
	latency   int
	kind      db.PacketKind
	command   string
	replySeq  int
	imageMode db.ImageMode
}

type mockPerformanceLog struct {
	kind db.PerformanceLogKind
	data map[string]any
}

func newMockDB() *mockDB {
	return &mockDB{
		mainGID: goroutineID(),
		block:   map[mockBlockKey]int{},
	}
}

func goroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	id, err := strconv.Atoi(strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0])
	checkErr(err)
	return id
}

// checkGID panics off the goroutine that made the mock, as db.Database does with CheckGID on
func (m *mockDB) checkGID() {
	if goroutineID() != m.mainGID {
		panic(fmt.Sprintf("database queries should be run from single thread, expected: %d", m.mainGID))
	}
}

func (m *mockDB) user(id db.UserID) *mockUser {
	for _, u := range m.users {
		if u.UserID == id {
			return u
		}
	}
	return nil
}

func (m *mockDB) userByChat(chatID int64) *mockUser {
	for _, u := range m.users {
		if u.ChatID == chatID {
			return u
		}
	}
	return nil
}

// checkUser stands for a foreign key to users
func (m *mockDB) checkUser(id db.UserID) {
	if m.user(id) == nil {
		panic(fmt.Sprintf("no user %d", id))
	}
}

// liveUser follows migrated_to to the end of the chain
func (m *mockDB) liveUser(u *mockUser) *mockUser {
	for u != nil && u.migratedTo != nil {
		u = m.user(*u.migratedTo)
	}
	return u
}

func (m *mockDB) streamer(id int) *mockStreamer {
	for _, s := range m.streamers {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (m *mockDB) streamerByNickname(nickname string) *mockStreamer {
	for _, s := range m.streamers {
		if s.Nickname == nickname {
			return s
		}
	}
	return nil
}

// checkStreamer stands for a foreign key to streamers
func (m *mockDB) checkStreamer(id int) {
	if m.streamer(id) == nil {
		panic(fmt.Sprintf("no streamer %d", id))
	}
}

func checkStatus(s cmdlib.StatusKind) {
	if s > cmdlib.StatusOnline {
		panic(fmt.Sprintf("status %d is out of range", s))
	}
}

func checkPriority(p db.Priority) {
	if p != db.PriorityHigh && p != db.PriorityLow {
		panic(fmt.Sprintf("priority %d is out of range", p))
	}
}

// insertStreamer adds a streamer, checking what the table's constraints check
func (m *mockDB) insertStreamer(s db.Streamer) *mockStreamer {
	if s.Nickname == "" {
		panic("empty nickname")
	}
	if m.streamerByNickname(s.Nickname) != nil {
		panic(fmt.Sprintf("streamer %s exists", s.Nickname))
	}
	for _, st := range []cmdlib.StatusKind{s.ConfirmedStatus, s.UnconfirmedStatus, s.PrevUnconfirmedStatus} {
		checkStatus(st)
	}
	s.ID = len(m.streamers) + 1
	added := &mockStreamer{Streamer: s}
	m.streamers = append(m.streamers, added)
	return added
}

func (m *mockDB) insertStatusChange(c mockStatusChange) {
	checkStatus(c.status)
	checkStatus(c.prevStatus)
	if c.status != cmdlib.StatusUnknown && c.status == c.prevStatus {
		panic(fmt.Sprintf("status change repeats its status %d", c.status))
	}
	for _, o := range m.statusChanges {
		if o.streamerID == c.streamerID && o.timestamp == c.timestamp {
			panic(fmt.Sprintf("streamer %d has a change at %d", c.streamerID, c.timestamp))
		}
	}
	m.statusChanges = append(m.statusChanges, c)
}

func (m *mockDB) insertPendingSubscription(p mockPendingSubscription) {
	m.checkUser(p.UserID)
	for _, o := range m.pendingSubscriptions {
		if o.UserID == p.UserID && o.Nickname == p.Nickname && o.Endpoint == p.Endpoint {
			panic(fmt.Sprintf("duplicate pending subscription %+v", p.PendingSubscription))
		}
	}
	m.pendingSubscriptions = append(m.pendingSubscriptions, &p)
}

// userCopy copies a user out, so a caller cannot change the stored row
func userCopy(u *mockUser) db.User {
	row := u.User
	row.ChatType = clonePtr(u.ChatType)
	row.MemberCount = clonePtr(u.MemberCount)
	row.Timezone = clonePtr(u.Timezone)
	row.AffiliateParams = maps.Clone(u.AffiliateParams)
	return row
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	return new(*p)
}

func (m *mockDB) AddFeedback(_ string, userID db.UserID, _ string, _ int) {
	m.checkGID()
	m.checkUser(userID)
}

func (m *mockDB) AddPendingSubscription(
	userID db.UserID,
	nickname string,
	endpoint string,
	referral bool,
	command string,
	replySeq int,
) {
	m.checkGID()
	m.insertPendingSubscription(mockPendingSubscription{PendingSubscription: db.PendingSubscription{
		UserID:   userID,
		Nickname: nickname,
		Endpoint: endpoint,
		Referral: referral,
		Command:  command,
		ReplySeq: replySeq,
	}})
}

func (m *mockDB) AddReferral(userID db.UserID, referralID string) {
	m.checkGID()
	m.checkUser(userID)
	for _, r := range m.referrals {
		if r.userID == userID {
			panic(fmt.Sprintf("user %d has a referral", userID))
		}
	}
	m.referrals = append(m.referrals, &mockReferral{userID: userID, referralID: referralID})
}

func (m *mockDB) AddReferralEvent(_ int, referrerUserID *db.UserID, followerUserID db.UserID, streamerID *int) {
	m.checkGID()
	if referrerUserID != nil {
		m.checkUser(*referrerUserID)
	}
	m.checkUser(followerUserID)
	if streamerID != nil {
		m.checkStreamer(*streamerID)
	}
	m.referralEvents = append(m.referralEvents, mockReferralEvent{
		referrer:   clonePtr(referrerUserID),
		follower:   followerUserID,
		streamerID: clonePtr(streamerID),
	})
}

func (m *mockDB) AddReferrerBonus(userID db.UserID, bonus int) {
	m.checkGID()
	if u := m.user(userID); u != nil {
		u.MaxSubs += bonus
	}
}

func (m *mockDB) AddSubscription(userID db.UserID, streamerID int, endpoint string) {
	m.checkGID()
	m.checkUser(userID)
	m.checkStreamer(streamerID)
	sub := mockSubscription{userID: userID, streamerID: streamerID, endpoint: endpoint}
	if slices.Contains(m.subscriptions, sub) {
		panic(fmt.Sprintf("duplicate subscription %+v", sub))
	}
	m.subscriptions = append(m.subscriptions, sub)
}

func (m *mockDB) AddUser(chatID int64, maxSubs int, now int, chatType string) (id db.UserID, created bool) {
	m.checkGID()
	if u := m.liveUser(m.userByChat(chatID)); u != nil {
		if u.ChatType == nil && chatType != "" {
			u.ChatType = &chatType
		}
		return u.UserID, false
	}
	m.lastUserID++
	u := &mockUser{User: db.User{
		ChatID:               chatID,
		UserID:               m.lastUserID,
		MaxSubs:              maxSubs,
		ShowImages:           true,
		OfflineNotifications: true,
		ShowSubject:          true,
		CreatedAt:            int64(now),
	}}
	if chatType != "" {
		u.ChatType = &chatType
	}
	m.users = append(m.users, u)
	return u.UserID, true
}

func (m *mockDB) BlacklistUser(userID db.UserID) {
	m.checkGID()
	if u := m.user(userID); u != nil {
		u.Blacklist = true
	}
}

func (m *mockDB) BroadcastUsers(endpoint string) (users []db.User) {
	m.checkGID()
	ids := map[db.UserID]bool{}
	for _, sub := range m.subscriptions {
		if sub.endpoint == endpoint && m.user(sub.userID).ChatID > 0 {
			ids[sub.userID] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		users = append(users, db.User{UserID: id, Blocked: m.block[mockBlockKey{id, endpoint}]})
	}
	return
}

func (m *mockDB) ChangesFromToForStreamers(streamerIDs []int, from int, to int) map[int][]db.StatusChange {
	m.checkGID()
	result := map[int][]db.StatusChange{}
	for _, id := range streamerIDs {
		var changes []mockStatusChange
		for _, c := range m.statusChanges {
			if c.streamerID == id && c.timestamp >= from {
				changes = append(changes, c)
			}
		}
		slices.SortFunc(changes, func(a, b mockStatusChange) int { return a.timestamp - b.timestamp })
		if s := m.streamer(id); s != nil {
			opening := s.UnconfirmedStatus
			if len(changes) > 0 {
				opening = changes[0].prevStatus
			}
			result[id] = []db.StatusChange{{Status: opening, Timestamp: from}}
		}
		for _, c := range changes {
			result[id] = append(result[id], db.StatusChange{Status: c.status, Timestamp: min(c.timestamp, to)})
		}
		result[id] = append(result[id], db.StatusChange{Timestamp: to})
	}
	return result
}

func (m *mockDB) ChatIDForUser(userID db.UserID) (int64, bool) {
	m.checkGID()
	u := m.liveUser(m.user(userID))
	if u == nil {
		return 0, false
	}
	return u.ChatID, true
}

func (m *mockDB) CheckingSubs(nicknames []string) map[string][]db.PendingSubscription {
	m.checkGID()
	subs := map[string][]db.PendingSubscription{}
	for _, p := range m.pendingSubscriptions {
		if p.checking && slices.Contains(nicknames, p.Nickname) {
			sub := p.PendingSubscription
			sub.ChatID = m.user(p.UserID).ChatID
			subs[p.Nickname] = append(subs[p.Nickname], sub)
		}
	}
	return subs
}

func (m *mockDB) ConfirmStatusChanges(now int, onlineSeconds int, offlineSeconds int) []db.ConfirmedStatusChange {
	m.checkGID()
	var result []db.ConfirmedStatusChange
	for _, s := range m.streamers {
		if s.ConfirmedStatus == s.UnconfirmedStatus {
			continue
		}
		held := now - s.UnconfirmedTimestamp
		switch {
		case s.UnconfirmedStatus == cmdlib.StatusOnline && held >= onlineSeconds,
			s.UnconfirmedStatus == cmdlib.StatusOffline && held >= offlineSeconds,
			s.UnconfirmedStatus == cmdlib.StatusUnknown:
		default:
			continue
		}
		result = append(result, db.ConfirmedStatusChange{
			StreamerID: s.ID,
			Nickname:   s.Nickname,
			Status:     s.UnconfirmedStatus,
			PrevStatus: s.ConfirmedStatus,
			Timestamp:  now,
		})
		s.ConfirmedStatus = s.UnconfirmedStatus
	}
	return result
}

func (m *mockDB) ConfirmSub(sub db.PendingSubscription) int {
	m.checkGID()
	s := m.streamerByNickname(sub.Nickname)
	if s == nil {
		s = m.insertStreamer(db.Streamer{Nickname: sub.Nickname})
	}
	m.AddSubscription(sub.UserID, s.ID, sub.Endpoint)
	m.DenySub(sub)
	return s.ID
}

func (m *mockDB) DeleteNotification(id int) {
	m.checkGID()
	m.notifications = slices.DeleteFunc(m.notifications, func(n *mockNotification) bool { return n.ID == id })
}

func (m *mockDB) DenySub(sub db.PendingSubscription) {
	m.checkGID()
	m.pendingSubscriptions = slices.DeleteFunc(m.pendingSubscriptions, func(p *mockPendingSubscription) bool {
		return p.UserID == sub.UserID && p.Endpoint == sub.Endpoint && p.Nickname == sub.Nickname
	})
}

func (m *mockDB) EnsureUser(chatID int64) db.UserID {
	m.checkGID()
	id, _ := m.AddUser(chatID, testConfig.MaxSubs, int(time.Now().Unix()), "")
	return id
}

func (m *mockDB) GrantStarPaymentSubs(
	chatID int64,
	_ string,
	chargeID string,
	_ int,
	_ string,
	quantity int,
	_ string,
	now int,
) (added bool, maxSubs int, userID db.UserID) {
	m.checkGID()
	if slices.Contains(m.starChargeIDs, chargeID) {
		return false, 0, m.liveUser(m.userByChat(chatID)).UserID
	}
	userID, _ = m.AddUser(chatID, testConfig.MaxSubs, now, "")
	m.starChargeIDs = append(m.starChargeIDs, chargeID)
	u := m.user(userID)
	u.MaxSubs += quantity
	return true, u.MaxSubs, userID
}

func (m *mockDB) IncrementReferredUsers(userID db.UserID) {
	m.checkGID()
	for _, r := range m.referrals {
		if r.userID == userID {
			r.referredUsers++
		}
	}
}

func (m *mockDB) IncrementReports(userIDs []db.UserID) {
	m.checkGID()
	for _, id := range userIDs {
		if u := m.user(id); u != nil {
			u.Reports++
		}
	}
}

func (m *mockDB) KnownStreamers() map[string]bool {
	m.checkGID()
	streamers := map[string]bool{}
	for _, s := range m.streamers {
		if s.UnconfirmedStatus != cmdlib.StatusUnknown {
			streamers[s.Nickname] = true
		}
	}
	return streamers
}

func (m *mockDB) LiveUserID(userID db.UserID) db.UserID {
	m.checkGID()
	if u := m.liveUser(m.user(userID)); u != nil {
		return u.UserID
	}
	return userID
}

func (m *mockDB) LogDiceSkippedSends(
	timestamp int,
	result int,
	priority db.Priority,
	kind db.PacketKind,
	skips []db.SkippedSend,
) {
	m.checkGID()
	checkPriority(priority)
	for _, s := range skips {
		m.checkUser(s.UserID)
		m.sentMessageLog = append(m.sentMessageLog, mockSentMessageLog{
			timestamp: timestamp,
			userID:    s.UserID,
			result:    result,
			kind:      kind,
		})
	}
}

func (m *mockDB) LogPerformance(_ int, kind db.PerformanceLogKind, _ int, data map[string]any) {
	m.checkGID()
	m.performanceLog = append(m.performanceLog, mockPerformanceLog{kind: kind, data: maps.Clone(data)})
}

func (m *mockDB) LogReceivedMessage(_ int, _ string, userID db.UserID, command string) {
	m.checkGID()
	m.checkUser(userID)
	m.receivedMessageCommands = append(m.receivedMessageCommands, command)
}

func (m *mockDB) LogSentMessage(
	timestamp int,
	userID db.UserID,
	result int,
	_ string,
	priority db.Priority,
	latency int,
	kind db.PacketKind,
	command string,
	replySeq int,
	imageMode db.ImageMode,
) {
	m.checkGID()
	m.checkUser(userID)
	checkPriority(priority)
	m.sentMessageLog = append(m.sentMessageLog, mockSentMessageLog{
		timestamp: timestamp,
		userID:    userID,
		result:    result,
		latency:   latency,
		kind:      kind,
		command:   command,
		replySeq:  replySeq,
		imageMode: imageMode,
	})
}

func (m *mockDB) MarkUnconfirmedAsChecking() {
	m.checkGID()
	for _, p := range m.pendingSubscriptions {
		p.checking = true
	}
}

func (m *mockDB) MaybeStreamer(nickname string) *db.Streamer {
	m.checkGID()
	s := m.streamerByNickname(nickname)
	if s == nil {
		return nil
	}
	return new(s.Streamer)
}

func (m *mockDB) MigrateChat(fromID, toID int64) *db.ChatMigration {
	m.checkGID()
	if fromID == toID {
		return nil
	}
	src := m.userByChat(fromID)
	if src == nil || src.migratedTo != nil {
		return nil
	}
	dst := m.userByChat(toID)
	if dst == nil {
		src.ChatID = toID
		src.ChatType = new("supergroup")
		return &db.ChatMigration{Renamed: true}
	}
	// Only links the source: internal/db tests what a merge moves
	src.migratedTo = new(dst.UserID)
	return &db.ChatMigration{}
}

func (m *mockDB) NewNotifications() []db.Notification {
	m.checkGID()
	var nots []db.Notification
	for _, n := range m.notifications {
		if n.sending {
			continue
		}
		n.sending = true
		u := m.user(n.UserID)
		s := m.streamer(*n.StreamerID)
		not := n.Notification
		not.StreamerID = clonePtr(n.StreamerID)
		not.TimeDiff = clonePtr(n.TimeDiff)
		not.Viewers = clonePtr(n.Viewers)
		not.Nickname = s.Nickname
		not.SilentMessages = u.SilentMessages
		not.ChatID = u.ChatID
		not.ChatType = clonePtr(u.ChatType)
		not.AffiliateParams = maps.Clone(u.AffiliateParams)
		not.Reports = u.Reports
		nots = append(nots, not)
	}
	return nots
}

func (m *mockDB) OnlineStreamers() map[string]bool {
	m.checkGID()
	online := map[string]bool{}
	for _, s := range m.streamers {
		if s.UnconfirmedStatus == cmdlib.StatusOnline {
			online[s.Nickname] = true
		}
	}
	return online
}

func (m *mockDB) PolledStreamersWithStatus() []db.Streamer {
	m.checkGID()
	var out []db.Streamer
	for _, s := range m.streamers {
		if s.poll {
			out = append(out, s.Streamer)
		}
	}
	slices.SortFunc(out, func(a, b db.Streamer) int { return strings.Compare(a.Nickname, b.Nickname) })
	return out
}

// QueryDurations returns fixed statistics: the mock measures nothing
func (m *mockDB) QueryDurations() map[string]db.QueryDurationsData {
	return map[string]db.QueryDurationsData{
		"a": {Avg: 3, Count: 1},
		"b": {Avg: 2, Count: 3},
	}
}

func (m *mockDB) ReferralID(userID db.UserID) *string {
	m.checkGID()
	for _, r := range m.referrals {
		if r.userID == userID {
			return new(r.referralID)
		}
	}
	return nil
}

func (m *mockDB) ReferralIDExists(referralID string) bool {
	m.checkGID()
	return m.UserByReferralID(referralID) != nil
}

func (m *mockDB) RemoveSubscription(userID db.UserID, nickname string, endpoint string) {
	m.checkGID()
	m.subscriptions = slices.DeleteFunc(m.subscriptions, func(s mockSubscription) bool {
		return s.userID == userID && s.endpoint == endpoint && m.streamer(s.streamerID).Nickname == nickname
	})
	m.pendingSubscriptions = slices.DeleteFunc(m.pendingSubscriptions, func(p *mockPendingSubscription) bool {
		return p.UserID == userID && p.Nickname == nickname && p.Endpoint == endpoint
	})
}

func (m *mockDB) RequeueNotification(id int) {
	m.checkGID()
	for _, n := range m.notifications {
		if n.ID == id {
			n.sending = false
		}
	}
}

func (m *mockDB) ResetBlock(endpoint string, userID db.UserID) {
	m.checkGID()
	m.block[mockBlockKey{userID, endpoint}] = 0
}

// SearchStreamers finds nothing: internal/db tests the search
func (m *mockDB) SearchStreamers(string) []string {
	m.checkGID()
	return nil
}

func (m *mockDB) SetAffiliateParams(userID db.UserID, params map[string]string) {
	m.checkGID()
	if u := m.user(userID); u != nil {
		u.AffiliateParams = nil
		if len(params) > 0 {
			u.AffiliateParams = maps.Clone(params)
		}
	}
}

func (m *mockDB) SetLimit(userID db.UserID, maxSubs int) {
	m.checkGID()
	if u := m.user(userID); u != nil {
		u.MaxSubs = maxSubs
	}
}

func (m *mockDB) SetMemberSubscriptions(userID db.UserID, memberSubscriptions bool) {
	m.checkGID()
	if u := m.user(userID); u != nil {
		u.MemberSubscriptions = memberSubscriptions
	}
}

func (m *mockDB) SetPoll(nickname string, on bool) bool {
	m.checkGID()
	m.streamerByNickname(nickname).poll = on
	return true
}

func (m *mockDB) SetShowImages(userID db.UserID, showImages bool) {
	m.checkGID()
	if u := m.user(userID); u != nil {
		u.ShowImages = showImages
	}
}

func (m *mockDB) SetTimezone(userID db.UserID, timezone string) {
	m.checkGID()
	if u := m.user(userID); u != nil {
		u.Timezone = nil
		if timezone != "" {
			u.Timezone = &timezone
		}
	}
}

func (m *mockDB) StoreNotifications(nots []db.Notification) {
	m.checkGID()
	for _, n := range nots {
		m.checkUser(n.UserID)
		if n.StreamerID == nil {
			panic("a notification without a streamer")
		}
		m.checkStreamer(*n.StreamerID)
		checkPriority(n.Priority)
		m.lastNotificationID++
		stored := db.Notification{
			ID:              m.lastNotificationID,
			Endpoint:        n.Endpoint,
			UserID:          n.UserID,
			StreamerID:      clonePtr(n.StreamerID),
			Status:          n.Status,
			TimeDiff:        clonePtr(n.TimeDiff),
			ImageURL:        n.ImageURL,
			ProfileImageURL: n.ProfileImageURL,
			MissingImage:    n.MissingImage,
			Viewers:         clonePtr(n.Viewers),
			ShowKind:        n.ShowKind,
			Social:          n.Social,
			Sound:           n.Sound,
			Priority:        n.Priority,
			Kind:            n.Kind,
			Command:         n.Command,
			ReplySeq:        n.ReplySeq,
			FieldsHint:      n.FieldsHint,
			Subject:         n.Subject,
		}
		m.notifications = append(m.notifications, &mockNotification{Notification: stored})
	}
}

func (m *mockDB) StreamersForUser(endpoint string, userID db.UserID) (streamers []db.Streamer) {
	m.checkGID()
	for _, s := range m.streamerRowsForUser(endpoint, userID) {
		streamers = append(streamers, db.Streamer{ID: s.ID, Nickname: s.Nickname})
	}
	return
}

// streamerRowsForUser returns the streamers a user is subscribed to on an endpoint,
// ordered by nickname
func (m *mockDB) streamerRowsForUser(endpoint string, userID db.UserID) (streamers []*mockStreamer) {
	for _, sub := range m.subscriptions {
		if sub.userID == userID && sub.endpoint == endpoint {
			streamers = append(streamers, m.streamer(sub.streamerID))
		}
	}
	slices.SortFunc(streamers, func(a, b *mockStreamer) int { return strings.Compare(a.Nickname, b.Nickname) })
	return
}

func (m *mockDB) SubscribedOrPending(endpoint string, userID db.UserID, nickname string) bool {
	m.checkGID()
	return slices.Contains(m.SubscribedOrPendingNicknames(endpoint, userID), nickname)
}

func (m *mockDB) SubscribedOrPendingCount(endpoint string, userID db.UserID) int {
	m.checkGID()
	count := len(m.streamerRowsForUser(endpoint, userID))
	for _, p := range m.pendingSubscriptions {
		if p.UserID == userID && p.Endpoint == endpoint {
			count++
		}
	}
	return count
}

func (m *mockDB) SubscribedOrPendingNicknames(endpoint string, userID db.UserID) []string {
	m.checkGID()
	nicknames := map[string]bool{}
	for _, s := range m.streamerRowsForUser(endpoint, userID) {
		nicknames[s.Nickname] = true
	}
	for _, p := range m.pendingSubscriptions {
		if p.UserID == userID && p.Endpoint == endpoint {
			nicknames[p.Nickname] = true
		}
	}
	return slices.Sorted(maps.Keys(nicknames))
}

func (m *mockDB) UnconfirmedStatusesForStreamers(nicknames []string) map[string]db.StatusChange {
	m.checkGID()
	statuses := map[string]db.StatusChange{}
	for _, s := range m.streamers {
		if slices.Contains(nicknames, s.Nickname) {
			statuses[s.Nickname] = db.StatusChange{
				Nickname:  s.Nickname,
				Status:    s.UnconfirmedStatus,
				Timestamp: s.UnconfirmedTimestamp,
			}
		}
	}
	return statuses
}

func (m *mockDB) UnconfirmedStatusesForUser(endpoint string, userID db.UserID) (statuses []db.Streamer) {
	m.checkGID()
	for _, s := range m.streamerRowsForUser(endpoint, userID) {
		row := s.Streamer
		row.ConfirmedStatus = cmdlib.StatusUnknown
		statuses = append(statuses, row)
	}
	return
}

func (m *mockDB) UpsertUnconfirmedStatusChanges(
	changedStatuses []db.StatusChange,
	timestamp int,
) db.UpsertUnconfirmedTimings {
	m.checkGID()
	seen := map[string]bool{}
	for _, c := range changedStatuses {
		if seen[c.Nickname] {
			panic(fmt.Sprintf("streamer %s changes twice in one upsert", c.Nickname))
		}
		seen[c.Nickname] = true
		checkStatus(c.Status)
		s := m.streamerByNickname(c.Nickname)
		if s == nil {
			s = m.insertStreamer(db.Streamer{
				Nickname:             c.Nickname,
				UnconfirmedStatus:    c.Status,
				UnconfirmedTimestamp: timestamp,
			})
		} else {
			s.PrevUnconfirmedStatus = s.UnconfirmedStatus
			s.PrevUnconfirmedTimestamp = s.UnconfirmedTimestamp
			s.UnconfirmedStatus = c.Status
			s.UnconfirmedTimestamp = max(timestamp, s.UnconfirmedTimestamp+1)
		}
		m.insertStatusChange(mockStatusChange{
			streamerID: s.ID,
			timestamp:  s.UnconfirmedTimestamp,
			status:     s.UnconfirmedStatus,
			prevStatus: s.PrevUnconfirmedStatus,
		})
	}
	return db.UpsertUnconfirmedTimings{}
}

func (m *mockDB) UserByChatID(chatID int64) (user db.User, found bool) {
	m.checkGID()
	u := m.liveUser(m.userByChat(chatID))
	if u == nil {
		return db.User{}, false
	}
	return userCopy(u), true
}

func (m *mockDB) UserByID(userID db.UserID) (user db.User, found bool) {
	m.checkGID()
	u := m.user(userID)
	if u == nil {
		return db.User{}, false
	}
	return userCopy(u), true
}

func (m *mockDB) UserByReferralID(referralID string) *db.UserID {
	m.checkGID()
	for _, r := range m.referrals {
		if r.referralID == referralID {
			return new(r.userID)
		}
	}
	return nil
}

func (m *mockDB) UsersForStreamers(streamerIDs []int) (users map[int][]db.User, endpoints map[int][]string) {
	m.checkGID()
	users = map[int][]db.User{}
	endpoints = map[int][]string{}
	for _, sub := range m.subscriptions {
		if !slices.Contains(streamerIDs, sub.streamerID) {
			continue
		}
		u := m.user(sub.userID)
		users[sub.streamerID] = append(users[sub.streamerID], db.User{
			ChatID:               u.ChatID,
			UserID:               u.UserID,
			OfflineNotifications: u.OfflineNotifications,
			ShowImages:           u.ShowImages,
			ShowSubject:          u.ShowSubject,
			Blocked:              m.block[mockBlockKey{u.UserID, sub.endpoint}],
		})
		endpoints[sub.streamerID] = append(endpoints[sub.streamerID], sub.endpoint)
	}
	return
}

func count[T any](rows []T, match func(T) bool) int {
	n := 0
	for _, r := range rows {
		if match(r) {
			n++
		}
	}
	return n
}

func (m *mockDB) chatID(userID db.UserID) int64 { return m.user(userID).ChatID }

func (m *mockDB) subscriptionCount(nickname string) int {
	return count(m.subscriptions, func(s mockSubscription) bool { return m.streamer(s.streamerID).Nickname == nickname })
}

func (m *mockDB) pendingSubscriptionCount(nickname string) int {
	return count(m.pendingSubscriptions, func(p *mockPendingSubscription) bool { return p.Nickname == nickname })
}

// streamerReferralEventCount returns how many referral events
// record the chat following the streamer
func (m *mockDB) streamerReferralEventCount(nickname string, chatID int64) int {
	return count(m.referralEvents, func(e mockReferralEvent) bool {
		return e.streamerID != nil && m.streamer(*e.streamerID).Nickname == nickname && m.chatID(e.follower) == chatID
	})
}

// setMaxBlock stores the largest blocked-send count for a chat on an endpoint,
// so the chat loses any roll of the dice: a win at threshold/MaxInt32 is unreachable in practice
func (m *mockDB) setMaxBlock(endpoint string, chatID int64) {
	m.block[mockBlockKey{m.userByChat(chatID).UserID, endpoint}] = math.MaxInt32
}

func (u *mockUser) inZone(zone string) bool { return u.Timezone != nil && *u.Timezone == zone }
