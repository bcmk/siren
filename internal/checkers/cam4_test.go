package checkers

import (
	"encoding/json"
	"testing"
	"time"
)

// TestCam4WentOnlineAt reads lastBroadcast as when the model last went online, in Unix ms
func TestCam4WentOnlineAt(t *testing.T) {
	start := time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		body string
		want *time.Time
	}{
		{"offline", `{"online": false, "lastBroadcast": 1792699200000}`, &start},
		{"live", `{"online": true, "lastBroadcast": 1792699200000}`, &start},
		{"never broadcast", `{"online": false, "lastBroadcast": 0}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := &cam4Response{}
			if err := json.Unmarshal([]byte(tc.body), parsed); err != nil {
				t.Fatal(err)
			}
			if got := cam4WentOnlineAt(parsed); !sameTime(got, tc.want) {
				t.Errorf("start = %v, want %v", got, tc.want)
			}
		})
	}
}

// A CAM4 model's own profile image passes, and CAM4's shared default cards are dropped.
func TestCam4ProfileImage(t *testing.T) {
	const own = "https://stackvaults-media.xcdnpro.com/a/quality:100/resize:fit:400:300:1/b"
	cases := []struct {
		name         string
		profileThumb string
		want         string
	}{
		{"a model's own image", own, own},
		{"a default card", "https://static.cam4.com/web/images/defaults/default_Female.png", ""},
		{"none", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cam4ProfileImage(tc.profileThumb); got != tc.want {
				t.Errorf("profile image = %q, want %q", got, tc.want)
			}
		})
	}
}
