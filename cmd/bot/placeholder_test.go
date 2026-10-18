package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// Each language's alerts have a placeholder, shipped in res/images.
func TestImageUnavailableShips(t *testing.T) {
	t.Parallel()
	base := filepath.Join("..", "..", "res")
	for _, lang := range realLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			trs, _ := cmdlib.LoadAllTranslations(map[string][]string{"cb": {
				filepath.Join(base, "translations", "common."+lang+".yaml"),
				filepath.Join(base, "translations", "chaturbate."+lang+".yaml"),
			}})
			image := trs["cb"].ImageUnavailable.Image
			if image == "" {
				t.Fatal("the translation names no image")
			}
			if _, err := os.Stat(filepath.Join(base, "images", image)); err != nil {
				t.Error(err)
			}
		})
	}
}
