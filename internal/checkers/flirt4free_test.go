package checkers

import (
	"encoding/json"
	"testing"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// An online model checked on their own gets their live screencap, and an offline one gets none.
func TestFlirt4FreeModelInfo(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus cmdlib.StatusKind
		wantImage  string
	}{
		{"online", `{"status": "online", "model_id": 1431593}`, cmdlib.StatusOnline,
			"https://live-screencaps.vscdns.com/1431593-desktop.jpg"},
		{"offline", `{"status": "offline", "model_id": 1431593}`, cmdlib.StatusOffline, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := &flirt4FreeCheckResponse{}
			if err := json.Unmarshal([]byte(tc.body), parsed); err != nil {
				t.Fatal(err)
			}
			info := flirt4FreeModelInfo(parsed)
			if info.Status != tc.wantStatus {
				t.Errorf("status = %v, want %v", info.Status, tc.wantStatus)
			}
			if info.ImageURL != tc.wantImage {
				t.Errorf("image = %q, want %q", info.ImageURL, tc.wantImage)
			}
		})
	}
}
