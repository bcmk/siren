package db

import "testing"

func TestGrantStarPaymentSubs(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	d := tdb.Database

	const chatID = int64(123)
	credited, _ := d.insertUser(d.db, chatID, 1000, "private")
	bystander, _ := d.AddUserAndChat("ep", 456, 1000, "private")

	// First credit: max_subs 5 -> 15, row stored.
	if added, maxSubs, _ := d.GrantStarPaymentSubs(chatID, "ep", "charge_1", 500, "subs", 10, "stars:subs:123:10", 1000); !added || maxSubs != 15 {
		t.Fatalf("first credit: added=%v maxSubs=%d, want true 15", added, maxSubs)
	}

	// Same charge id again: no-op, even with different amount/quantity.
	added, maxSubs, id := d.GrantStarPaymentSubs(chatID, "ep", "charge_1", 999, "subs", 99, "stars:subs:123:99", 2000)
	if added || maxSubs != 0 || id != credited {
		t.Errorf("duplicate charge: added=%v maxSubs=%d credited=%d, want false 0 %d", added, maxSubs, id, credited)
	}

	// Same charge id from a never-seen user: a no-op too, creating no user,
	// and naming the user the first charge credited, whose chat the reply's log row needs.
	const newChatID = int64(789)
	added, _, id = d.GrantStarPaymentSubs(newChatID, "ep", "charge_1", 500, "subs", 10, "stars:subs:789:10", 2000)
	if added || id != credited {
		t.Errorf("duplicate charge from a never-seen user: added=%v credited=%d, want false %d", added, id, credited)
	}
	if _, found := d.UserByChatID(newChatID); found {
		t.Error("duplicate charge from a never-seen user left a user")
	}

	// Same charge id from a user with no chat on the endpoint: a no-op too, creating no chat.
	known := d.EnsureUser(790)
	added, _, id = d.GrantStarPaymentSubs(790, "ep", "charge_1", 500, "subs", 10, "", 2000)
	if added || id != credited {
		t.Errorf("duplicate charge from a user with no chat: added=%v credited=%d, want false %d", added, id, credited)
	}
	if _, hasChat := d.Chat("ep", known); hasChat {
		t.Error("duplicate charge left a stray chat")
	}

	// A different charge credits again. Reaching 25 (not 35) confirms the
	// duplicate above did not bump max_subs.
	if added, maxSubs, _ := d.GrantStarPaymentSubs(chatID, "ep", "charge_2", 500, "subs", 10, "stars:subs:123:10", 3000); !added || maxSubs != 25 {
		t.Errorf("second charge: added=%v maxSubs=%d, want true 25", added, maxSubs)
	}

	// The stored row still reflects the first credit, not the duplicate.
	var stars, quantity int
	var product string
	found := d.MaybeRecord(
		"select stars_amount, product, quantity from star_payments where telegram_payment_charge_id = $1",
		QueryParams{"charge_1"},
		ScanTo{&stars, &product, &quantity})
	if !found {
		t.Fatal("star_payments row for charge_1 not found")
	}
	if stars != 500 || product != "subs" || quantity != 10 {
		t.Errorf("stored row = (%d, %q, %d), want (500, subs, 10)", stars, product, quantity)
	}

	if chat, _ := d.Chat("ep", bystander); chat.MaxSubs != 5 {
		t.Errorf("a bystander's max_subs = %d, want its own 5", chat.MaxSubs)
	}
}
