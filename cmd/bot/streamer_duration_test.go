package main

import (
	"testing"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

func TestStreamerDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		streamer db.Streamer
		want     int
	}{
		{
			name: "stamp behind now",
			streamer: db.Streamer{
				UnconfirmedStatus:     cmdlib.StatusOnline,
				UnconfirmedTimestamp:  70,
				PrevUnconfirmedStatus: cmdlib.StatusOffline,
			},
			want: 30,
		},
		{
			name: "stamp ahead of now",
			streamer: db.Streamer{
				UnconfirmedStatus:     cmdlib.StatusOffline,
				UnconfirmedTimestamp:  101,
				PrevUnconfirmedStatus: cmdlib.StatusOnline,
			},
			want: 0,
		},
	}
	w := &worker{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := w.streamerDuration(tc.streamer, 100)
			if got == nil {
				t.Fatal("got no duration")
			}
			if *got != tc.want {
				t.Errorf("got %d, want %d", *got, tc.want)
			}
		})
	}
}
