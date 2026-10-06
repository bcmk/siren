package checkers

import (
	"testing"

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
