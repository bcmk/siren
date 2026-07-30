package checkers

import (
	"maps"
	"strings"
	"testing"
)

func TestChaturbateParseAffiliateParams(t *testing.T) {
	var c ChaturbateChecker
	tests := []struct {
		name   string
		input  string
		want   map[string]string
		wantOK bool
	}{
		{
			"token payout link keeps its tour",
			"https://chaturbate.com/in/?tour=7Bge&campaign=WIl8t&track=default&room=sirenbot",
			map[string]string{"campaign": "WIl8t", "tour": "7Bge", "track": "default"},
			true,
		},
		{
			"revshare link keeps its tour",
			"https://chaturbate.com/in/?tour=LQps&campaign=WIl8t&track=default&room=sirenbot",
			map[string]string{"campaign": "WIl8t", "tour": "LQps", "track": "default"},
			true,
		},
		{
			"surrounding whitespace is tolerated",
			"  https://chaturbate.com/in/?tour=LQps&campaign=WIl8t&track=default&room=x  ",
			map[string]string{"campaign": "WIl8t", "tour": "LQps", "track": "default"},
			true,
		},
		{
			"rejects a link missing a param",
			"https://chaturbate.com/in/?tour=7Bge&campaign=WIl8t&room=sirenbot",
			nil,
			false,
		},
		{"rejects another host", "https://evil.com/in/?tour=7Bge&campaign=WIl8t&track=default", nil, false},
		{"rejects non-https", "http://chaturbate.com/in/?tour=7Bge&campaign=WIl8t&track=default", nil, false},
		{"rejects a bare id", "WIl8t", nil, false},
		{"rejects junk", "not a link", nil, false},
		{
			"rejects a value with unsafe characters",
			"https://chaturbate.com/in/?tour=7Bge&campaign=a b&track=default",
			nil,
			false,
		},
		{
			"rejects an over-long value",
			"https://chaturbate.com/in/?tour=7Bge&campaign=" + strings.Repeat("a", 129) + "&track=default",
			nil,
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := c.ParseAffiliateParams(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ParseAffiliateParams(%q) ok = %v, want %v", tc.input, ok, tc.wantOK)
			}
			if ok && !maps.Equal(got, tc.want) {
				t.Errorf("ParseAffiliateParams(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestChaturbateSupportsCustomAffiliateLink(t *testing.T) {
	var c ChaturbateChecker
	if !c.Capabilities().SupportsCustomAffiliateLink {
		t.Error("chaturbate should support custom affiliate")
	}
}

func TestOtherSitesRejectAffiliate(t *testing.T) {
	// Every site inherits BaseChecker's refusal until it opts in.
	var c StripchatChecker
	if c.Capabilities().SupportsCustomAffiliateLink {
		t.Error("stripchat should not support custom affiliate yet")
	}
	if _, ok := c.ParseAffiliateParams("https://chaturbate.com/in/?tour=7Bge&campaign=WIl8t&track=default"); ok {
		t.Error("a site without custom affiliate should accept nothing")
	}
}
