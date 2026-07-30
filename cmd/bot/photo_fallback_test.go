package main

import (
	"testing"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// A photo sent where the chat forbids photos falls back to its caption as text.
// online is the one image-bearing translation, and it disables previews,
// so the fallback must disable them too rather than show a card the translation refused.
func TestPhotoFallbackKeepsPreviewSetting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		disablePreview bool
		want           bool
	}{
		{"translation disables previews", true, true},
		{"translation allows previews", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := &cmdlib.Translation{Key: "online", DisablePreview: tc.disablePreview}
			var noRender *renderParams
			photo := noRender.asDeferredSendable(tr, true, []byte("image")).(*photoParams)
			photo.Caption = "already rendered"
			text := photo.toText()
			disabled := text.LinkPreviewOptions != nil &&
				text.LinkPreviewOptions.IsDisabled != nil &&
				*text.LinkPreviewOptions.IsDisabled
			if disabled != tc.want {
				t.Errorf("preview disabled = %v, want %v", disabled, tc.want)
			}
			if text.Text != "already rendered" {
				t.Errorf("caption did not carry across: %q", text.Text)
			}
		})
	}
}
