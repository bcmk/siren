package db

import (
	"context"
	"testing"
)

// A user row can have chat_type = null: pre-0032 rows,
// old inserts of only chat_id and max_subs, and every 0060 tombstone.
// addUser must read chat_type into a *string,
// so a null scans to nil instead of panicking.
func TestAddUserNullChatType(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	d := tdb.Database

	// A tombstone-style row: id and chat_id only, chat_type left null.
	d.MustExec("insert into users (id, chat_id) values (900, 900)")
	d.MustExec("insert into users (id, chat_id) values (901, 901)")

	// Must not panic scanning the null chat_type.
	id, created := d.AddUser(900, 7, 1000, "private")
	if created {
		t.Error("created = true for an existing user")
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

// Two sessions can both find a new chat missing.
// The second's insert waits for the first's, then takes the first's row as not created.
func TestAddUserLostRace(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	loser := NewDatabase(tdb.connStr, false, 5)
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
			winnerID, created := tdb.AddUserInTx(tx, tt.chatID, 5, 1000, tt.winnerChatType)
			if !created {
				t.Fatal("the winner's insert reported no creation")
			}

			type result struct {
				id      UserID
				created bool
			}
			loserResult := make(chan result, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				id, created := loser.AddUser(tt.chatID, 9, 2000, "private")
				loserResult <- result{id, created}
			}()
			waitForLockWait(t, tdb.Database, done)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}

			got := <-loserResult
			if got.id != winnerID || got.created {
				t.Errorf("the loser got (%d, %v), want (%d, false)", got.id, got.created, winnerID)
			}
			user, found := tdb.UserByID(winnerID)
			if !found || user.MaxSubs != 5 || user.ChatType == nil || *user.ChatType != tt.wantChatType {
				t.Errorf("stored %s, found: %v, want max_subs 5 and type %q", asJSON(user), found, tt.wantChatType)
			}
		})
	}
}
