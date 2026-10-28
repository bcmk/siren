package db

import (
	"context"
	"testing"
)

// A user row can have chat_type = null: pre-0032 rows,
// old inserts of only chat_id and max_subs, and every 0060 tombstone.
// addUserAndChat must read chat_type into a *string,
// so a null scans to nil instead of panicking.
func TestAddUserAndChatNullChatType(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	d := tdb.Database

	// A tombstone-style row: id and tg_chat_id only, chat_type left null.
	d.MustExec("insert into users (id, tg_chat_id) values (900, 900)")
	d.MustExec("insert into users (id, tg_chat_id) values (901, 901)")

	// Must not panic scanning the null chat_type.
	id, userCreated := d.AddUserAndChat("en", 900, 1000, "private")
	if userCreated {
		t.Error("user created = true for an existing user")
	}
	if id != 900 {
		t.Errorf("id = %d, want 900", id)
	}
	// The null chat_type is backfilled from the passed one.
	user, found := d.UserByID(900)
	if !found {
		t.Fatal("user 900 not found")
	}
	if user.ChatType == nil || *user.ChatType != "private" {
		t.Errorf("chat_type = %s, want private (backfilled)", asJSON(user.ChatType))
	}
	if bystander, _ := d.UserByID(901); bystander.ChatType != nil {
		t.Errorf("a bystander's chat_type = %q, want null", *bystander.ChatType)
	}
}

// Two sessions can both find a new user missing.
// The second's user insert waits for the first's commit,
// then takes its user, as not created, and its chat.
func TestAddUserAndChatLostRace(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	// Another default limit than the winner's, so a chat the loser overwrites shows
	loser := NewDatabase(tdb.connStr, false, 9)
	defer func() { _ = loser.Close() }()
	ctx := context.Background()

	tests := []struct {
		name           string
		chatID         int64
		winnerChatType string
		wantChatType   string
	}{
		{"keeps the winner's type", 1, "group", "group"},
		{"backfills a type the winner left null", 2, "", "private"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := tdb.begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			winnerID, created := tdb.insertUser(tx, tt.chatID, 1000, tt.winnerChatType)
			if !created {
				t.Fatal("the winner's insert reported no creation")
			}
			tdb.addChat(tx, "en", winnerID)

			type result struct {
				id          UserID
				userCreated bool
			}
			loserResult := make(chan result, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				id, userCreated := loser.AddUserAndChat("en", tt.chatID, 2000, "private")
				loserResult <- result{id, userCreated}
			}()
			waitForLockWait(t, tdb.Database, done)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}

			got := <-loserResult
			if got.id != winnerID || got.userCreated {
				t.Errorf("the loser got (%d, %v), want (%d, false)", got.id, got.userCreated, winnerID)
			}
			user, found := tdb.UserByID(winnerID)
			if !found || user.ChatType == nil || *user.ChatType != tt.wantChatType {
				t.Errorf("stored %s, found: %v, want type %q", asJSON(user), found, tt.wantChatType)
			}
			if chat, found := tdb.Chat("en", winnerID); !found || chat.MaxSubs != tdb.defaultMaxSubs {
				t.Errorf("chat = (%+v, %v), want the winner's, with its limit of %d", chat, found, tdb.defaultMaxSubs)
			}
		})
	}
}
