package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bcmk/siren/v5/internal/checkers"
	"github.com/bcmk/siren/v5/internal/db"
	"github.com/bcmk/siren/v5/lib/cmdlib"
)

// TestFailedAddOffersSearch holds the find-and-add button to the chats a web app opens in,
// and to the terms a search would run on.
// A group would take the button and answer 400, swallowing the reply with it.
func TestFailedAddOffersSearch(t *testing.T) {
	t.Parallel()
	const appURL = "https://" + testWebhookDomain + "/apps/add?endpoint=test"
	// A rejected nickname exactly as long as a search runs on, and the term it becomes.
	atLimit := strings.Repeat("a", maxSearchTerm-2) + " b"
	atLimitTerm := strings.Repeat("a", maxSearchTerm-2) + "+b"
	tests := []struct {
		name string
		// nickname is what /add carries, empty for the bare command.
		nickname string
		chatID   int64
		// fromConfirmation answers as the confirmation round does, not as the command does.
		fromConfirmation bool
		fixedList        bool
		wantURL          string
	}{
		{"a failed add in a private chat", "no_such_model", 10, true, false, appURL + "&term=no_such_model"},
		{"a failed add in a group", "no_such_model", -10, true, false, ""},
		{"a failed add for a fixed-list checker", "no_such_model", 10, true, true, ""},
		{"an invalid nickname in a private chat", "Anna Smith", 10, false, false, appURL + "&term=anna+smith"},
		{"an invalid nickname in a group", "Anna Smith", -10, false, false, ""},
		{"an invalid nickname for a fixed-list checker", "Anna Smith", 10, false, true, ""},
		{"a nickname past the search limit", atLimit + "c", 10, false, false, appURL},
		{"a nickname at the limit behind a blank", " " + atLimit, 10, false, false, appURL + "&term=" + atLimitTerm},
		{"the bare add reply", "", 10, false, false, appURL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := webAppTestWorker(t)
			defer w.terminate()
			if tc.fixedList {
				w.checker = &checkers.TwitchChecker{}
			}

			m := testMessage(w, tc.chatID, "add", 100)
			if tc.fromConfirmation {
				w.notifyOfAddResults(db.PriorityHigh, []db.Notification{{
					Endpoint: "test",
					UserID:   m.userID,
					ChatID:   m.chatID,
					Nickname: tc.nickname,
					Status:   cmdlib.StatusNotFound,
					Kind:     db.ReplyPacket,
					Command:  "add",
				}})
			} else {
				w.addStreamer(m, tc.nickname, false)
			}
			if got := webAppButtonURL(t, w); got != tc.wantURL {
				t.Errorf("search button URL = %q, want %q", got, tc.wantURL)
			}
		})
	}
}

// TestButtonHintFollowsTheButton pins the gate on the line that reads into the button:
// a chat that gets no button must not be left hanging on the "to" the label completes.
func TestButtonHintFollowsTheButton(t *testing.T) {
	t.Parallel()
	base := filepath.Join("..", "..", "res", "translations")
	files := map[string][]string{
		"en": {filepath.Join(base, "common.en.yaml"), filepath.Join(base, "stripchat.en.yaml")},
		"ru": {filepath.Join(base, "common.ru.yaml"), filepath.Join(base, "stripchat.ru.yaml")},
	}
	hints := map[string][]string{
		"en": {"Or click the button below to", "You can try the button below to"},
		"ru": {"Или нажмите кнопку ниже, чтобы", "Можно нажать кнопку ниже, чтобы"},
	}
	_, tpl := cmdlib.LoadAllTranslations(files)
	keys := []string{"syntax_add", "syntax_remove", "add_error", "invalid_symbols", "streamer_not_in_list"}
	for lang := range files {
		for _, key := range keys {
			t.Run(lang+"/"+key, func(t *testing.T) {
				t.Parallel()
				render := func(button bool) string {
					params := &renderParams{
						templates: tpl[lang],
						key:       key,
						data:      tplData{"streamer": "a_model", "has_button": button},
					}
					return params.render("")
				}
				offered, bare := render(true), render(false)
				var ends bool
				for _, hint := range hints[lang] {
					ends = ends || strings.HasSuffix(offered, hint)
					if strings.Contains(bare, hint) {
						t.Errorf("a chat with no button still reads %q:\n%s", hint, bare)
					}
				}
				if !ends {
					t.Errorf("a chat with a button ends on no hint:\n%s", offered)
				}
			})
		}
	}
}
