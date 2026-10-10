package db

import (
	"reflect"
	"testing"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// TestUpsertStampsAfterPreviousChange checks that a streamer's changes get distinct stamps
func TestUpsertStampsAfterPreviousChange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// the round timestamps, one change each
		rounds []int
		want   []int
	}{
		{name: "clock moves on", rounds: []int{100, 105}, want: []int{100, 105}},
		{name: "same second", rounds: []int{100, 100, 100}, want: []int{100, 101, 102}},
		{name: "clock steps back", rounds: []int{200, 150}, want: []int{200, 201}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			defer db.terminate()

			status := cmdlib.StatusOffline
			for _, ts := range tc.rounds {
				status = cmdlib.StatusOnline + cmdlib.StatusOffline - status
				db.UpsertUnconfirmedStatusChanges([]StatusChange{{Nickname: "a", Status: status}}, ts)
			}

			got := db.MustInts(`select timestamp from status_changes order by timestamp`)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("status_changes timestamps = %v, want %v", got, tc.want)
			}
			// streamers holds the last two stamps
			s := db.MaybeStreamer("a")
			last := tc.want[len(tc.want)-1]
			prev := tc.want[len(tc.want)-2]
			if s.UnconfirmedTimestamp != last || s.PrevUnconfirmedTimestamp != prev {
				t.Errorf(
					"streamers timestamps = %d, %d, want %d, %d",
					s.PrevUnconfirmedTimestamp,
					s.UnconfirmedTimestamp,
					prev,
					last)
			}
		})
	}
}
