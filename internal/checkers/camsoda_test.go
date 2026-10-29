package checkers

import "testing"

// A CamSoda model's profile image is their first offline picture, wherever it is,
// and never a stock image.
func TestCamSodaProfileImage(t *testing.T) {
	const (
		safe    = "https://media.livemediahost.com/media/offline_picture/558/a.pg_safe.20240117233608.jpg"
		hot     = "https://media.livemediahost.com/media/offline_picture/558/a.pg_hot.20240117233604.jpg"
		blurred = "https://media.livemediahost.com/assets/img/blurred/4x3/62.webp"
	)
	cases := []struct {
		name    string
		pgImage []string
		want    string
	}{
		{"an offline picture", []string{safe}, safe},
		{"the first of two offline pictures", []string{safe, hot}, safe},
		{"an offline picture after a stock image", []string{blurred, hot}, hot},
		{"a stock image alone", []string{blurred}, ""},
		{"no images", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := camSodaProfileImage(tc.pgImage); got != tc.want {
				t.Errorf("profile image = %q, want %q", got, tc.want)
			}
		})
	}
}
