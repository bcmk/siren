package db

import "testing"

// A user row can have chat_type = null: pre-0032 rows,
// old inserts of only chat_id and max_subs, and every 0060 tombstone.
// addUser must read chat_type into a *string,
// so a null scans to nil instead of panicking.
func TestAddUserNullChatType(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	defer tdb.terminate()
	d := tdb.Database

	// A tombstone-style row: id and chat_id only, chat_type left null.
	d.MustExec("insert into users (id, chat_id) values (900, 900)")

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
		t.Errorf("chat_type = %v, want private (backfilled)", user.ChatType)
	}
}

// AddUser with an empty chat_type stores null, not ”,
// so unset has a single encoding.
func TestAddUserEmptyChatTypeIsNull(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	defer tdb.terminate()
	d := tdb.Database

	d.AddUser(901, 5, 1000, "")
	user, found := d.User(901)
	if !found {
		t.Fatal("user with chat_id 901 not found")
	}
	if user.ChatType != nil {
		t.Errorf("chat_type = %q, want nil (null, not empty string)", *user.ChatType)
	}
}
