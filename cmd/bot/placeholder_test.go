package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// An online alert whose picture failed to download carries the placeholder instead.
func TestFailedDownloadShowsPlaceholder(t *testing.T) {
	t.Parallel()
	const nickname = "online_model"
	const url = "http://" + nickname + ".jpg"
	tests := []struct {
		name   string
		images map[string][]byte
		want   string
	}{
		{"a downloaded picture goes as it is", map[string][]byte{url: []byte("image")}, "image"},
		{"one that failed gives way to the placeholder", map[string][]byte{}, "placeholder"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.initCache()
			cfg := testConfig
			cfg.ShowImages = true
			w.cfg = &cfg
			tr := testTranslations
			tr.ImageUnavailable = &cmdlib.Translation{ImageBytes: []byte("placeholder")}
			w.tr["test"] = &tr
			insertTestStreamer(&w.db, db.Streamer{Nickname: nickname, UnconfirmedStatus: cmdlib.StatusOnline})
			w.unconfirmedOnlineStreamers[nickname] = cmdlib.StreamerInfo{ImageURL: url}
			insertSubscription(&w.db, "test", 1, nickname)
			w.listOnlineStreamers(testMessage(w, 1, "pics", 100))
			w.enqueueNotifications(notificationBatch{notifications: w.db.NewNotifications(), images: tc.images})
			q := w.sendQueue.pop()
			if q == nil {
				t.Fatal("nothing was queued")
			}
			p, ok := q.message.(*photoParams)
			if !ok {
				t.Fatal("the alert is not a photo")
			}
			if !bytes.Equal(p.imageData, []byte(tc.want)) {
				t.Errorf("picture = %q, want %q", p.imageData, tc.want)
			}
		})
	}
}

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
				t.Fatal("the translation names no picture")
			}
			if _, err := os.Stat(filepath.Join(base, "images", image)); err != nil {
				t.Error(err)
			}
		})
	}
}
