package checkers

import "testing"

// A performer's avatar path names the profile image the online list would report.
func TestBongaCamsProfileImage(t *testing.T) {
	cases := []struct {
		name   string
		avatar string
		want   string
	}{
		{"an avatar", "/0f5/0b2/2fc/95cafee2f6ba00c4afa5f6b832edd9a0_avatars.jpg",
			"https://i.bgicdn.com/0f5/0b2/2fc/95cafee2f6ba00c4afa5f6b832edd9a0_thumb_big.jpg"},
		{"an unexpected path", "/0f5/0b2/2fc/95cafee2f6ba00c4afa5f6b832edd9a0_avatarm.jpg", ""},
		{"none", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bongacamsProfileImage(tc.avatar); got != tc.want {
				t.Errorf("profile image = %q, want %q", got, tc.want)
			}
		})
	}
}
