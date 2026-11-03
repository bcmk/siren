package main

import (
	"strings"
	"testing"
)

// A model a lookup finds online gets their camserv's live snapshot, where MFC serves one,
// and their avatar.
func TestLookupImages(t *testing.T) {
	const avatar = "https://img.mfcimg.com/photos2/123/12345678/avatar.300x300.jpg"
	tests := []struct {
		name    string
		vs      int
		camserv int
		// wantSnapshot is the snapshot's URL up to its cache-busting query, empty for none.
		wantSnapshot string
	}{
		{"in a public show", 0, 7, "https://video1157.myfreecams.com/snaps/mfc_12345678_853x480.jpg?"},
		{"on a camserv with no known host", 0, 8, ""},
		{"in a private show", 12, 7, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newSnapshot()
			s.setVideoHosts(map[int]string{7: "video1157"})
			uid, vs, camserv := 12345678, tc.vs, tc.camserv
			image, profile := s.lookupImages(sessionUpdate{
				UID: &uid,
				VS:  &vs,
				U:   &sessionUpdateUser{Camserv: &camserv},
			})
			if tc.wantSnapshot == "" && image != "" || !strings.HasPrefix(image, tc.wantSnapshot) {
				t.Errorf("image = %q, want %q", image, tc.wantSnapshot)
			}
			if profile != avatar {
				t.Errorf("profile image = %q, want %q", profile, avatar)
			}
		})
	}
}
