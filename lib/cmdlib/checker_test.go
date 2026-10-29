package cmdlib

import "testing"

// A streamer's online-list line names each image they have, leaving out an empty URL.
func TestOnlineListLine(t *testing.T) {
	cases := []struct {
		name string
		info StreamerInfo
		want string
	}{
		{"both images", StreamerInfo{ImageURL: "https://a.jpg", ProfileImageURL: "https://p.jpg"},
			"alice, pic: https://a.jpg, prof: https://p.jpg"},
		{"only a profile image", StreamerInfo{ProfileImageURL: "https://p.jpg"}, "alice, prof: https://p.jpg"},
		{"only an image", StreamerInfo{ImageURL: "https://a.jpg"}, "alice, pic: https://a.jpg"},
		{"neither", StreamerInfo{}, "alice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OnlineListLine("alice", tc.info); got != tc.want {
				t.Errorf("line = %q, want %q", got, tc.want)
			}
		})
	}
}
