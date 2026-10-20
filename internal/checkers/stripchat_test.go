package checkers

import "testing"

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
