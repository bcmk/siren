package db

import "testing"

// A referrer's count holds only the users their own links brought
func TestReferredUsersCount(t *testing.T) {
	t.Parallel()
	tdb := newTestDB(t)
	d := tdb.Database

	ids := map[string]UserID{}
	for i, name := range []string{"alice", "bob", "carol", "f1", "f2", "f3", "f4"} {
		ids[name], _ = d.AddUserAndChat("test", int64(100+i), 1000, "private")
	}
	alice, bob := ids["alice"], ids["bob"]
	d.AddReferralEvent(1000, &alice, ids["f1"], nil)
	d.AddReferralEvent(1000, &alice, ids["f2"], nil)
	d.AddReferralEvent(1000, &bob, ids["f3"], nil)
	// A streamer link records an event with no referrer
	d.AddReferralEvent(1000, nil, ids["f4"], nil)

	tests := []struct {
		referrer string
		want     int
	}{
		{"alice", 2},
		{"bob", 1},
		{"carol", 0},
	}
	for _, tc := range tests {
		t.Run(tc.referrer, func(t *testing.T) {
			if got := d.ReferredUsersCount(ids[tc.referrer]); got != tc.want {
				t.Errorf("ReferredUsersCount = %d, want %d", got, tc.want)
			}
		})
	}
}
