package main

import (
	"maps"
	"testing"
	texttemplate "text/template"

	"github.com/bcmk/siren/v4/internal/botconfig"
	"github.com/bcmk/siren/v4/internal/checkers"
)

func TestStreamerLink(t *testing.T) {
	legacyTpl := texttemplate.Must(texttemplate.New("affiliate_link").
		Parse("<a href='https://siren.chat/out/cb/{{ . }}'>{{ . }}</a>"))
	base := &worker{cfg: &botconfig.Config{AffiliateBase: "https://siren.chat/out/cb"}}
	slash := &worker{cfg: &botconfig.Config{AffiliateBase: "https://siren.chat/out/cb/"}}
	legacy := &worker{cfg: &botconfig.Config{}, legacyAffiliateTpl: legacyTpl}
	plain := &worker{cfg: &botconfig.Config{}}
	tests := []struct {
		name      string
		w         *worker
		affiliate map[string]string
		want      string
	}{
		{"legacy template", legacy, nil, "<a href='https://siren.chat/out/cb/alice'>alice</a>"},
		// affiliate_base with no affiliate must match the legacy output byte for byte.
		{"affiliate_base, no affiliate", base, nil, "<a href='https://siren.chat/out/cb/alice'>alice</a>"},
		{
			"a trailing slash on the base does not double up",
			slash,
			nil,
			"<a href='https://siren.chat/out/cb/alice'>alice</a>",
		},
		{
			"affiliate_base with affiliate",
			base,
			map[string]string{"campaign": "modelX", "tour": "7Bge", "track": "default"},
			"<a href='https://siren.chat/out/cb/alice?campaign=modelX&amp;tour=7Bge&amp;track=default'>alice</a>",
		},
		{"no affiliate config falls back to the plain name", plain, nil, "alice"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.w.streamerLink("alice", tc.affiliate); got != tc.want {
				t.Errorf("streamerLink() = %q, want %q", got, tc.want)
			}
		})
	}
}

// affiliateWorker builds a worker. Chaturbate supports affiliate, Twitch not.
// A non-empty affiliate_base is required for custom affiliate.
func affiliateWorker(enabled, supported bool) *worker {
	w := &worker{cfg: &botconfig.Config{
		EnableCustomAffiliateLink: enabled,
		AffiliateBase:             "https://siren.chat/out/cb",
	}}
	if supported {
		w.checker = &checkers.ChaturbateChecker{}
	} else {
		w.checker = &checkers.TwitchChecker{}
	}
	return w
}

func TestAffiliateLinkEnabled(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		supported bool
		want      bool
	}{
		{"config and site both allow it", true, true, true},
		{"config off", false, true, false},
		{"site does not support it", true, false, false},
		{"neither", false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := affiliateWorker(tc.enabled, tc.supported)
			if got := w.customAffiliateLinkEnabled(); got != tc.want {
				t.Errorf("customAffiliateLinkEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("no affiliate_base", func(t *testing.T) {
		w := affiliateWorker(true, true)
		w.cfg.AffiliateBase = ""
		if w.customAffiliateLinkEnabled() {
			t.Error("customAffiliateLinkEnabled() = true without affiliate_base, want false")
		}
	})
}

func TestGatedAffiliate(t *testing.T) {
	own := map[string]string{"campaign": "abc123", "tour": "7Bge", "track": "default"}
	tests := []struct {
		name      string
		enabled   bool
		supported bool
		custom    map[string]string
		want      map[string]string
	}{
		{"a chat's own set passes through", true, true, own, own},
		{"no set stays empty, so the template falls back to ours", true, true, nil, nil},
		{"unsupported site ignores a stored set", true, false, own, nil},
		{"config off ignores a stored set", false, true, own, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := affiliateWorker(tc.enabled, tc.supported).gatedAffiliate(tc.custom)
			if !maps.Equal(got, tc.want) {
				t.Errorf("gatedAffiliate(%v) = %v, want %v", tc.custom, got, tc.want)
			}
		})
	}
}

func TestIsGroupOrChannel(t *testing.T) {
	tests := []struct {
		name   string
		chatID int64
		want   bool
	}{
		{"zero", 0, false},
		{"private", 42, false},
		{"group", -42, true},
		{"supergroup", -1001234567890, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGroupOrChannel(tc.chatID); got != tc.want {
				t.Errorf("isGroupOrChannel(%v) = %v, want %v", tc.chatID, got, tc.want)
			}
		})
	}
}
