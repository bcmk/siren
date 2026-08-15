package checkers

import (
	"maps"
	"net/url"
	"strings"
	"testing"
)

func TestStripchatParseAffiliateParams(t *testing.T) {
	var c StripchatChecker
	creditsStreamer := map[string]string{"referrer": "streamer"}
	builderLink := "https://go.whitetrafsa.com?campaignId=some_campaign_id&creativeId=some_creative_id" +
		"&sourceId=some_source_id&memberId=some_click_id" +
		"&userId=d100c22f4c1ce24c2706ec4ac744d0bbd9a2a46fc437add588196a2d7aa9db07"
	tests := []struct {
		name   string
		input  string
		want   map[string]string
		wantOK bool
	}{
		{"the streamer keyword credits the model an alert names", "streamer", creditsStreamer, true},
		{"the keyword is case insensitive", "Streamer", creditsStreamer, true},
		{"surrounding whitespace is tolerated", "  streamer  ", creditsStreamer, true},
		{
			"a links-builder link keeps its fields",
			builderLink,
			map[string]string{
				"campaignId": "some_campaign_id",
				"creativeId": "some_creative_id",
				"sourceId":   "some_source_id",
				"memberId":   "some_click_id",
				"userId":     "d100c22f4c1ce24c2706ec4ac744d0bbd9a2a46fc437add588196a2d7aa9db07",
			},
			true,
		},
		{
			"a link with only the affiliate identity is enough",
			"https://go.whitetrafsa.com?userId=abc123",
			map[string]string{"userId": "abc123"},
			true,
		},
		{
			"a link with a path parses like any other",
			"https://go.whitetrafsa.com/promo/?userId=abc123",
			map[string]string{"userId": "abc123"},
			true,
		},
		{"rejects a link with no userId", "https://go.whitetrafsa.com?campaignId=1", nil, false},
		{"rejects an empty userId", "https://go.whitetrafsa.com?userId=", nil, false},
		{"rejects non-https", "http://go.whitetrafsa.com?userId=abc123", nil, false},
		{
			"a builder field keeps whatever the affiliate typed",
			"https://go.whitetrafsa.com?userId=abc123&campaignId=" + url.QueryEscape("летняя promo 2026!"),
			map[string]string{"userId": "abc123", "campaignId": "летняя promo 2026!"},
			true,
		},
		{
			"rejects an over-long builder field",
			"https://go.whitetrafsa.com?userId=abc123&campaignId=" + strings.Repeat("a", 257),
			nil,
			false,
		},
		{
			"rejects an over-long userId",
			"https://go.whitetrafsa.com?userId=" + strings.Repeat("a", 129),
			nil,
			false,
		},
		{
			"rejects a userId with unsafe characters",
			"https://go.whitetrafsa.com?userId=" + url.QueryEscape("a b"),
			nil,
			false,
		},
		{"rejects the empty argument", "", nil, false},
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

func TestStripchatSupportsCustomAffiliateLink(t *testing.T) {
	var c StripchatChecker
	if !c.Capabilities().SupportsCustomAffiliateLink {
		t.Error("stripchat should support custom affiliate")
	}
}

// The ID tells the two identities apart: the status message shows one and not the other.
func TestStripchatAffiliateID(t *testing.T) {
	var c StripchatChecker
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"a pasted link shows its userId", "https://go.whitetrafsa.com?userId=abc123", "abc123"},
		{"crediting the streamer shows none", "streamer", ""},
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
