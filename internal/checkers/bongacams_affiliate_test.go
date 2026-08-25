package checkers

import (
	"maps"
	"strings"
	"testing"
)

func TestBongaCamsParseAffiliateParams(t *testing.T) {
	var c BongaCamsChecker
	tests := []struct {
		name   string
		input  string
		want   map[string]string
		wantOK bool
	}{
		{
			"a member-referral link keeps its fuid",
			"https://bongacams.com?fuid=241616110",
			map[string]string{"fuid": "241616110"},
			true,
		},
		{
			"a direct link keeps only its campaign",
			"https://bongacams4.com/track?v=2&c=673886",
			map[string]string{"c": "673886"},
			true,
		},
		{
			"surrounding whitespace is tolerated",
			"  https://bongacams.com?fuid=241616110  ",
			map[string]string{"fuid": "241616110"},
			true,
		},
		{
			"a subdomain parses like the bare host",
			"https://en.bongacams.com/?fuid=241616110",
			map[string]string{"fuid": "241616110"},
			true,
		},
		{"rejects a link with neither identity", "https://bongacams4.com/track?v=2", nil, false},
		{"rejects an empty fuid with nothing else", "https://bongacams.com?fuid=", nil, false},
		{"rejects a non-numeric fuid", "https://bongacams.com?fuid=abc", nil, false},
		{"rejects an over-long fuid", "https://bongacams.com?fuid=" + strings.Repeat("1", 33), nil, false},
		{"rejects another host", "https://evil.com?fuid=241616110", nil, false},
		{"rejects a lookalike host", "https://notbongacams.com?fuid=241616110", nil, false},
		{"rejects non-https", "http://bongacams.com?fuid=241616110", nil, false},
		{"rejects a bare id", "241616110", nil, false},
		{"rejects junk", "not a link", nil, false},
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

func TestBongaCamsSupportsCustomAffiliateLink(t *testing.T) {
	var c BongaCamsChecker
	if !c.Capabilities().SupportsCustomAffiliateLink {
		t.Error("bongacams should support custom affiliate")
	}
}

// The ID names whichever identity the link carries.
func TestBongaCamsAffiliateID(t *testing.T) {
	var c BongaCamsChecker
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"a member-referral link shows its fuid", "https://bongacams.com?fuid=241616110", "241616110"},
		{"a direct link shows its campaign", "https://bongacams4.com/track?v=2&c=673886", "673886"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params, ok := c.ParseAffiliateParams(tc.input)
			if !ok {
				t.Fatalf("ParseAffiliateParams(%q) refused it", tc.input)
			}
			if got := c.AffiliateID(params); got != tc.want {
				t.Errorf("AffiliateID() = %q, want %q", got, tc.want)
			}
		})
	}
}
