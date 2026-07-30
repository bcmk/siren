package main

import (
	"path/filepath"
	"strings"
	"testing"
	texttemplate "text/template"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// TestWeekChunkSeparatesRows pins the fragment that replaced a Go-side join:
// it dispatches each row through the week template and puts a blank line between rows.
func TestWeekChunkSeparatesRows(t *testing.T) {
	t.Parallel()
	base := filepath.Join("..", "..", "res", "translations")
	_, tpl := cmdlib.LoadAllTranslations(map[string][]string{"cb": {
		filepath.Join(base, "common.en.yaml"),
		filepath.Join(base, "chaturbate.en.yaml"),
	}})
	// affiliate_link comes from config, not from the translation files.
	texttemplate.Must(tpl["cb"].New("affiliate_link").Parse("{{ . }}"))
	chunk := func(rows ...tplData) string {
		params := &renderParams{templates: tpl["cb"], key: "week_chunk", data: tplData{"rows": rows}}
		return params.render("")
	}
	first := tplData{"hours": make([]bool, 24), "weekday": 0, "streamer": "alica_webcam"}
	second := tplData{"hours": make([]bool, 24), "weekday": 3, "streamer": "bob_cam"}

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
}
