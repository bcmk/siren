package checkers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// TestChaturbateStatus pins the codes of a room Chaturbate refuses to show
func TestChaturbateStatus(t *testing.T) {
	cases := []struct {
		code string
		want cmdlib.StatusKind
	}{
		{"access-denied", cmdlib.StatusDenied},
		{"unauthorized", cmdlib.StatusDenied},
		// The room exists, though its password hides whether it is live
		{"password-required", cmdlib.StatusOnline | cmdlib.StatusOffline},
		{"something-new", cmdlib.StatusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			if got := chaturbateStatus(tc.code); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestChaturbateWentOfflineAt reads last_broadcast in UTC-7 all year, for an offline room alone
func TestChaturbateWentOfflineAt(t *testing.T) {
	end := time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)
	endMillis := end.Add(429 * time.Millisecond)
	cases := []struct {
		name    string
		body    string
		want    *time.Time
		wantErr bool
	}{
		{"offline", `{"room_status": "offline", "last_broadcast": "2026-10-22T13:00:00"}`, &end, false},
		{
			"fractional seconds",
			`{"room_status": "offline", "last_broadcast": "2026-10-22T13:00:00.429"}`,
			&endMillis,
			false,
		},
		{"live", `{"room_status": "public", "last_broadcast": "2026-10-22T13:00:00"}`, nil, false},
		{"away", `{"room_status": "away", "last_broadcast": "2026-10-22T13:00:00"}`, nil, false},
		{"never broadcast", `{"room_status": "offline", "last_broadcast": ""}`, nil, false},
		{"unreadable", `{"room_status": "offline", "last_broadcast": "yesterday"}`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := &chaturbateResponse{}
			if err := json.Unmarshal([]byte(tc.body), parsed); err != nil {
				t.Fatal(err)
			}
			got, err := chaturbateWentOfflineAt(parsed)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if !sameTime(got, tc.want) {
				t.Errorf("end = %v, want %v", got, tc.want)
			}
		})
	}
}

// sameTime reports whether two optional times are both absent or the same instant
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// TestChaturbateShowKind maps Chaturbate's show names to show kinds
func TestChaturbateShowKind(t *testing.T) {
	cases := []struct {
		show string
		want cmdlib.ShowKind
	}{
		{"public", cmdlib.ShowPublic},
		{"private", cmdlib.ShowPrivate},
		{"group", cmdlib.ShowGroup},
		{"hidden", cmdlib.ShowHidden},
		{"away", cmdlib.ShowAway},
		{"offline", cmdlib.ShowUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.show, func(t *testing.T) {
			if got := chaturbateShowKind(tc.show); got != tc.want {
				t.Errorf("show = %v, want %v", got, tc.want)
			}
		})
	}
}
