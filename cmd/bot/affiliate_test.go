package main

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"
	texttemplate "text/template"

	"github.com/bcmk/siren/v4/internal/botconfig"
	"github.com/bcmk/siren/v4/internal/checkers"
	"github.com/bcmk/siren/v4/lib/cmdlib"
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
		{
			"the opt-in flag rides the query",
			base,
			map[string]string{"referrer": "streamer"},
			"<a href='https://siren.chat/out/cb/alice?referrer=streamer'>alice</a>",
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

// TestStripchatAffiliate renders the real Stripchat messages,
// where a chat claims one of two identities: the model an alert names, or a pasted StripCash link.
func TestStripchatAffiliate(t *testing.T) {
	t.Parallel()
	base := filepath.Join("..", "..", "res", "translations")
	tests := []struct {
		name         string
		arg          string
		want         map[string]string
		wantContains string
	}{
		{
			name:         "the streamer keyword credits each model",
			arg:          "streamer",
			want:         map[string]string{"referrer": "streamer"},
			wantContains: "https://siren.chat/out/sc/your_model_name?referrer=streamer",
		},
		{
			name:         "a pasted link shows its affiliate ID",
			arg:          "https://go.whitetrafsa.com?campaignId=some_campaign_id&userId=abc123",
			want:         map[string]string{"campaignId": "some_campaign_id", "userId": "abc123"},
			wantContains: "Affiliate ID: <b>abc123</b>",
		},
		{
			name:         "a bare command explains both",
			wantContains: "StripCash links builder",
		},
		{
			name:         "junk is refused, naming both ways in",
			arg:          "Streamers",
			// The command, not the prose around it.
			wantContains: "<code>/affiliate streamer</code>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.tr, w.tpl = cmdlib.LoadAllTranslations(map[string][]string{"test": {
				filepath.Join(base, "common.en.yaml"),
				filepath.Join(base, "stripchat.en.yaml"),
			}})
			cfg := testConfig
			cfg.AffiliateBase = "https://siren.chat/out/sc"
			cfg.EnableCustomAffiliateLink = true
			w.cfg = &cfg
			w.checker = &checkers.StripchatChecker{}
			m := testMessage(w, -10, "affiliate", 0)
			w.setAffiliate(m, tc.arg)

			if got := w.mustUserByID(m.userID).AffiliateParams; !maps.Equal(got, tc.want) {
				t.Errorf("affiliate params = %v, want %v", got, tc.want)
			}
			if n := w.sendQueue.Len(); n != 1 {
				t.Fatalf("queued replies = %d, want 1", n)
			}
			q := w.sendQueue.pop()
			q.message.render("")
			text := q.message.(*messageParams).Text
			if !strings.Contains(text, tc.wantContains) {
				t.Errorf("reply does not carry %q, in:\n%s", tc.wantContains, text)
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
