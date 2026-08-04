package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	texttemplate "text/template"

	"github.com/bcmk/siren/v4/internal/db"
	"github.com/bcmk/siren/v4/lib/cmdlib"
)

// realLangs are the languages a chaturbate endpoint ships,
// each carrying its own template bodies that only a render can vet.
var realLangs = []string{"en", "ru"}

// realTemplates loads the translation set a chaturbate endpoint serves in one language.
func realTemplates(t *testing.T, lang string) *texttemplate.Template {
	t.Helper()
	base := filepath.Join("..", "..", "res", "translations")
	_, tpl := cmdlib.LoadAllTranslations(map[string][]string{"cb": {
		filepath.Join(base, "common."+lang+".yaml"),
		filepath.Join(base, "chaturbate."+lang+".yaml"),
	}})
	return tpl["cb"]
}

// TestWeekChunkSeparatesRows pins the fragment that replaced a Go-side join:
// it dispatches each row through the week template and puts a blank line between rows.
func TestWeekChunkSeparatesRows(t *testing.T) {
	t.Parallel()
	for _, lang := range realLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			tpl := realTemplates(t, lang)
			chunk := func(rows ...tplData) string {
				params := &renderParams{templates: tpl, key: "week_chunk", data: tplData{"rows": rows}}
				return params.render("")
			}
			first := tplData{
				"hours":         make([]bool, 24),
				"weekday":       0,
				"timezone":      "UTC",
				"streamer_link": "alica_webcam",
			}
			second := tplData{
				"hours":         make([]bool, 24),
				"weekday":       3,
				"timezone":      "UTC",
				"streamer_link": "bob_cam",
			}

			one := chunk(first)
			if !strings.Contains(one, "alica_webcam") {
				t.Fatalf("a row does not render through the week template: %q", one)
			}
			if got, want := chunk(first, second), one+"\n\n"+chunk(second); got != want {
				t.Errorf("two rows = %q, want %q", got, want)
			}
			if got := chunk(); got != "" {
				t.Errorf("no rows = %q, want empty", got)
			}
		})
	}
}

// TestWeekTemplateRowCount ties the template's chunking to the window weekWindow hands it:
// a row is broken every 24 cells, so a window past weekHours prints a day the week does not hold.
// English alone, since the row labels are what the count reads and every language spells them anew.
func TestWeekTemplateRowCount(t *testing.T) {
	t.Parallel()
	row := regexp.MustCompile(`(?m)^[A-Z][a-z]: `)
	tpl := realTemplates(t, "en")
	for _, tc := range []struct {
		cells int
		rows  int
	}{
		{1, 1},
		{24, 1},
		{144, 6},
		{weekHours, 7},
		// One cell past the window weekWindow can return, and the eighth row it would open.
		{weekHours + 1, 8},
	} {
		t.Run(fmt.Sprintf("%d cells", tc.cells), func(t *testing.T) {
			t.Parallel()
			params := &renderParams{templates: tpl, key: "week", data: tplData{
				"hours":         make([]bool, tc.cells),
				"weekday":       0,
				"timezone":      "UTC",
				"streamer_link": "alica_webcam",
			}}
			if got := len(row.FindAllString(params.render(""), -1)); got != tc.rows {
				t.Errorf("%d cells rendered %d rows, want %d", tc.cells, got, tc.rows)
			}
		})
	}
}

// The never-online branch feeds streamerListEntry rows to week_never_online;
// a subscription with no online hours must come back in that reply, not vanish.
// The real templates render, tying the call site's data keys to the template's ranges,
// which a stub that ignores its data cannot.
func TestShowWeekNeverOnline(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	w.tpl["test"] = realTemplates(t, "en")
	m := testMessage(w, 10, "week", 100)
	id := insertTestStreamer(&w.db, db.Streamer{Nickname: "ghost_model"})
	w.db.AddSubscription(m.userID, id, "test")
	w.showWeek(m, "")
	if n := w.sendQueue.Len(); n != 2 {
		t.Fatalf("queued replies = %d, want 2", n)
	}
	retrieving := w.sendQueue.pop()
	retrieving.message.render("")
	if got := retrieving.message.(*messageParams).Text; got == "" {
		t.Error("the retrieving notice rendered empty")
	}
	never := w.sendQueue.pop()
	never.message.render("")
	if got := never.message.(*messageParams).Text; !strings.Contains(got, "ghost_model") {
		t.Errorf("the never-online reply lost the streamer: %q", got)
	}
}

// TestEntryTemplatesRender feeds the real templates streamerListEntry rows in every language,
// so a field rename or a template field typo cannot pass the suite and panic at dispatch.
// The wants are the names the fields carry in, the same in every language.
func TestEntryTemplatesRender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  string
		data tplData
		want []string
	}{
		{
			"never online", "week_never_online",
			tplData{"streamers": []streamerListEntry{
				{Link: "alica_webcam"},
				{Link: "bob_cam", TimeDiff: &timeDiff{Hours: 3}},
			}},
			[]string{"alica_webcam", "bob_cam"},
		},
		{
			"list", "list",
			tplData{
				"online":  []streamerListEntry{{Link: "alica_webcam", TimeDiff: &timeDiff{Hours: 3}}},
				"offline": []streamerListEntry{{Link: "bob_cam"}},
			},
			[]string{"alica_webcam", "bob_cam"},
		},
	}
	for _, lang := range realLangs {
		for _, tc := range tests {
			t.Run(lang+" "+tc.name, func(t *testing.T) {
				t.Parallel()
				params := &renderParams{templates: realTemplates(t, lang), key: tc.key, data: tc.data}
				out := params.render("")
				for _, want := range tc.want {
					if !strings.Contains(out, want) {
						t.Errorf("rendered %s misses %q: %q", tc.key, want, out)
					}
				}
			})
		}
	}
}
