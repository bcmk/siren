package db

import "testing"

func TestGrantStarPaymentSubs(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.terminate()
	d := tdb.Database

	const chatID = int64(123)
	d.AddUser(chatID, 5, 1000, "private")

	// First credit: max_subs 5 -> 15, row stored.
	if added, maxSubs := d.GrantStarPaymentSubs(chatID, "ep", "charge_1", 500, "subs", 10, "stars:subs:123:10", 1000); !added || maxSubs != 15 {
		t.Fatalf("first credit: added=%v maxSubs=%d, want true 15", added, maxSubs)
	}

	// Same charge id again: no-op, even with different amount/quantity.
	if added, maxSubs := d.GrantStarPaymentSubs(chatID, "ep", "charge_1", 999, "subs", 99, "stars:subs:123:99", 2000); added || maxSubs != 0 {
		t.Errorf("duplicate charge: added=%v maxSubs=%d, want false 0", added, maxSubs)
	}

	// A different charge credits again. Reaching 25 (not 35) confirms the
	// duplicate above did not bump max_subs.
	if added, maxSubs := d.GrantStarPaymentSubs(chatID, "ep", "charge_2", 500, "subs", 10, "stars:subs:123:10", 3000); !added || maxSubs != 25 {
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
}
