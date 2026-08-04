package main

import (
	"html"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bcmk/siren/v4/internal/db"
	"github.com/bcmk/siren/v4/lib/cmdlib"
)

var htmlTags = regexp.MustCompile(`<[^>]*>`)

// visibleText strips what Telegram parses into entities, leaving the text its limits count.
func visibleText(s string) string {
	return html.UnescapeString(htmlTags.ReplaceAllString(s, ""))
}

// utf16Units measures a string as Telegram measures a caption.
func utf16Units(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// TestOnlineTemplateComposesTheHint pins the fragment that replaced a Go-side append:
// the real online template carries the customization hint a blank line below the status,
// and only where the answer asks for one.
func TestOnlineTemplateComposesTheHint(t *testing.T) {
	t.Parallel()
	for _, lang := range realLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			tpl := realTemplates(t, lang)
			render := func(hint bool) string {
				params := &renderParams{templates: tpl, key: "online", data: tplData{
					"streamer_link": "alica_webcam",
					"fields_hint":   hint,
				}}
				return params.render("")
			}
			plain, hinted := render(false), render(true)
			if strings.Contains(plain, "/settings") {
				t.Errorf("an unhinted status carries the hint anyway: %q", plain)
			}
			if !strings.HasPrefix(hinted, plain+"\n\n") {
				t.Errorf("the hint did not follow the status a blank line below: %q", hinted)
			}
			if tip := strings.TrimPrefix(hinted, plain+"\n\n"); !strings.Contains(tip, "/settings") {
				t.Errorf("the hint lost its settings command: %q", tip)
			}
		})
	}
}

// A subject cut to fit the caption says so, rather than ending mid-word as if the topic did.
func TestOnlineTemplateMarksAClippedSubject(t *testing.T) {
	t.Parallel()
	for _, lang := range realLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			tpl := realTemplates(t, lang)
			render := func(clipped bool) string {
				params := &renderParams{templates: tpl, key: "online", data: tplData{
					"streamer_link":   "alica_webcam",
					"subject":         "a room topic",
					"subject_clipped": clipped,
				}}
				return params.render("")
			}
			if got := render(false); strings.Contains(got, "…") {
				t.Errorf("a whole subject was marked as cut: %q", got)
			}
			if got := render(true); !strings.Contains(got, "a room topic…") {
				t.Errorf("a cut subject was not marked: %q", got)
			}
		})
	}
}

// Telegram rejects a photo caption past 1024 UTF-16 units, entities parsed out,
// which finalizes the notification and loses the picture with every field on it.
// The worst message the templates can build has to fit, which is what clipSubject is sized for.
func TestOnlineCaptionFitsTheTelegramLimit(t *testing.T) {
	t.Parallel()
	// Longer than any bot we run, and every command in the message pays for it.
	const mention = "@a_bot_name_of_thirty_two_charss"
	for _, lang := range realLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			// Emoji, so every unit the subject spends is a surrogate pair.
			subject, clipped := clipSubject(strings.Repeat("🟢", subjectLimit))
			if !clipped {
				t.Fatal("the subject under test was not clipped")
			}
			params := &renderParams{templates: realTemplates(t, lang), key: "online", data: tplData{
				"streamer_link":   `<a href="https://example.com/in/?tour=LQps&amp;campaign=WIl8t">a_long_streamer_name</a>`,
				"time_diff":       &timeDiff{Days: 9999, Hours: 23, Minutes: 59},
				"viewers":         999999999,
				"show_kind":       cmdlib.ShowTicket,
				"subject":         html.EscapeString(subject),
				"subject_clipped": clipped,
				"fields_hint":     true,
			}}
			visible := visibleText(params.render(mention))
			if units := utf16Units(visible); units > 1024 {
				t.Errorf("the worst caption measures %d units, over Telegram's 1024: %q", units, visible)
			}
		})
	}
}

// A topic past subjectLimit reaches the chat cut and marked, inside the caption's limit.
// The real templates render, tying notifyOfStatus's clip to the mark the message prints,
// which a stub that renders neither cannot.
func TestOnlinePicsClipALongSubject(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.tpl["test"] = realTemplates(t, "en")

	const nickname = "online_model"
	const url = "http://" + nickname + ".jpg"
	insertTestStreamer(&w.db, db.Streamer{Nickname: nickname, UnconfirmedStatus: cmdlib.StatusOnline})
	insertSubscription(&w.db, "test", 1, nickname)
	// Emoji, so the topic spends two units a character and overruns the limit twice over.
	w.unconfirmedOnlineStreamers[nickname] = cmdlib.StreamerInfo{
		ImageURL: url,
		Subject:  strings.Repeat("🟢", subjectLimit),
	}
	w.listOnlineStreamers(testMessage(w, 1, "pics", 100))

	nots := w.db.NewNotifications()
	if len(nots) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(nots))
	}
	w.enqueueNotifications(notificationBatch{
		notifications: nots,
		images:        map[string][]byte{url: []byte("image")},
	})

	q := w.sendQueue.pop()
	if q == nil {
		t.Fatal("the answer queued nothing")
	}
	caption, isPhoto := queuedText(t, q.message)
	if !isPhoto {
		t.Fatal("the message carries no picture, so no caption is under test")
	}
	if !strings.Contains(caption, "…</blockquote>") {
		t.Errorf("the cut topic reached the chat unmarked: %q", caption)
	}
	if units := utf16Units(visibleText(caption)); units > 1024 {
		t.Errorf("the caption measures %d units, over Telegram's 1024", units)
	}
}

func TestClipSubject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		subject string
		want    string
		wantCut bool
	}{
		{"a short topic is left alone", "a room topic", "a room topic", false},
		{
			"a topic at the limit is left alone",
			strings.Repeat("a", subjectLimit), strings.Repeat("a", subjectLimit), false,
		},
		{
			"a topic one past the limit is cut",
			strings.Repeat("a", subjectLimit+1), strings.Repeat("a", subjectLimit), true,
		},
		// Cyrillic is one UTF-16 unit apiece, so the limit counts characters and not bytes.
		{
			"multi-byte runes count once",
			strings.Repeat("я", subjectLimit), strings.Repeat("я", subjectLimit), false,
		},
		// An emoji is a surrogate pair, so half as many fit, and the cut lands on a rune boundary.
		{
			"astral runes count twice",
			strings.Repeat("🟢", subjectLimit), strings.Repeat("🟢", subjectLimit/2), true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, cut := clipSubject(tc.subject)
			if got != tc.want {
				t.Errorf("clipped to %d runes, want %d",
					utf8.RuneCountInString(got), utf8.RuneCountInString(tc.want))
			}
			if cut != tc.wantCut {
				t.Errorf("cut = %v, want %v", cut, tc.wantCut)
			}
			if units := utf16Units(got); units > subjectLimit {
				t.Errorf("clipped subject measures %d units, over the %d limit", units, subjectLimit)
			}
			if !utf8.ValidString(got) {
				t.Error("the cut split a rune")
			}
		})
	}
}
