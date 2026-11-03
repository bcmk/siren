package checkers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bcmk/siren/v6/lib/cmdlib"
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

// An online model queried on their own gets their live snapshot,
// with their avatar as the profile image, and an offline one gets neither.
func TestCam4ModelInfo(t *testing.T) {
	const avatar = "https://stackvaults-media.xcdnpro.com/a//raw:true/b"
	const snapshot = "https://snapshots.xcdnpro.com/thumbnails/alice"
	cases := []struct {
		name        string
		body        string
		wantStatus  cmdlib.StatusKind
		wantImage   string
		wantProfile string
	}{
		{"online", `{"online": true, "avatarUrl": "` + avatar + `"}`, cmdlib.StatusOnline, snapshot, avatar},
		{"offline", `{"online": false, "avatarUrl": "` + avatar + `"}`, cmdlib.StatusOffline, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := &cam4Response{}
			if err := json.Unmarshal([]byte(tc.body), parsed); err != nil {
				t.Fatal(err)
			}
			info := cam4ModelInfo("alice", parsed)
			if info.Status != tc.wantStatus {
				t.Errorf("status = %v, want %v", info.Status, tc.wantStatus)
			}
			if info.ImageURL != tc.wantImage {
				t.Errorf("image = %q, want %q", info.ImageURL, tc.wantImage)
			}
			if info.ProfileImageURL != tc.wantProfile {
				t.Errorf("profile image = %q, want %q", info.ProfileImageURL, tc.wantProfile)
			}
		})
	}
}
