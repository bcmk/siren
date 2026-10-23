package checkers

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStripchatNicknamePreprocessing(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"nickname", "Alice", "alice"},
		{"link", "https://stripchat.com/Alice", "alice"},
		{"link without scheme", "stripchat.com/Alice", "alice"},
		{"language subdomain and query", "https://de.stripchat.com/Alice?utm_source=x", "alice"},
		{"trailing slash", "https://stripchat.com/Alice/", "alice"},
		{"profile tab", "https://stripchat.com/Alice/profile", "alice"},
		{"timeline tab", "https://stripchat.com/Alice/timeline/", "alice"},
		{"uppercase scheme and host", "HTTPS://STRIPCHAT.COM/Alice", "alice"},
		{"xhamster live", "https://xhamsterlive.com/Alice", "alice"},
		{"category page", "https://stripchat.com/girls/blondes", "https://stripchat.com/girls/blondes"},
		{"another site", "https://example.com/Alice", "https://example.com/alice"},
	}
	c := &StripchatChecker{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.NicknamePreprocessing(tc.input)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if again := c.NicknamePreprocessing(got); again != got {
				t.Errorf("a second pass changed %q to %q", got, again)
			}
		})
	}
}

// TestStripchatWentOfflineAt reads wentIdleAt as the end of an offline model's last stream.
func TestStripchatWentOfflineAt(t *testing.T) {
	end := time.Date(2026, 10, 24, 18, 33, 16, 0, time.UTC)
	cam := func(status, wentIdleAt string) string {
		return `{"user": {"user": {"status": "` + status + `", "statusChangedAt": "2026-10-24T18:37:57Z", ` +
			`"wentIdleAt": ` + wentIdleAt + `}}}`
	}
	cases := []struct {
		name string
		body string
		want *time.Time
	}{
		{"off", cam("off", `"2026-10-24T18:33:16Z"`), &end},
		{"idle", cam("idle", `"2026-10-24T18:33:16Z"`), &end},
		{"never approved", cam("off", "null"), nil},
		{"went live in between", cam("public", `"2026-10-24T18:33:16Z"`), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := &stripchatCamResponse{}
			if err := json.Unmarshal([]byte(tc.body), parsed); err != nil {
				t.Fatal(err)
			}
			if got := stripchatWentOfflineAt(parsed); !sameTime(got, tc.want) {
				t.Errorf("end = %v, want %v", got, tc.want)
			}
		})
	}
}
