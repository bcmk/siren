package db

import (
	"cmp"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// asJSON spells out what %v prints as a pointer's address
func asJSON(v any) string {
	b, err := json.Marshal(v)
	checkErr(err)
	return string(b)
}

func insertStreamerRow(d *Database, s Streamer) int {
	d.MustExec("insert into nicknames (nickname) values ($1)", s.Nickname)
	return d.MustInt(`
		insert into streamers (
			nickname,
			confirmed_status,
			unconfirmed_status,
			unconfirmed_timestamp,
			prev_unconfirmed_status,
			prev_unconfirmed_timestamp)
		values ($1, $2, $3, $4, $5, $6)
		returning id`,
		s.Nickname,
		s.ConfirmedStatus,
		s.UnconfirmedStatus,
		s.UnconfirmedTimestamp,
		s.PrevUnconfirmedStatus,
		s.PrevUnconfirmedTimestamp)
}

func TestBroadcastUsers(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	for chatID := int64(1); chatID <= 8; chatID++ {
		db.AddUser(chatID, 0, 0, "")
	}
	db.addSub(1, "a", "ep1")
	db.addSub(2, "b", "ep1")
	db.addSub(3, "c", "ep1")
	db.addSub(3, "c2", "ep1")
	db.addSub(3, "c3", "ep1")
	db.addSub(4, "d", "ep1")
	db.addSub(5, "d", "ep1")
	db.addSub(6, "e", "ep1")
	db.addSub(7, "f", "ep1")
	db.addSub(6, "e", "ep2")
	db.addSub(7, "f", "ep2")
	db.addSub(8, "g", "ep2")
	db.addBlock(2, "ep1", 0)
	db.addBlock(3, "ep1", 5)
	// A count on another endpoint, and a group, which a broadcast skips
	db.addBlock(1, "ep2", 7)
	db.AddUser(-9, 0, 0, "group")
	db.addSub(-9, "h", "ep1")

	tests := []struct {
		endpoint string
		want     []User
	}{
		{
			endpoint: "ep1",
			want: []User{
				{UserID: 1},
				{UserID: 2},
				{UserID: 3, Blocked: 5},
				{UserID: 4},
				{UserID: 5},
				{UserID: 6},
				{UserID: 7},
			},
		},
		{endpoint: "ep2", want: []User{{UserID: 6}, {UserID: 7}, {UserID: 8}}},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			if got := db.BroadcastUsers(tt.endpoint); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BroadcastUsers = %s, want %s", asJSON(got), asJSON(tt.want))
			}
		})
	}
}

func TestBlockCounts(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	increment := db.IncrementBlock
	reset := db.ResetBlock
	alice, _ := db.AddUser(1, 0, 0, "")
	bob, _ := db.AddUser(2, 0, 0, "")

	type key struct {
		endpoint string
		user     UserID
	}
	// Each step acts on what the steps before it left
	steps := []struct {
		name     string
		act      func(endpoint string, user UserID)
		endpoint string
		user     UserID
		want     map[key]int
	}{
		{"first block", increment, "ep1", bob, map[key]int{{"ep1", bob}: 1}},
		{"second block", increment, "ep1", bob, map[key]int{{"ep1", bob}: 2}},
		{
			"another endpoint counts apart",
			increment,
			"ep2",
			bob,
			map[key]int{{"ep1", bob}: 2, {"ep2", bob}: 1},
		},
		{
			"another user counts apart",
			increment,
			"ep1",
			alice,
			map[key]int{{"ep1", alice}: 1, {"ep1", bob}: 2, {"ep2", bob}: 1},
		},
		{
			"reset leaves another endpoint and another user",
			reset,
			"ep1",
			bob,
			map[key]int{{"ep1", alice}: 1, {"ep1", bob}: 0, {"ep2", bob}: 1},
		},
	}
	for _, tt := range steps {
		tt.act(tt.endpoint, tt.user)
		t.Run(tt.name, func(t *testing.T) {
			got := map[key]int{}
			var k key
			var block int
			db.MustQuery(
				"select endpoint, user_id, block from block",
				nil,
				ScanTo{&k.endpoint, &k.user, &block},
				func() { got[k] = block })
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("block counts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNotificationsStorage(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	db.AddUser(1, 3, 0, "private")
	db.AddUser(2, 3, 0, "group")
	db.AddUser(3, 3, 0, "supergroup")
	db.AddUser(4, 3, 0, "")
	db.setReports(1, 7)
	db.setReports(2, 8)
	db.setReports(3, 9)
	db.SetSilentMessages(1, true)
	db.SetAffiliateParams(1, map[string]string{"campaign": "a"})
	db.SetAffiliateParams(2, map[string]string{"campaign": "b"})
	db.SetAffiliateParams(3, map[string]string{"campaign": "c"})

	idA := insertStreamerRow(db.Database, Streamer{Nickname: "a"})
	idB := insertStreamerRow(db.Database, Streamer{Nickname: "b"})
	idC := insertStreamerRow(db.Database, Streamer{Nickname: "c"})
	idD := insertStreamerRow(db.Database, Streamer{Nickname: "d"})

	// Each batch fetches only what it stored.
	// Each flag has its own pattern across the rows, so swapping any two shows.
	tests := []struct {
		name string
		want []Notification
	}{
		{
			name: "first batch",
			want: []Notification{
				{
					ID:              1,
					Endpoint:        "endpoint_a",
					UserID:          1,
					StreamerID:      &idA,
					Nickname:        "a",
					Status:          cmdlib.StatusOnline,
					TimeDiff:        new(2),
					ImageURL:        "image_a",
					ProfileImageURL: "profile_a",
					Viewers:         new(10),
					ShowKind:        cmdlib.ShowPublic,
					Social:          true,
					Priority:        PriorityLow,
					Kind:            ReplyPacket,
					Command:         "add",
					ReplySeq:        4,
					FieldsHint:      true,
					Subject:         "subject_a",
					SilentMessages:  true,
					ChatID:          1,
					ChatType:        new("private"),
					AffiliateParams: map[string]string{"campaign": "a"},
					Reports:         7,
				},
				{
					ID:              2,
					Endpoint:        "endpoint_b",
					UserID:          2,
					StreamerID:      &idB,
					Nickname:        "b",
					Status:          cmdlib.StatusOffline,
					TimeDiff:        new(3),
					ImageURL:        "image_b",
					ProfileImageURL: "profile_b",
					MissingImage:    true,
					Viewers:         new(20),
					ShowKind:        cmdlib.ShowPrivate,
					Sound:           true,
					Priority:        PriorityHigh,
					Kind:            AdPacket,
					Command:         "pics",
					ReplySeq:        5,
					FieldsHint:      true,
					Subject:         "subject_b",
					ChatID:          2,
					ChatType:        new("group"),
					AffiliateParams: map[string]string{"campaign": "b"},
					Reports:         8,
				},
			},
		},
		{
			name: "second batch",
			want: []Notification{
				{
					ID:              3,
					Endpoint:        "endpoint_c",
					UserID:          3,
					StreamerID:      &idC,
					Nickname:        "c",
					Status:          cmdlib.StatusOnline,
					TimeDiff:        new(6),
					ImageURL:        "image_c",
					ProfileImageURL: "profile_c",
					Viewers:         new(30),
					ShowKind:        cmdlib.ShowGroup,
					Social:          true,
					Sound:           true,
					Priority:        PriorityLow,
					Kind:            MessagePacket,
					Command:         "online",
					ReplySeq:        1,
					Subject:         "subject_c",
					ChatID:          3,
					ChatType:        new("supergroup"),
					AffiliateParams: map[string]string{"campaign": "c"},
					Reports:         9,
				},
				// Nothing optional: a null time, viewers and command, and no chat type
				{
					ID:         4,
					Endpoint:   "endpoint_d",
					UserID:     4,
					StreamerID: &idD,
					Nickname:   "d",
					ChatID:     4,
				},
			},
		},
	}
	for _, tt := range tests {
		// The queue assigns the id, and the read joins the recipient's chat
		stored := slices.Clone(tt.want)
		for i := range stored {
			stored[i].ID, stored[i].ChatID, stored[i].ChatType = 0, 0, nil
			stored[i].SilentMessages, stored[i].AffiliateParams, stored[i].Reports = false, nil, 0
		}
		db.StoreNotifications(stored)
		got := db.NewNotifications()
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewNotifications = %s, want %s", asJSON(got), asJSON(tt.want))
			}
		})
	}
	if count := db.MustInt("select count(*) from notification_queue"); count != 4 {
		t.Errorf("unexpected notifications count %d", count)
	}
	if count := db.MustInt("select count(*) from notification_queue where command is null"); count != 1 {
		t.Errorf("%d notifications store a null command, want the 1 with none", count)
	}
}

func TestUnconfirmedStatusConsistency(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// Each step changes the status the step before it left
	steps := []struct {
		name       string
		status     cmdlib.StatusKind
		timestamp  int
		wantPrev   cmdlib.StatusKind
		wantPrevAt int
	}{
		{"first change has no previous", cmdlib.StatusOnline, 100, cmdlib.StatusUnknown, 0},
		{"second change keeps the first", cmdlib.StatusOffline, 200, cmdlib.StatusOnline, 100},
		{"third change shifts them", cmdlib.StatusOnline, 300, cmdlib.StatusOffline, 200},
	}
	for _, tt := range steps {
		db.UpsertUnconfirmedStatusChanges([]StatusChange{{Nickname: "a", Status: tt.status}}, tt.timestamp)
		t.Run(tt.name, func(t *testing.T) {
			s := db.MaybeStreamer("a")
			if s == nil {
				t.Fatal("streamer not found")
			}
			if s.UnconfirmedStatus != tt.status || s.UnconfirmedTimestamp != tt.timestamp {
				t.Errorf("unexpected unconfirmed status: %+v", s)
			}
			if s.PrevUnconfirmedStatus != tt.wantPrev || s.PrevUnconfirmedTimestamp != tt.wantPrevAt {
				t.Errorf("unexpected prev unconfirmed status: %+v", s)
			}
			if n := db.MustInt("select count(*) from nicknames where nickname = $1", "a"); n != 1 {
				t.Errorf("%d nicknames rows, want 1", n)
			}
		})
	}
}

// TestUpsertUnconfirmedStatusChangesBatch gives each streamer of a call its own status and row
func TestUpsertUnconfirmedStatusChangesBatch(t *testing.T) {
	t.Parallel()
	const (
		unknown = cmdlib.StatusUnknown
		offline = cmdlib.StatusOffline
		online  = cmdlib.StatusOnline
	)
	db := newTestDB(t)
	db.UpsertUnconfirmedStatusChanges(
		[]StatusChange{
			{Nickname: "a", Status: online},
			{Nickname: "b", Status: offline},
			{Nickname: "c", Status: unknown},
		},
		100)
	db.UpsertUnconfirmedStatusChanges(
		[]StatusChange{
			{Nickname: "a", Status: offline},
			{Nickname: "b", Status: online},
			{Nickname: "d", Status: online},
		},
		200)

	tests := []struct {
		nickname string
		want     []statusChangeRow
	}{
		{"a", []statusChangeRow{{100, online, unknown}, {200, offline, online}}},
		{"b", []statusChangeRow{{100, offline, unknown}, {200, online, offline}}},
		{"c", []statusChangeRow{{100, unknown, unknown}}},
		{"d", []statusChangeRow{{200, online, unknown}}},
	}
	for _, tt := range tests {
		t.Run(tt.nickname, func(t *testing.T) {
			got := streamerChanges(db.Database, streamerID(t, db.Database, tt.nickname))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("status_changes rows = %+v, want %+v", got, tt.want)
			}
			checkStatusChangesConsistent(t, db.Database, tt.nickname)
			if n := db.MustInt("select count(*) from nicknames where nickname = $1", tt.nickname); n != 1 {
				t.Errorf("%d nicknames rows, want 1", n)
			}
		})
	}
}

func TestConfirmAndDenySub(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		streamerExists bool
		// act returns the streamer id a confirmation gives, 0 for a denial
		act          func(d *Database, sub PendingSubscription) int
		wantStreamer bool
	}{
		{
			name:         "confirm",
			act:          func(d *Database, sub PendingSubscription) int { return d.ConfirmSub(sub) },
			wantStreamer: true,
		},
		{
			name:           "confirm a known streamer",
			streamerExists: true,
			act:            func(d *Database, sub PendingSubscription) int { return d.ConfirmSub(sub) },
			wantStreamer:   true,
		},
		{
			name: "deny",
			act: func(d *Database, sub PendingSubscription) int {
				d.DenySub(sub)
				return 0
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			userID, _ := db.AddUser(1, 0, 0, "")
			db.AddUser(2, 0, 0, "")
			db.addPending(1, "a", "test")
			// Each differs from the one acted on in a single column
			db.addPending(2, "a", "test")
			db.addPending(1, "b", "test")
			db.addPending(1, "a", "other")
			if tt.streamerExists {
				// First, so a fallback not filtered by nickname returns this streamer
				insertStreamerRow(db.Database, Streamer{Nickname: "other"})
				insertStreamerRow(db.Database, Streamer{Nickname: "a"})
			}

			id := tt.act(db.Database, PendingSubscription{Endpoint: "test", UserID: userID, Nickname: "a"})

			pending := db.MustStrings(`
				select u.chat_id || ' ' || ps.endpoint || ' ' || ps.nickname
				from pending_subscriptions ps
				join users u on u.id = ps.user_id
				order by 1`)
			if want := []string{"1 other a", "1 test b", "2 test a"}; !slices.Equal(pending, want) {
				t.Errorf("pending subscriptions = %q, want %q", pending, want)
			}
			streamer := db.MaybeStreamer("a")
			if (streamer != nil) != tt.wantStreamer {
				t.Fatalf("streamer = %+v, want one: %v", streamer, tt.wantStreamer)
			}
			wantRows := 0
			if tt.wantStreamer {
				wantRows = 1
				if id != streamer.ID {
					t.Errorf("returned id = %d, want the streamer's %d", id, streamer.ID)
				}
			}
			if n := db.MustInt("select count(*) from subscriptions"); n != wantRows {
				t.Errorf("%d subscriptions, want %d", n, wantRows)
			}
			if n := db.MustInt("select count(*) from nicknames where nickname = $1", "a"); n != wantRows {
				t.Errorf("%d nicknames, want %d", n, wantRows)
			}
		})
	}
}

func TestStatusConfirmations(t *testing.T) {
	t.Parallel()
	const now, onlineSeconds, offlineSeconds = 105, 3, 5
	const (
		unknown = cmdlib.StatusUnknown
		offline = cmdlib.StatusOffline
		online  = cmdlib.StatusOnline
	)
	tests := []struct {
		name        string
		confirmed   cmdlib.StatusKind
		unconfirmed cmdlib.StatusKind
		timestamp   int
		wantChange  bool
	}{
		{"offline to online confirms at its threshold", offline, online, now - onlineSeconds, true},
		{"offline to online waits under its threshold", offline, online, now - onlineSeconds + 1, false},
		{"online to offline confirms at its threshold", online, offline, now - offlineSeconds, true},
		{"online to offline waits under its threshold", online, offline, now - offlineSeconds + 1, false},
		{"online to unknown confirms immediately", online, unknown, now, true},
		{"offline to unknown confirms immediately", offline, unknown, now, true},
		{"same status online no change", online, online, 100, false},
		{"same status offline no change", offline, offline, 100, false},
		{"same status unknown no change", unknown, unknown, 100, false},
	}
	db := newTestDB(t)
	var want []ConfirmedStatusChange
	for _, tt := range tests {
		id := insertStreamerRow(db.Database, Streamer{
			Nickname:             tt.name,
			ConfirmedStatus:      tt.confirmed,
			UnconfirmedStatus:    tt.unconfirmed,
			UnconfirmedTimestamp: tt.timestamp,
		})
		if tt.wantChange {
			want = append(want, ConfirmedStatusChange{
				StreamerID: id,
				Nickname:   tt.name,
				Status:     tt.unconfirmed,
				PrevStatus: tt.confirmed,
				Timestamp:  now,
			})
		}
	}

	changes := db.ConfirmStatusChanges(now, onlineSeconds, offlineSeconds)

	// The statement sets no order, and ids ascend with the rows
	slices.SortFunc(changes, func(x, y ConfirmedStatusChange) int { return cmp.Compare(x.StreamerID, y.StreamerID) })
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("confirmed %+v, want %+v", changes, want)
	}
	if again := db.ConfirmStatusChanges(now, onlineSeconds, offlineSeconds); len(again) != 0 {
		t.Errorf("confirmed %+v again, want none", again)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantConfirmed := tt.confirmed
			if tt.wantChange {
				wantConfirmed = tt.unconfirmed
			}
			if got := db.MaybeStreamer(tt.name).ConfirmedStatus; got != wantConfirmed {
				t.Errorf("confirmed_status = %v, want %v", got, wantConfirmed)
			}
		})
	}
}

// TestAddUserReportsCreation pins the created flag the referral bonus gates on:
// a new chat gets what it was given, and a known one keeps its row
func TestAddUserReportsCreation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// Each step acts on what the steps before it left
	steps := []struct {
		name          string
		chatID        int64
		maxSubs       int
		now           int
		chatType      string
		wantCreated   bool
		wantMaxSubs   int
		wantCreatedAt int
		wantChatType  string
	}{
		{"new chat", 1, 5, 1000, "private", true, 5, 1000, "private"},
		{"known chat keeps its row", 1, 9, 2000, "group", false, 5, 1000, "private"},
		{"another new chat", 2, 7, 3000, "group", true, 7, 3000, "group"},
	}
	ids := map[int64]UserID{}
	for _, tt := range steps {
		id, created := db.AddUser(tt.chatID, tt.maxSubs, tt.now, tt.chatType)
		known, wasKnown := ids[tt.chatID]
		ids[tt.chatID] = id
		t.Run(tt.name, func(t *testing.T) {
			if created != tt.wantCreated {
				t.Errorf("created = %v, want %v", created, tt.wantCreated)
			}
			if wasKnown && id != known {
				t.Errorf("id = %d, want the known %d", id, known)
			}
			var maxSubs, createdAt int
			var chatType string
			db.MaybeRecord(
				"select max_subs, created_at, chat_type from users where id = $1 and chat_id = $2",
				QueryParams{int64(id), tt.chatID},
				ScanTo{&maxSubs, &createdAt, &chatType})
			if maxSubs != tt.wantMaxSubs || createdAt != tt.wantCreatedAt || chatType != tt.wantChatType {
				t.Errorf(
					"stored (%d, %d, %q), want (%d, %d, %q)",
					maxSubs,
					createdAt,
					chatType,
					tt.wantMaxSubs,
					tt.wantCreatedAt,
					tt.wantChatType)
			}
		})
	}
}

func TestEnsureUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	before := time.Now().Unix()
	known, _ := db.AddUser(2, 9, 1000, "group")

	// Each step acts on what the steps before it left
	steps := []struct {
		name         string
		chatID       int64
		wantMaxSubs  int
		wantChatType *string
		minCreatedAt int64
		maxCreatedAt int64
	}{
		{"a new chat gets the default limit, no type and the time", 1, 5, nil, before, math.MaxInt64},
		{"the chat it created keeps its row", 1, 5, nil, before, math.MaxInt64},
		{"a known chat keeps its row", 2, 9, new("group"), 1000, 1000},
	}
	ids := map[int64]UserID{2: known}
	for _, tt := range steps {
		id := db.EnsureUser(tt.chatID)
		known, wasKnown := ids[tt.chatID]
		ids[tt.chatID] = id
		t.Run(tt.name, func(t *testing.T) {
			if wasKnown && id != known {
				t.Errorf("id = %d, want the known %d", id, known)
			}
			user, found := db.UserByID(id)
			if !found || user.ChatID != tt.chatID || user.MaxSubs != tt.wantMaxSubs ||
				!reflect.DeepEqual(user.ChatType, tt.wantChatType) ||
				user.CreatedAt < tt.minCreatedAt || user.CreatedAt > tt.maxCreatedAt {
				t.Errorf("user = %s, found: %v", asJSON(user), found)
			}
			if n := db.MustInt("select count(*) from users"); n != 2 {
				t.Errorf("%d users, want 2", n)
			}
		})
	}
}

// userRow reads a user's whole row past the readers under test; numbers come as float64
func userRow(d *Database, id UserID) map[string]any {
	var row map[string]any
	d.MaybeRecord("select to_jsonb(u) from users u where id = $1", QueryParams{int64(id)}, ScanTo{&row})
	return row
}

// TestUserSettersWriteTheirColumn pins each setter to its column on its user alone
func TestUserSettersWriteTheirColumn(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	target, _ := db.AddUser(1, 5, 1000, "private")
	bystander, _ := db.AddUser(2, 5, 1000, "private")
	// So a step clearing these on every user shows
	db.SetTimezone(bystander, "Asia/Tokyo")
	db.SetAffiliateParams(bystander, map[string]string{"campaign": "b"})

	// Each step acts on what the steps before it left
	steps := []struct {
		name   string
		set    func()
		column string
		want   any
	}{
		{"SetLimit", func() { db.SetLimit(target, 42) }, "max_subs", 42.0},
		{"AddReferrerBonus", func() { db.AddReferrerBonus(target, 3) }, "max_subs", 45.0},
		{"SetShowImages off", func() { db.SetShowImages(target, false) }, "show_images", false},
		{"SetShowImages on", func() { db.SetShowImages(target, true) }, "show_images", true},
		{
			"SetOfflineNotifications off",
			func() { db.SetOfflineNotifications(target, false) },
			"offline_notifications",
			false,
		},
		{
			"SetOfflineNotifications on",
			func() { db.SetOfflineNotifications(target, true) },
			"offline_notifications",
			true,
		},
		{"SetShowSubject off", func() { db.SetShowSubject(target, false) }, "show_subject", false},
		{"SetShowSubject on", func() { db.SetShowSubject(target, true) }, "show_subject", true},
		{"SetSilentMessages on", func() { db.SetSilentMessages(target, true) }, "silent_messages", true},
		{"SetSilentMessages off", func() { db.SetSilentMessages(target, false) }, "silent_messages", false},
		{
			"SetMemberSubscriptions on",
			func() { db.SetMemberSubscriptions(target, true) },
			"member_subscriptions",
			true,
		},
		{
			"SetMemberSubscriptions off",
			func() { db.SetMemberSubscriptions(target, false) },
			"member_subscriptions",
			false,
		},
		{"BlacklistUser", func() { db.BlacklistUser(target) }, "blacklist", true},
		{"SetTimezone", func() { db.SetTimezone(target, "Europe/Berlin") }, "timezone", "Europe/Berlin"},
		{"SetTimezone clears on empty", func() { db.SetTimezone(target, "") }, "timezone", nil},
		{
			"SetAffiliateParams",
			func() { db.SetAffiliateParams(target, map[string]string{"campaign": "a"}) },
			"affiliate_params",
			map[string]any{"campaign": "a"},
		},
		{"SetAffiliateParams clears on empty", func() { db.SetAffiliateParams(target, nil) }, "affiliate_params", nil},
		{"UpdateMemberCount", func() { db.UpdateMemberCount(target, 7) }, "member_count", 7.0},
	}
	for _, tt := range steps {
		wantTarget, wantBystander := userRow(db.Database, target), userRow(db.Database, bystander)
		wantTarget[tt.column] = tt.want
		tt.set()
		t.Run(tt.name, func(t *testing.T) {
			if got := userRow(db.Database, target); !reflect.DeepEqual(got, wantTarget) {
				t.Errorf("target = %v, want %v", got, wantTarget)
			}
			if got := userRow(db.Database, bystander); !reflect.DeepEqual(got, wantBystander) {
				t.Errorf("bystander = %v, want %v", got, wantBystander)
			}
		})
	}
}

func TestIncrementReportsPerUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	a, _ := db.AddUser(1, 5, 0, "")
	b, _ := db.AddUser(2, 5, 0, "")
	c, _ := db.AddUser(3, 5, 0, "")

	db.IncrementReports([]UserID{a, b, a})
	db.IncrementReports([]UserID{a})

	got := map[UserID]int{}
	var id UserID
	var reports int
	db.MustQuery("select id, reports from users", nil, ScanTo{&id, &reports}, func() { got[id] = reports })
	if want := (map[UserID]int{a: 3, b: 1, c: 0}); !reflect.DeepEqual(got, want) {
		t.Errorf("reports = %v, want %v", got, want)
	}
}

// TestUserReadersReadEveryColumn holds both user readers to the row stored
func TestUserReadersReadEveryColumn(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// Each flag has its own pattern across the rows, so swapping any two shows
	users := []User{
		{
			ChatID:               1,
			MaxSubs:              11,
			Reports:              21,
			Blacklist:            true,
			OfflineNotifications: true,
			SilentMessages:       true,
			CreatedAt:            31,
			ChatType:             new("private"),
			MemberCount:          new(41),
			AffiliateParams:      map[string]string{"campaign": "a"},
			Timezone:             new("Europe/Berlin"),
		},
		{
			ChatID:               2,
			MaxSubs:              12,
			Reports:              22,
			ShowImages:           true,
			OfflineNotifications: true,
			CreatedAt:            32,
			ChatType:             new("group"),
			MemberCount:          new(42),
			AffiliateParams:      map[string]string{"campaign": "b"},
			MemberSubscriptions:  true,
			Timezone:             new("Asia/Tokyo"),
		},
		// No nullable column set
		{
			ChatID:              3,
			MaxSubs:             13,
			Reports:             23,
			ShowSubject:         true,
			SilentMessages:      true,
			CreatedAt:           33,
			MemberSubscriptions: true,
		},
	}
	for i, u := range users {
		users[i].UserID = UserID(db.MustInt(`
			insert into users (
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
				member_count,
				affiliate_params,
				member_subscriptions,
				timezone)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			returning id`,
			u.ChatID,
			u.MaxSubs,
			u.Reports,
			u.Blacklist,
			u.ShowImages,
			u.OfflineNotifications,
			u.ShowSubject,
			u.SilentMessages,
			u.CreatedAt,
			u.ChatType,
			u.MemberCount,
			u.AffiliateParams,
			u.MemberSubscriptions,
			u.Timezone))
	}

	readers := []struct {
		name string
		read func(u User) (User, bool)
	}{
		{"UserByID", func(u User) (User, bool) { return db.UserByID(u.UserID) }},
		{"UserByChatID", func(u User) (User, bool) { return db.UserByChatID(u.ChatID) }},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			for _, want := range users {
				if got, found := reader.read(want); !found || !reflect.DeepEqual(got, want) {
					t.Errorf("read %s, found: %v, want %s", asJSON(got), found, asJSON(want))
				}
			}
			if got, found := reader.read(User{UserID: 999, ChatID: 999}); found {
				t.Errorf("read %s of an absent user", asJSON(got))
			}
		})
	}
}

func TestSubscriptionReads(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	alice, _ := db.AddUser(1, 5, 0, "private")
	bob, _ := db.AddUser(2, 5, 0, "private")
	a := Streamer{
		Nickname:                 "a",
		UnconfirmedStatus:        cmdlib.StatusOnline,
		UnconfirmedTimestamp:     400,
		PrevUnconfirmedStatus:    cmdlib.StatusOffline,
		PrevUnconfirmedTimestamp: 300,
	}
	b := Streamer{
		Nickname:                 "b",
		UnconfirmedStatus:        cmdlib.StatusOffline,
		UnconfirmedTimestamp:     600,
		PrevUnconfirmedStatus:    cmdlib.StatusOnline,
		PrevUnconfirmedTimestamp: 500,
	}
	c := Streamer{
		Nickname:                 "c",
		UnconfirmedStatus:        cmdlib.StatusUnknown,
		UnconfirmedTimestamp:     800,
		PrevUnconfirmedStatus:    cmdlib.StatusOffline,
		PrevUnconfirmedTimestamp: 700,
	}
	// Out of nickname order, so the rows' stored order can't stand in for a sort
	c.ID = insertStreamerRow(db.Database, c)
	b.ID = insertStreamerRow(db.Database, b)
	a.ID = insertStreamerRow(db.Database, a)
	insertStreamerRow(db.Database, Streamer{Nickname: "nobody's", UnconfirmedStatus: cmdlib.StatusOnline})
	// Added out of order, and with a nickname both subscribed and pending
	db.AddSubscription(alice, b.ID, "ep1")
	db.AddSubscription(alice, a.ID, "ep1")
	db.AddSubscription(alice, c.ID, "ep2")
	db.AddSubscription(bob, c.ID, "ep1")
	db.AddPendingSubscription(alice, "aa", "ep1", false, "", 0)
	db.AddPendingSubscription(alice, "a", "ep1", false, "", 0)
	db.AddPendingSubscription(alice, "q", "ep2", false, "", 0)
	db.AddPendingSubscription(bob, "r", "ep1", false, "", 0)

	lists := []struct {
		name          string
		endpoint      string
		user          UserID
		wantStatuses  []Streamer
		wantNicknames []string
		wantCount     int
	}{
		{"alice on ep1", "ep1", alice, []Streamer{a, b}, []string{"a", "aa", "b"}, 4},
		{"alice on ep2", "ep2", alice, []Streamer{c}, []string{"c", "q"}, 2},
		{"bob on ep1", "ep1", bob, []Streamer{c}, []string{"c", "r"}, 2},
		{"bob on ep2", "ep2", bob, nil, nil, 0},
	}
	// So a union hashes, as its own sort could stand in for the order by
	db.MustExec("set enable_sort = off")
	for _, tt := range lists {
		t.Run(tt.name, func(t *testing.T) {
			var wantStreamers []Streamer
			for _, s := range tt.wantStatuses {
				wantStreamers = append(wantStreamers, Streamer{ID: s.ID, Nickname: s.Nickname})
			}
			if got := db.StreamersForUser(tt.endpoint, tt.user); !reflect.DeepEqual(got, wantStreamers) {
				t.Errorf("StreamersForUser = %+v, want %+v", got, wantStreamers)
			}
			if got := db.UnconfirmedStatusesForUser(tt.endpoint, tt.user); !reflect.DeepEqual(got, tt.wantStatuses) {
				t.Errorf("UnconfirmedStatusesForUser = %+v, want %+v", got, tt.wantStatuses)
			}
			if got := db.SubscribedOrPendingNicknames(tt.endpoint, tt.user); !reflect.DeepEqual(got, tt.wantNicknames) {
				t.Errorf("SubscribedOrPendingNicknames = %v, want %v", got, tt.wantNicknames)
			}
			if got := db.SubscribedOrPendingCount(tt.endpoint, tt.user); got != tt.wantCount {
				t.Errorf("SubscribedOrPendingCount = %d, want %d", got, tt.wantCount)
			}
		})
	}

	lookups := []struct {
		name     string
		endpoint string
		user     UserID
		nickname string
		want     bool
	}{
		{"subscribed", "ep1", alice, "b", true},
		{"pending", "ep1", alice, "aa", true},
		{"subscribed on another endpoint", "ep2", alice, "b", false},
		{"pending on another endpoint", "ep2", alice, "aa", false},
		{"another user's subscription", "ep1", bob, "b", false},
		{"another user's pending", "ep1", alice, "r", false},
		{"nobody's", "ep1", alice, "nobody's", false},
	}
	for _, tt := range lookups {
		t.Run("SubscribedOrPending "+tt.name, func(t *testing.T) {
			if got := db.SubscribedOrPending(tt.endpoint, tt.user, tt.nickname); got != tt.want {
				t.Errorf("SubscribedOrPending = %v, want %v", got, tt.want)
			}
		})
	}

	want := map[string]bool{"a": true, "b": true, "c": true}
	if got := db.SubscribedStreamers(); !reflect.DeepEqual(got, want) {
		t.Errorf("SubscribedStreamers = %v, want %v", got, want)
	}
}

func TestRemoveSubscriptions(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	alice, _ := db.AddUser(1, 5, 0, "private")
	db.AddUser(2, 5, 0, "private")
	db.addSub(1, "a", "ep1")
	db.addSub(1, "b", "ep1")
	db.addSub(1, "a", "ep2")
	db.addSub(2, "a", "ep1")
	db.addSub(2, "a", "ep2")
	db.addPending(1, "p", "ep1")
	db.addPending(1, "q", "ep1")
	db.addPending(1, "p", "ep2")
	db.addPending(2, "p", "ep1")
	db.addPending(2, "p", "ep2")

	subs := func() []string {
		return db.MustStrings(`
			select u.chat_id || ' ' || sub.endpoint || ' ' || s.nickname
			from subscriptions sub
			join users u on u.id = sub.user_id
			join streamers s on s.id = sub.streamer_id
			order by 1`)
	}
	pending := func() []string {
		return db.MustStrings(`
			select u.chat_id || ' ' || ps.endpoint || ' ' || ps.nickname
			from pending_subscriptions ps
			join users u on u.id = ps.user_id
			order by 1`)
	}

	// Each step acts on what the steps before it left
	steps := []struct {
		name        string
		act         func()
		wantSubs    []string
		wantPending []string
	}{
		{
			"RemoveSubscription takes a subscription",
			func() { db.RemoveSubscription(alice, "a", "ep1") },
			[]string{"1 ep1 b", "1 ep2 a", "2 ep1 a", "2 ep2 a"},
			[]string{"1 ep1 p", "1 ep1 q", "1 ep2 p", "2 ep1 p", "2 ep2 p"},
		},
		{
			"RemoveSubscription takes a pending one",
			func() { db.RemoveSubscription(alice, "p", "ep1") },
			[]string{"1 ep1 b", "1 ep2 a", "2 ep1 a", "2 ep2 a"},
			[]string{"1 ep1 q", "1 ep2 p", "2 ep1 p", "2 ep2 p"},
		},
		{
			"RemoveAllSubscriptions takes one endpoint of one user",
			func() { db.RemoveAllSubscriptions(alice, "ep2") },
			[]string{"1 ep1 b", "2 ep1 a", "2 ep2 a"},
			[]string{"1 ep1 q", "2 ep1 p", "2 ep2 p"},
		},
	}
	for _, tt := range steps {
		tt.act()
		t.Run(tt.name, func(t *testing.T) {
			if got := subs(); !reflect.DeepEqual(got, tt.wantSubs) {
				t.Errorf("subscriptions = %q, want %q", got, tt.wantSubs)
			}
			if got := pending(); !reflect.DeepEqual(got, tt.wantPending) {
				t.Errorf("pending subscriptions = %q, want %q", got, tt.wantPending)
			}
		})
	}
}

// TestPendingSubscriptionChecks walks pending subscriptions through a check
func TestPendingSubscriptionChecks(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	alice, _ := db.AddUser(1, 5, 0, "private")
	bob, _ := db.AddUser(-2, 5, 0, "group")
	aliceOnA := PendingSubscription{
		UserID:   alice,
		Nickname: "a",
		Endpoint: "ep1",
		ChatID:   1,
		Referral: true,
		Command:  "add",
		ReplySeq: 3,
	}
	bobOnA := PendingSubscription{UserID: bob, Nickname: "a", Endpoint: "ep2", ChatID: -2}
	aliceOnB := PendingSubscription{UserID: alice, Nickname: "b", Endpoint: "ep1", ChatID: 1, Command: "start"}
	bobOnC := PendingSubscription{UserID: bob, Nickname: "c", Endpoint: "ep2", ChatID: -2, ReplySeq: 1}
	add := func(subs ...PendingSubscription) func() {
		return func() {
			for _, s := range subs {
				db.AddPendingSubscription(s.UserID, s.Nickname, s.Endpoint, s.Referral, s.Command, s.ReplySeq)
			}
		}
	}

	// Each step acts on what the steps before it left
	steps := []struct {
		name            string
		act             func()
		wantUnconfirmed map[string]bool
		wantChecking    map[string][]PendingSubscription
	}{
		{
			"new ones wait for a check",
			add(aliceOnA, bobOnA, aliceOnB),
			map[string]bool{"a": true, "b": true},
			map[string][]PendingSubscription{},
		},
		{
			"marking puts them under check",
			db.MarkUnconfirmedAsChecking,
			map[string]bool{},
			map[string][]PendingSubscription{"a": {aliceOnA, bobOnA}, "b": {aliceOnB}},
		},
		{
			"one added later waits alone",
			add(bobOnC),
			map[string]bool{"c": true},
			map[string][]PendingSubscription{"a": {aliceOnA, bobOnA}, "b": {aliceOnB}},
		},
		{
			"a reset brings them all back",
			db.ResetCheckingToUnconfirmed,
			map[string]bool{"a": true, "b": true, "c": true},
			map[string][]PendingSubscription{},
		},
	}
	checking := func(nicknames ...string) map[string][]PendingSubscription {
		subs := db.CheckingSubs(nicknames)
		for _, s := range subs {
			slices.SortFunc(s, func(x, y PendingSubscription) int { return cmp.Compare(x.UserID, y.UserID) })
		}
		return subs
	}
	for _, tt := range steps {
		tt.act()
		t.Run(tt.name, func(t *testing.T) {
			if got := db.UnconfirmedSubs(); !reflect.DeepEqual(got, tt.wantUnconfirmed) {
				t.Errorf("UnconfirmedSubs = %v, want %v", got, tt.wantUnconfirmed)
			}
			if got := checking("a", "b", "c"); !reflect.DeepEqual(got, tt.wantChecking) {
				t.Errorf("CheckingSubs = %+v, want %+v", got, tt.wantChecking)
			}
		})
	}

	db.MarkUnconfirmedAsChecking()
	want := map[string][]PendingSubscription{"b": {aliceOnB}}
	if got := checking("b"); !reflect.DeepEqual(got, want) {
		t.Errorf("CheckingSubs of one nickname = %+v, want %+v", got, want)
	}
	if n := db.MustInt("select count(*) from pending_subscriptions where command is null"); n != 2 {
		t.Errorf("%d pending subscriptions store a null command, want the 2 with none", n)
	}
}

func TestStreamerReads(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// The confirmed status differs from the unconfirmed one, which alone decides these reads
	a := Streamer{
		Nickname:                 "a",
		ConfirmedStatus:          cmdlib.StatusOffline,
		UnconfirmedStatus:        cmdlib.StatusOnline,
		UnconfirmedTimestamp:     400,
		PrevUnconfirmedStatus:    cmdlib.StatusUnknown,
		PrevUnconfirmedTimestamp: 300,
	}
	b := Streamer{
		Nickname:                 "b",
		ConfirmedStatus:          cmdlib.StatusOnline,
		UnconfirmedStatus:        cmdlib.StatusOffline,
		UnconfirmedTimestamp:     600,
		PrevUnconfirmedStatus:    cmdlib.StatusUnknown,
		PrevUnconfirmedTimestamp: 500,
	}
	c := Streamer{
		Nickname:                 "c",
		ConfirmedStatus:          cmdlib.StatusOnline,
		UnconfirmedStatus:        cmdlib.StatusUnknown,
		UnconfirmedTimestamp:     800,
		PrevUnconfirmedStatus:    cmdlib.StatusOnline,
		PrevUnconfirmedTimestamp: 700,
	}
	a.ID = insertStreamerRow(db.Database, a)
	b.ID = insertStreamerRow(db.Database, b)
	c.ID = insertStreamerRow(db.Database, c)
	db.SetPoll("b", true)
	db.SetPoll("a", true)

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"OnlineStreamers", db.OnlineStreamers(), map[string]bool{"a": true}},
		{"KnownStreamers", db.KnownStreamers(), map[string]bool{"a": true, "b": true}},
		{"PolledStreamersWithStatus", db.PolledStreamersWithStatus(), []Streamer{a, b}},
		{
			"UnconfirmedStatusesForStreamers",
			db.UnconfirmedStatusesForStreamers([]string{"a", "c", "absent"}),
			map[string]StatusChange{
				"a": {Nickname: "a", Status: cmdlib.StatusOnline, Timestamp: 400},
				"c": {Nickname: "c", Status: cmdlib.StatusUnknown, Timestamp: 800},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Errorf("got %+v, want %+v", tt.got, tt.want)
			}
		})
	}
}

func TestIncrementPollErrors(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	for _, nickname := range []string{"a", "b", "c"} {
		db.ensureStreamer(nickname)
	}

	db.IncrementPollErrors([]string{"a", "b"})
	db.IncrementPollErrors([]string{"a", "absent"})

	got := map[string]int{}
	var nickname string
	var errorCount int
	db.MustQuery(
		"select nickname, poll_error_count from streamers",
		nil,
		ScanTo{&nickname, &errorCount},
		func() { got[nickname] = errorCount })
	if want := (map[string]int{"a": 2, "b": 1, "c": 0}); !reflect.DeepEqual(got, want) {
		t.Errorf("poll errors = %v, want %v", got, want)
	}
}

func TestUsersForStreamers(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// Each flag has its own pattern across the users, so swapping any two shows
	users := []User{
		{ChatID: 1, OfflineNotifications: true, ShowImages: true},
		{ChatID: 2, OfflineNotifications: true, ShowSubject: true},
		{ChatID: 3, ShowImages: true, ShowSubject: true},
	}
	for i, u := range users {
		users[i].UserID, _ = db.AddUser(u.ChatID, 5, 0, "private")
		db.SetOfflineNotifications(users[i].UserID, u.OfflineNotifications)
		db.SetShowImages(users[i].UserID, u.ShowImages)
		db.SetShowSubject(users[i].UserID, u.ShowSubject)
	}
	db.addSub(1, "a", "ep1")
	db.addSub(1, "a", "ep2")
	db.addSub(1, "b", "ep1")
	db.addSub(2, "a", "ep1")
	db.addSub(3, "c", "ep1")
	db.addBlock(1, "ep1", 3)
	// On an endpoint this user has no subscription on
	db.addBlock(2, "ep2", 9)
	a, b := db.MaybeStreamer("a").ID, db.MaybeStreamer("b").ID

	type recipient struct {
		User
		Endpoint string
	}
	blockedOnEP1 := users[0]
	blockedOnEP1.Blocked = 3
	want := map[int][]recipient{
		a: {{blockedOnEP1, "ep1"}, {users[0], "ep2"}, {users[1], "ep1"}},
		b: {{blockedOnEP1, "ep1"}},
	}

	gotUsers, gotEndpoints := db.UsersForStreamers([]int{a, b})

	got := map[int][]recipient{}
	for id := range gotUsers {
		for i, u := range gotUsers[id] {
			got[id] = append(got[id], recipient{u, gotEndpoints[id][i]})
		}
		slices.SortFunc(got[id], func(x, y recipient) int {
			return cmp.Or(cmp.Compare(x.ChatID, y.ChatID), cmp.Compare(x.Endpoint, y.Endpoint))
		})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recipients = %s, want %s", asJSON(got), asJSON(want))
	}
}

// TestNotificationQueueStates walks queued notifications through fetches, requeues and deletes
func TestNotificationQueueStates(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	userID, _ := db.AddUser(1, 5, 0, "private")
	streamerID := insertStreamerRow(db.Database, Streamer{Nickname: "a"})
	db.StoreNotifications([]Notification{
		{Endpoint: "ep", UserID: userID, StreamerID: &streamerID},
		{Endpoint: "ep", UserID: userID, StreamerID: &streamerID},
		{Endpoint: "ep", UserID: userID, StreamerID: &streamerID},
	})
	ids := db.MustInts("select id from notification_queue order by id")
	first, second, third := ids[0], ids[1], ids[2]

	// Each step acts on what the steps before it left
	steps := []struct {
		name string
		act  func()
		// wantFetched is what the fetch after the act returns
		wantFetched []int
		wantQueued  []int
	}{
		{"a fetch takes them all", func() {}, []int{first, second, third}, []int{first, second, third}},
		{"a fetch finds none while they are sent", func() {}, nil, []int{first, second, third}},
		{
			"RequeueNotification puts one back",
			func() { db.RequeueNotification(second) },
			[]int{second},
			[]int{first, second, third},
		},
		{"DeleteNotification removes one", func() { db.DeleteNotification(first) }, nil, []int{second, third}},
		{
			"ResetNotificationSending puts all back",
			db.ResetNotificationSending,
			[]int{second, third},
			[]int{second, third},
		},
	}
	for _, tt := range steps {
		tt.act()
		var fetched []int
		for _, n := range db.NewNotifications() {
			fetched = append(fetched, n.ID)
		}
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(fetched, tt.wantFetched) {
				t.Errorf("fetched %v, want %v", fetched, tt.wantFetched)
			}
			got := db.MustInts("select id from notification_queue order by id")
			if !reflect.DeepEqual(got, tt.wantQueued) {
				t.Errorf("queued %v, want %v", got, tt.wantQueued)
			}
		})
	}
}

func TestSentMessageLog(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	a, _ := db.AddUser(1, 5, 0, "")
	b, _ := db.AddUser(2, 5, 0, "")

	type row struct {
		Timestamp int
		UserID    UserID
		Result    int
		Endpoint  string
		Priority  Priority
		Latency   int
		Kind      PacketKind
		Command   *string
		ReplySeq  int
		ImageMode ImageMode
	}
	tests := []struct {
		name      string
		log       func()
		timestamp int
		want      []row
	}{
		{
			name:      "a command is stored by name",
			log:       func() { db.LogSentMessage(1000, a, 200, "ep", PriorityLow, 77, ReplyPacket, "add", 3, ImageUpload) },
			timestamp: 1000,
			want:      []row{{1000, a, 200, "ep", PriorityLow, 77, ReplyPacket, new("add"), 3, ImageUpload}},
		},
		{
			name:      "no command is stored as null",
			log:       func() { db.LogSentMessage(2000, b, 403, "ep2", PriorityHigh, 88, AdPacket, "", 0, NoImage) },
			timestamp: 2000,
			want:      []row{{2000, b, 403, "ep2", PriorityHigh, 88, AdPacket, nil, 0, NoImage}},
		},
		{
			name: "dice-skipped sends take zero latency and no command",
			log: func() {
				skips := []SkippedSend{{UserID: a, Endpoint: "ep"}, {UserID: b, Endpoint: "ep2"}}
				db.LogDiceSkippedSends(3000, 429, PriorityLow, MessagePacket, skips)
			},
			timestamp: 3000,
			want: []row{
				{3000, a, 429, "ep", PriorityLow, 0, MessagePacket, nil, 0, NoImage},
				{3000, b, 429, "ep2", PriorityLow, 0, MessagePacket, nil, 0, NoImage},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.log()
			var got []row
			var r row
			db.MustQuery(`
				select timestamp, user_id, result, endpoint, priority, latency, kind, command, reply_seq, image_mode
				from sent_message_log
				where timestamp = $1
				order by user_id`,
				QueryParams{tt.timestamp},
				ScanTo{
					&r.Timestamp,
					&r.UserID,
					&r.Result,
					&r.Endpoint,
					&r.Priority,
					&r.Latency,
					&r.Kind,
					&r.Command,
					&r.ReplySeq,
					&r.ImageMode,
				},
				func() { got = append(got, r) })
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sent_message_log = %s, want %s", asJSON(got), asJSON(tt.want))
			}
		})
	}
}

func TestReceivedMessageLog(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	user, _ := db.AddUser(1, 5, 0, "")

	tests := []struct {
		name      string
		timestamp int
		command   string
		want      *string
	}{
		{"a command is stored by name", 1000, "start", new("start")},
		{"no command is stored as null", 2000, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db.LogReceivedMessage(tt.timestamp, "ep", user, tt.command)
			var endpoint string
			var userID UserID
			var command *string
			found := db.MaybeRecord(
				"select endpoint, user_id, command from received_message_log where timestamp = $1",
				QueryParams{tt.timestamp},
				ScanTo{&endpoint, &userID, &command})
			if !found || endpoint != "ep" || userID != user || !reflect.DeepEqual(command, tt.want) {
				t.Errorf(
					"stored (%q, %d, %s), found: %v, want command %s",
					endpoint,
					userID,
					asJSON(command),
					found,
					asJSON(tt.want))
			}
		})
	}
}

func TestReferrals(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	alice, _ := db.AddUser(1, 5, 0, "private")
	bob, _ := db.AddUser(2, 5, 0, "private")

	if db.ReferralIDExists("alices") {
		t.Error("a referral id nobody took exists")
	}
	db.AddReferral(alice, "alices")
	db.AddReferral(bob, "bobs")
	if !db.ReferralIDExists("alices") {
		t.Error("a taken referral id does not exist")
	}
	if db.ReferralIDExists("nobodys") {
		t.Error("a referral id nobody took exists after others were taken")
	}
	if id := db.ReferralID(alice); id == nil || *id != "alices" {
		t.Errorf("ReferralID = %s, want alices", asJSON(id))
	}
	if owner := db.UserByReferralID("alices"); owner == nil || *owner != alice {
		t.Errorf("UserByReferralID = %s, want %d", asJSON(owner), alice)
	}
	if n, found := db.referredUsers(1); !found || n != 0 {
		t.Errorf("a new referral id's referred users = (%d, %v), want 0", n, found)
	}

	// Each step acts on what the steps before it left
	steps := []struct {
		name      string
		user      UserID
		wantAlice int
		wantBob   int
	}{
		{"alice refers one", alice, 1, 0},
		{"alice refers another", alice, 2, 0},
		{"bob counts apart", bob, 2, 1},
	}
	for _, tt := range steps {
		db.IncrementReferredUsers(tt.user)
		t.Run(tt.name, func(t *testing.T) {
			if n, found := db.referredUsers(1); !found || n != tt.wantAlice {
				t.Errorf("alice's referred users = (%d, %v), want %d", n, found, tt.wantAlice)
			}
			if n, found := db.referredUsers(2); !found || n != tt.wantBob {
				t.Errorf("bob's referred users = (%d, %v), want %d", n, found, tt.wantBob)
			}
		})
	}
}

func TestAddReferralEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	referrer, _ := db.AddUser(1, 5, 0, "private")
	follower, _ := db.AddUser(2, 5, 0, "private")
	// Past the users' ids, so the columns can't pass for one another
	streamerID := db.MustInt(
		"insert into streamers (id, nickname) overriding system value values (77, 'a') returning id")

	tests := []struct {
		name      string
		timestamp int
		referrer  *UserID
		streamer  *int
	}{
		{"a referrer and a streamer", 1000, &referrer, &streamerID},
		{"neither", 2000, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db.AddReferralEvent(tt.timestamp, tt.referrer, follower, tt.streamer)
			var gotReferrer *UserID
			var gotFollower UserID
			var gotStreamer *int
			found := db.MaybeRecord(
				"select referrer_user_id, follower_user_id, streamer_id from referral_events where timestamp = $1",
				QueryParams{tt.timestamp},
				ScanTo{&gotReferrer, &gotFollower, &gotStreamer})
			if !found {
				t.Fatal("no event stored at this timestamp")
			}
			if !reflect.DeepEqual(gotReferrer, tt.referrer) {
				t.Errorf("referrer_user_id = %s, want %s", asJSON(gotReferrer), asJSON(tt.referrer))
			}
			if gotFollower != follower {
				t.Errorf("follower_user_id = %d, want %d", gotFollower, follower)
			}
			if !reflect.DeepEqual(gotStreamer, tt.streamer) {
				t.Errorf("streamer_id = %s, want %s", asJSON(gotStreamer), asJSON(tt.streamer))
			}
		})
	}
}

func TestAddFeedback(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	user, _ := db.AddUser(1, 5, 0, "private")

	db.AddFeedback("ep", user, "hello", 1000)

	var endpoint, text string
	var userID UserID
	var timestamp int
	found := db.MaybeRecord(
		"select endpoint, user_id, text, timestamp from feedback",
		nil,
		ScanTo{&endpoint, &userID, &text, &timestamp})
	if !found || endpoint != "ep" || userID != user || text != "hello" || timestamp != 1000 {
		t.Errorf("stored (%q, %d, %q, %d), found: %v", endpoint, userID, text, timestamp, found)
	}
}

func TestProfilePhotoHashes(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	type photo struct{ endpoint, hash string }
	// Each step acts on what the steps before it left
	steps := []struct {
		name string
		set  photo
		want map[photo]bool
	}{
		{
			"a first upload",
			photo{"ep1", "old"},
			map[photo]bool{{"ep1", "old"}: true, {"ep1", "new"}: false, {"ep2", "old"}: false},
		},
		{
			"another endpoint keeps its own",
			photo{"ep2", "other"},
			map[photo]bool{{"ep1", "old"}: true, {"ep2", "other"}: true, {"ep2", "old"}: false},
		},
		{
			"a new upload replaces the last",
			photo{"ep1", "new"},
			map[photo]bool{{"ep1", "new"}: true, {"ep1", "old"}: false, {"ep2", "other"}: true},
		},
	}
	for _, tt := range steps {
		db.SetProfilePhotoHash(tt.set.endpoint, tt.set.hash)
		t.Run(tt.name, func(t *testing.T) {
			for p, want := range tt.want {
				if got := db.ProfilePhotoUploaded(p.endpoint, p.hash); got != want {
					t.Errorf("ProfilePhotoUploaded(%q, %q) = %v, want %v", p.endpoint, p.hash, got, want)
				}
			}
		})
	}
}

// TestSearchStreamers pins each search leg:
// nicknames holds the rows the search indexes see, streamers those the exact and prefix legs see
func TestSearchStreamers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		term      string
		nicknames []string
		streamers []string
		want      []string
	}{
		{
			name:      "an exact match in streamers alone",
			term:      "wonderland",
			nicknames: []string{"bob"},
			streamers: []string{"wonderland"},
			want:      []string{"wonderland"},
		},
		{
			name:      "a substring of a nickname",
			term:      "smi",
			nicknames: []string{"alice_smith", "bob"},
			want:      []string{"alice_smith"},
		},
		{
			name:      "an underscore in a substring is literal",
			term:      "abc_de",
			nicknames: []string{"xabc_dey", "xabcxdey"},
			want:      []string{"xabc_dey"},
		},
		{
			name:      "a typo over the similarity threshold",
			term:      "wonderwxyz",
			nicknames: []string{"wonderland", "bob"},
			want:      []string{"wonderland"},
		},
		{
			name:      "a long repeated run",
			term:      "aaaaa",
			nicknames: []string{"xaaaaay", "xaaaay"},
			want:      []string{"xaaaaay"},
		},
		{
			name:      "a long non-alnum run of literal underscores",
			term:      "___",
			nicknames: []string{"x___y", "x__y", "x_-_y"},
			want:      []string{"x___y"},
		},
		{
			name:      "a short prefix in streamers alone",
			term:      "ab",
			nicknames: []string{"abz"},
			streamers: []string{"ab1", "xab"},
			want:      []string{"ab1"},
		},
		{
			name:      "an underscore in a prefix is literal",
			term:      "a_",
			streamers: []string{"a_b", "abb"},
			want:      []string{"a_b"},
		},
		{
			name:      "a character no nickname can hold finds nothing",
			term:      "Alice",
			nicknames: []string{"Alice", "alice"},
			streamers: []string{"Alice", "alice"},
		},
		{
			name: "the closest seven, each once",
			term: "rank",
			nicknames: []string{
				"rank1234567", "rank123456", "rank12345", "rank1234", "rank123", "rank12", "rank1", "rank",
			},
			streamers: []string{"rank"},
			want:      []string{"rank", "rank1", "rank12", "rank123", "rank1234", "rank12345", "rank123456"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			for _, nickname := range tt.nicknames {
				db.MustExec("insert into nicknames (nickname) values ($1)", nickname)
			}
			for _, nickname := range tt.streamers {
				db.ensureStreamer(nickname)
			}
			if got := db.SearchStreamers(tt.term); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SearchStreamers(%q) = %q, want %q", tt.term, got, tt.want)
			}
		})
	}
}
