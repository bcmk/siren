package main

import (
	"bytes"
	"context"
	"errors"
	goimage "image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	texttemplate "text/template"
	"time"

	"github.com/go-telegram/bot"

	"github.com/bcmk/siren/v6/internal/botconfig"
	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

const (
	photoSent = `{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"},"photo":[` +
		`{"file_id":"small","file_unique_id":"s","width":90,"height":51},` +
		`{"file_id":"large","file_unique_id":"l","width":640,"height":360}]}}`
	textSent      = `{"ok":true,"result":{"message_id":2,"chat":{"id":1,"type":"private"},"text":"text"}}`
	unknownFileID = `{"ok":false,"error_code":400,"description":"Bad Request: wrong file identifier/HTTP URL specified"}`
	chatNotFound  = `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`
)

// fakeTelegram plays scripted replies and records each request: an upload, an id, or text.
type fakeTelegram struct {
	replies []fakeReply
	chats   []string
	sent    []string
	texts   []string
	uploads []string
}

// textsByChat maps each chat to the text sent to it.
func (f *fakeTelegram) textsByChat() map[string]string {
	texts := map[string]string{}
	next := 0
	for i, sent := range f.sent {
		if sent == "text" {
			texts[f.chats[i]] = f.texts[next]
			next++
		}
	}
	return texts
}

// fakeReply is the API's answer, or a transport failure when err is set.
type fakeReply struct {
	body string
	err  error
}

func (f *fakeTelegram) Do(req *http.Request) (*http.Response, error) {
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		return nil, err
	}
	f.chats = append(f.chats, req.MultipartForm.Value["chat_id"][0])
	switch {
	case strings.HasSuffix(req.URL.Path, "/sendMessage"):
		f.sent = append(f.sent, "text")
		f.texts = append(f.texts, req.MultipartForm.Value["text"][0])
	case req.MultipartForm.File["photo"] != nil:
		f.sent = append(f.sent, "upload")
		file, err := req.MultipartForm.File["photo"][0].Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		f.uploads = append(f.uploads, string(data))
	default:
		f.sent = append(f.sent, req.MultipartForm.Value["photo"][0])
	}
	if len(f.replies) == 0 {
		return nil, errors.New("no reply scripted")
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(r.body))}, nil
}

func newFakeBot(t *testing.T, f *fakeTelegram) *bot.Bot {
	b, err := bot.New("token", bot.WithHTTPClient(time.Minute, f), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("cannot build the stub bot: %v", err)
	}
	return b
}

// pngImage is an image as a site serves it.
func pngImage(t *testing.T) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, goimage.NewGray(goimage.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// imageServer serves an image, 404 under /missing, and drops the connection under /down.
// Its client opens a connection per request, so the transport retries none.
func imageServer(t *testing.T) (*httptest.Server, *http.Client, *atomic.Int32) {
	data := pngImage(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case strings.HasPrefix(r.URL.Path, "/missing"):
			rw.WriteHeader(http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/down"):
			conn, _, err := rw.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		default:
			_, _ = rw.Write(data)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}, &hits
}

func TestShippedImage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tr   *cmdlib.Translation
		want *image
	}{
		{"a translation's image goes by its file name",
			&cmdlib.Translation{Image: "flag.png", ImageBytes: []byte("png")}, &image{key: "flag.png", data: []byte("png")}},
		{"a translation without one has none", &cmdlib.Translation{}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shippedImage(tc.tr)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("image = %+v, want %+v", got, tc.want)
			}
			if got != nil && (got.key != tc.want.key || !bytes.Equal(got.data, tc.want.data)) {
				t.Errorf("image = %+v, want %+v", got, tc.want)
			}
		})
	}
}

const testImageURL = "https://example.com/preview.jpg"

// cacheStart is when TestImageCache's steps count from.
var cacheStart = time.Unix(1000, 0)

// cacheStep is what the bot learns at the offset from cacheStart.
type cacheStep struct {
	at    time.Duration
	learn func(c *imageCache, now time.Time)
}

// sent learns a send's outcome.
func sent(at time.Duration, o photoOutcome) cacheStep {
	return cacheStep{at, func(c *imageCache, now time.Time) { c.learn(o, now) }}
}

// cacheView is what dispatch reads of an image.
type cacheView struct {
	id      string
	uploads bool
}

func TestImageCache(t *testing.T) {
	t.Parallel()
	const ttl = imageTTL
	site := &image{key: testImageURL}
	ad := &image{key: "flag.png", data: []byte("png")}
	uploaded := func(at time.Duration, img *image, id string) cacheStep {
		return sent(at, photoOutcome{key: img.key, uploadedID: id, downloadedAt: cacheStart.Add(at)})
	}
	timedOut := func(at time.Duration, img *image) cacheStep {
		return sent(at, photoOutcome{key: img.key, timedOut: true})
	}
	refused := func(at time.Duration, img *image) cacheStep {
		return sent(at, photoOutcome{key: img.key, rejected: true})
	}
	byID := func(id string) cacheView { return cacheView{id: id, uploads: true} }
	upload := cacheView{uploads: true}
	givenUp := cacheView{}
	// The rows look the image up for a message queued at queued, sent at at.
	tests := []struct {
		name   string
		steps  []cacheStep
		img    *image
		queued time.Duration
		at     time.Duration
		want   cacheView
	}{
		{"an upload's id sends its image",
			[]cacheStep{uploaded(0, site, "id")}, site, ttl - time.Second, ttl - time.Second, byID("id")},
		{"in a message queued in time, however late it is sent",
			[]cacheStep{uploaded(0, site, "id")}, site, ttl - time.Second, imageIDTTL - time.Second, byID("id")},
		{"a message queued an imageTTL after the download uploads afresh, though sends use the id",
			[]cacheStep{uploaded(0, site, "id"), sent(ttl/2, photoOutcome{key: site.key})}, site, ttl, ttl, upload},
		{"however late the upload",
			[]cacheStep{sent(ttl/2, photoOutcome{key: site.key, uploadedID: "id", downloadedAt: cacheStart})},
			site, ttl, ttl, upload},
		{"the bot keeps an id an imageIDTTL",
			[]cacheStep{uploaded(0, site, "id")}, site, 0, imageIDTTL, upload},
		{"a refused id is dropped",
			[]cacheStep{uploaded(0, site, "id"), refused(time.Second, site)}, site, 0, 2 * time.Second, upload},
		{"an upload after a refusal replaces the id",
			[]cacheStep{uploaded(0, site, "old"), refused(time.Second, site), uploaded(time.Second, site, "new")},
			site, 0, 2 * time.Second, byID("new")},
		{"an upload timeout gives the image up",
			[]cacheStep{timedOut(0, site)}, site, 0, ttl - time.Second, givenUp},
		{"the give-up lapses an imageTTL after the timeout",
			[]cacheStep{timedOut(0, site)}, site, 0, ttl, upload},
		{"a give-up keeps the id",
			[]cacheStep{uploaded(0, site, "id"), timedOut(time.Second, site)}, site, 0, 2 * time.Second,
			cacheView{id: "id"}},
		{"a refusal keeps the give-up",
			[]cacheStep{timedOut(0, site), refused(time.Second, site)}, site, 0, 2 * time.Second, givenUp},
		{"a shipped image's id serves any message for an imageIDTTL",
			[]cacheStep{uploaded(0, ad, "id")}, ad, imageIDTTL - time.Second, imageIDTTL - time.Second, byID("id")},
		{"and then expires",
			[]cacheStep{uploaded(0, ad, "id")}, ad, 0, imageIDTTL, upload},
		{"its give-up lapses an imageTTL after the timeout",
			[]cacheStep{timedOut(0, ad)}, ad, 0, ttl, upload},
		{"a shipped image gives up too",
			[]cacheStep{timedOut(0, ad)}, ad, 0, time.Second, givenUp},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var c imageCache
			for _, s := range tc.steps {
				s.learn(&c, cacheStart.Add(s.at))
			}
			var got cacheView
			got.id, got.uploads = c.lookup(tc.img, cacheStart.Add(tc.at), cacheStart.Add(tc.queued))
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestImageCacheSweeps(t *testing.T) {
	t.Parallel()
	t.Run("an expired id is dropped", func(t *testing.T) {
		t.Parallel()
		var c imageCache
		c.learn(photoOutcome{key: testImageURL, uploadedID: "id", downloadedAt: cacheStart}, cacheStart)
		c.learn(photoOutcome{key: "flag.png", uploadedID: "id"}, cacheStart.Add(imageIDTTL))
		if len(c.images) != 1 {
			t.Errorf("held %d images, want 1", len(c.images))
		}
	})
	t.Run("a give-up outlives a sweep", func(t *testing.T) {
		t.Parallel()
		var c imageCache
		c.learn(photoOutcome{}, cacheStart)
		c.learn(photoOutcome{key: testImageURL, timedOut: true}, cacheStart.Add(imageTTL/2))
		c.learn(photoOutcome{}, cacheStart.Add(imageTTL))
		if _, uploads := c.lookup(&image{key: testImageURL}, cacheStart.Add(imageTTL), cacheStart); uploads {
			t.Error("the image uploads before its give-up ends")
		}
	})
}

func TestPhotoSend(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		fileID   string
		replies  []fakeReply
		wantSent []string
		wantErr  error
	}{
		{"an image with no id uploads", "", []fakeReply{{body: photoSent}}, []string{"upload"}, nil},
		{"a known image goes by its id", "known", []fakeReply{{body: photoSent}}, []string{"known"}, nil},
		{"a refused id uploads nothing", "stale",
			[]fakeReply{{body: unknownFileID}}, []string{"stale"}, bot.ErrorBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeTelegram{replies: tc.replies}
			p := &photoParams{
				SendPhotoParams: &bot.SendPhotoParams{ChatID: int64(1)},
				image:           &image{key: testImageURL},
				fileID:          tc.fileID,
				upload:          []byte("image"),
			}
			_, err := p.send(context.Background(), newFakeBot(t, f))
			if !errors.Is(err, tc.wantErr) || (err == nil) != (tc.wantErr == nil) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
			if !slices.Equal(f.sent, tc.wantSent) {
				t.Errorf("sent %v, want %v", f.sent, tc.wantSent)
			}
		})
	}
}

func TestAttempt(t *testing.T) {
	t.Parallel()
	site := &image{key: testImageURL}
	timeout := fakeReply{err: os.ErrDeadlineExceeded}
	tests := []struct {
		name       string
		img        *image
		fileID     string
		reply      fakeReply
		wantSent   string
		wantResult int
		want       photoOutcome
	}{
		{"an upload learns its id", site, "", fakeReply{body: photoSent},
			"upload", messageSent, photoOutcome{fileUpload: true, uploadedID: "large"}},
		{"a held id sends the image", site, "known", fakeReply{body: photoSent},
			"known", messageSent, photoOutcome{}},
		{"a refused id is reported", site, "stale", fakeReply{body: unknownFileID},
			"stale", messageFileIDRejected, photoOutcome{rejected: true}},
		{"a timed-out upload is reported", site, "", timeout,
			"upload", messageTimeout, photoOutcome{fileUpload: true, timedOut: true}},
		{"a send by id that times out is no upload timeout", site, "known", timeout,
			"known", messageTimeout, photoOutcome{}},
		{"another bad request is no refusal", site, "known", fakeReply{body: chatNotFound},
			"known", messageChatNotFound, photoOutcome{}},
		{"an upload is never a refused id", site, "", fakeReply{body: unknownFileID},
			"upload", messageFileIDRejected, photoOutcome{fileUpload: true}},
		{name: "a skipped chat uploads nothing", img: site, wantResult: messageSkipped},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeTelegram{replies: []fakeReply{tc.reply}}
			cfg := &botconfig.Config{}
			var wantSent []string
			if tc.wantSent == "" {
				cfg.WhitelistChats = []int64{2}
			} else {
				wantSent = []string{tc.wantSent}
			}
			w := &worker{
				cfg:  cfg,
				bots: map[string]*bot.Bot{"ep": newFakeBot(t, f)},
			}
			p := &photoParams{
				SendPhotoParams: &bot.SendPhotoParams{ChatID: int64(1)},
				image:           tc.img,
				sentImage:       tc.img,
				fileID:          tc.fileID,
				upload:          []byte("image"),
			}
			result, _, _, got := w.attempt("ep", p)
			if result != tc.wantResult {
				t.Errorf("result = %d, want %d", result, tc.wantResult)
			}
			if !slices.Equal(f.sent, wantSent) {
				t.Errorf("sent %v, want %v", f.sent, wantSent)
			}
			tc.want.key = tc.img.key
			if got != tc.want {
				t.Errorf("outcome = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDeferredSendableTakesAnImage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		image     *image
		wantPhoto bool
	}{
		{"no image sends text", nil, false},
		{"an image sends a photo", &image{key: testImageURL}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var noRender *renderParams
			msg := noRender.asDeferredSendable(&cmdlib.Translation{}, true, tc.image, nil)
			p, isPhoto := msg.(*photoParams)
			if isPhoto != tc.wantPhoto {
				t.Fatalf("photo = %t, want %t", isPhoto, tc.wantPhoto)
			}
			if isPhoto && p.image != tc.image {
				t.Error("the photo does not carry the image")
			}
		})
	}
}

// An online notification queues a photo of its image's URL, once the image downloads.
func TestNotificationQueuesImageURL(t *testing.T) {
	t.Parallel()
	const nickname = "online_model"
	const url = "http://" + nickname + ".jpg"
	tests := []struct {
		name       string
		showImages bool
		wantPhoto  bool
	}{
		{"with images on it is a photo", true, true},
		{"with images off it is text", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.initCache()
			cfg := testConfig
			cfg.ShowImages = tc.showImages
			w.cfg = &cfg
			insertTestStreamer(&w.db, db.Streamer{Nickname: nickname, UnconfirmedStatus: cmdlib.StatusOnline})
			w.unconfirmedOnlineStreamers[nickname] = cmdlib.StreamerInfo{ImageURL: url}
			insertSubscription(&w.db, "test", 1, nickname)
			w.listOnlineStreamers(testMessage(w, 1, "pics", 100))
			w.enqueueNotifications(w.db.NewNotifications())
			completeDownloads(w)
			q := w.sendQueue.pop()
			if q == nil {
				t.Fatal("nothing was queued")
			}
			p, isPhoto := q.message.(*photoParams)
			if isPhoto != tc.wantPhoto {
				t.Fatalf("photo = %t, want %t", isPhoto, tc.wantPhoto)
			}
			if isPhoto && (p.image.key != url || p.image.data != nil) {
				t.Errorf("image = %+v, want %s to download", p.image, url)
			}
		})
	}
}

// A batch's notifications of one URL share an image, so the queue holds the URL once.
func TestBatchSharesImage(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	cfg := testConfig
	cfg.ShowImages = true
	w.cfg = &cfg
	const nickname = "online_model"
	const url = "http://" + nickname + ".jpg"
	insertTestStreamer(&w.db, db.Streamer{Nickname: nickname, UnconfirmedStatus: cmdlib.StatusOnline})
	w.unconfirmedOnlineStreamers[nickname] = cmdlib.StreamerInfo{ImageURL: url}
	for chatID := int64(1); chatID <= 2; chatID++ {
		insertSubscription(&w.db, "test", chatID, nickname)
		w.listOnlineStreamers(testMessage(w, chatID, "pics", 100))
	}
	w.enqueueNotifications(w.db.NewNotifications())
	completeDownloads(w)
	var imgs []*image
	for w.sendQueue.Len() > 0 {
		if p, ok := w.sendQueue.pop().message.(*photoParams); ok {
			imgs = append(imgs, p.image)
		}
	}
	if len(imgs) != 2 {
		t.Fatalf("queued %d photos, want 2", len(imgs))
	}
	if imgs[0] != imgs[1] {
		t.Error("the photos carry images of their own")
	}
}

// A photo whose image cannot upload becomes text rendered afresh with image_unavailable set,
// whether it rendered already or carries no data, as an ad does.
// The photo's own caption stays as it was, for a later dispatch that sends the photo.
func TestUnavailableTextRendersAfresh(t *testing.T) {
	t.Parallel()
	tpl := texttemplate.Must(texttemplate.New("online").Parse(
		`{{ if .image_unavailable }}[unavailable] {{ end }}caption`))
	tests := []struct {
		name     string
		data     tplData
		rendered bool
	}{
		{"a photo yet to render", tplData{}, false},
		{"a rendered photo", tplData{}, true},
		{"a photo with no data", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &renderParams{templates: tpl, key: "online", data: tc.data}
			p := d.asDeferredPhoto(true, true, cmdlib.ParseHTML, &image{key: testImageURL})
			p.setChatID(1)
			if tc.rendered {
				p.render("")
			}
			m := p.toUnavailableText()
			m.render("")
			if m.Text != "[unavailable] caption" {
				t.Errorf("text = %q, want %q", m.Text, "[unavailable] caption")
			}
			p.render("")
			if p.Caption != "caption" {
				t.Errorf("the photo's caption = %q, want %q", p.Caption, "caption")
			}
		})
	}
}

// photoSendWorker is a test worker whose endpoint sends through f and downloads from client,
// with users for the chat ids.
func photoSendWorker(t *testing.T, f *fakeTelegram, client *http.Client, chatIDs ...int64) (*testWorker, []db.UserID) {
	w := newTestWorker()
	t.Cleanup(w.terminate)
	w.createDatabase()
	w.bots = map[string]*bot.Bot{"test": newFakeBot(t, f)}
	w.client = client
	w.tpl["test"] = realTemplates(t, "en")
	tr := testTranslations
	tr.ImageUnavailable = &cmdlib.Translation{Image: "image-unavailable.en.png", ImageBytes: []byte("placeholder")}
	w.tr["test"] = &tr
	w.sender("test").cooling = false
	users := make([]db.UserID, len(chatIDs))
	for i, chatID := range chatIDs {
		users[i], _ = w.db.AddUser(chatID, 3, 0, "private")
	}
	return w, users
}

// testNotificationID marks a test's message as a notification, which waits for its image.
const testNotificationID = 1

// queueImages queues an online alert of each image to its user, as text where it is nil.
func queueImages(w *testWorker, users []db.UserID, imgs []*image) {
	tr := &cmdlib.Translation{Key: "online", Parse: cmdlib.ParseHTML}
	for i, u := range users {
		w.enqueueTr(db.PriorityLow, "test", u, true, tr, tplData{}, imgs[i], nil, unprompted(db.MessagePacket),
			testNotificationID)
	}
}

// completeSends runs the worker's events until n send results complete.
func completeSends(w *testWorker, n int) {
	for done := 0; done < n; {
		select {
		case r := <-w.sendResults:
			done++
			w.completeSendResult(r)
		case u := <-w.cooledUsers:
			w.onUserCooled(u)
		case d := <-w.imageDownloader.results:
			w.completeDownload(d)
		}
	}
}

// sendImages queues an online alert of each image to its user, then completes n send results.
func sendImages(w *testWorker, users []db.UserID, imgs []*image, n int) {
	queueImages(w, users, imgs)
	completeSends(w, n)
}

// An upload's id reaches the endpoint's next send of the image, so a fan-out uploads once.
func TestFanOutUploadsOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		replies       []fakeReply
		results       int
		wantSent      []string
		wantDownloads int
		wantRefusals  int
		// wantUploads counts the logged sends that carried the image's bytes, then those that did not.
		wantUploads [2]int
	}{
		{"the next sends name the uploaded id",
			[]fakeReply{{body: photoSent}, {body: photoSent}, {body: photoSent}},
			3, []string{"upload", "large", "large"}, 1, 0, [2]int{1, 2}},
		{"a refused id gives way to the next photo's upload, whose id the refused one goes by",
			[]fakeReply{{body: photoSent}, {body: unknownFileID}, {body: photoSent}, {body: photoSent}},
			4, []string{"upload", "large", "upload", "large"}, 1, 1, [2]int{2, 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, client, hits := imageServer(t)
			f := &fakeTelegram{replies: tc.replies}
			w, users := photoSendWorker(t, f, client, 101, 102, 103)
			var imgs []*image
			for range users {
				imgs = append(imgs, &image{key: srv.URL + "/preview.jpg"})
			}
			sendImages(w, users, imgs, tc.results)
			if !slices.Equal(f.sent, tc.wantSent) {
				t.Errorf("sent %v, want %v", f.sent, tc.wantSent)
			}
			for _, upload := range f.uploads {
				if upload != string(pngImage(t)) {
					t.Errorf("uploaded %d bytes, want the downloaded image", len(upload))
				}
			}
			if got := int(hits.Load()); got != tc.wantDownloads {
				t.Errorf("downloads = %d, want %d", got, tc.wantDownloads)
			}
			logged := w.db.MustInt(
				`select count(*) from performance_log where kind = $1`,
				db.PerformanceLogImageDownload)
			if logged != tc.wantDownloads {
				t.Errorf("downloads logged = %d, want %d", logged, tc.wantDownloads)
			}
			refusals := w.db.MustInt(`select count(*) from sent_message_log where result = $1`, messageFileIDRejected)
			if refusals != tc.wantRefusals {
				t.Errorf("refusals logged = %d, want %d", refusals, tc.wantRefusals)
			}
			uploads := [2]int{
				w.db.MustInt(`select count(*) from sent_message_log where file_upload`),
				w.db.MustInt(`select count(*) from sent_message_log where not file_upload`),
			}
			if uploads != tc.wantUploads {
				t.Errorf("uploads and other sends logged = %v, want %v", uploads, tc.wantUploads)
			}
			if sent := w.db.MustInt(`select count(*) from sent_message_log where result = $1`, messageSent); sent != len(users) {
				t.Errorf("sends logged = %d, want %d", sent, len(users))
			}
		})
	}
}

// A timed-out upload retries as its caption after timedOutUploadPostpone, unranked by a stall.
func TestTimedOutUploadPostponesBriefly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		chatID     int64
		fileID     string
		want       time.Duration
		wantText   bool
		wantStalls int
	}{
		{"a private chat waits timedOutUploadPostpone", 1, "", timedOutUploadPostpone, true, 0},
		{"a group still waits its cooldown", -100, "", groupCooldown, true, 0},
		{"a timed-out send by id keeps the transport postpone", 1, "known", transientErrorPostpone, false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newFakeBot(t, &fakeTelegram{replies: []fakeReply{{err: os.ErrDeadlineExceeded}}})
			synctest.Test(t, func(t *testing.T) {
				w := &worker{
					cfg:         &botconfig.Config{},
					bots:        map[string]*bot.Bot{"ep": b},
					sendResults: make(chan msgSendResult, 16),
					cooledUsers: make(chan cooledUser, 16),
				}
				p := &photoParams{
					SendPhotoParams: &bot.SendPhotoParams{ChatID: tc.chatID},
					image:           &image{key: "flag.png", data: []byte("png")},
					sentImage:       &image{key: "flag.png", data: []byte("png")},
					fileID:          tc.fileID,
					upload:          []byte("png"),
				}
				start := time.Now()
				go w.deliver(&queuedMessage{
					userID:   1,
					endpoint: "ep",
					message:  p,
					order:    queueOrder{priority: db.PriorityLow},
					tag:      unprompted(db.NotificationPacket),
				}, p)
				r := <-w.sendResults
				if r.result != messageTimeout {
					t.Fatalf("result = %d, want a timeout", r.result)
				}
				if _, text := r.resend.message.(*messageParams); text != tc.wantText {
					t.Errorf("retry as text = %t, want %t", text, tc.wantText)
				}
				if r.resend.stalls != tc.wantStalls {
					t.Errorf("stalls = %d, want %d", r.resend.stalls, tc.wantStalls)
				}
				<-w.cooledUsers
				if d := time.Since(start); d != tc.want {
					t.Errorf("user freed after %v, want %v", d, tc.want)
				}
			})
		})
	}
}

// An image whose upload times out turns its alerts to text for a while,
// and the timed-out chat gets its caption alone, since the photo may have reached it.
func TestUnsendableImageGoesAsText(t *testing.T) {
	t.Parallel()
	srv, client, hits := imageServer(t)
	f := &fakeTelegram{replies: []fakeReply{
		{err: os.ErrDeadlineExceeded}, {body: textSent}, {body: textSent}, {body: textSent},
	}}
	w, users := photoSendWorker(t, f, client, 101, 102, 103)
	img := &image{key: srv.URL + "/a"}
	sendImages(w, users, []*image{img, img, img}, 4)
	if want := []string{"upload", "text", "text", "text"}; !slices.Equal(f.sent, want) {
		t.Fatalf("sent %v, want %v", f.sent, want)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("downloads = %d, want 1", got)
	}
	texts := f.textsByChat()
	for chat, prefix := range map[string]string{
		"101": "🟢", "102": "[[ 🚫 image unavailable ]]\n\n🟢", "103": "[[ 🚫 image unavailable ]]\n\n🟢",
	} {
		if !strings.HasPrefix(texts[chat], prefix) {
			t.Errorf("text to %s = %q, want it to open with %q", chat, texts[chat], prefix)
		}
	}
}

// A photo uploads the bytes downloaded for it, however long it waited since.
func TestDownloadedBytesUploadHoweverOld(t *testing.T) {
	t.Parallel()
	srv, client, hits := imageServer(t)
	f := &fakeTelegram{replies: []fakeReply{{body: photoSent}}}
	w, users := photoSendWorker(t, f, client, 101)
	p := &photoParams{
		SendPhotoParams: &bot.SendPhotoParams{},
		image:           &image{key: srv.URL + "/a.jpg"},
		upload:          []byte("held"),
		downloadedAt:    time.Now().Add(-2 * imageTTL),
	}
	w.enqueueMessage(db.PriorityLow, "test", p, unprompted(db.MessagePacket), users[0], 0)
	completeSends(w, 1)
	if got := hits.Load(); got != 0 {
		t.Errorf("downloads = %d, want 0", got)
	}
	if !slices.Equal(f.uploads, []string{"held"}) {
		t.Errorf("uploaded %q, want the held bytes", f.uploads)
	}
}

// A photo goes by an id serving it, though its image was given up.
func TestGivenUpImageGoesByID(t *testing.T) {
	t.Parallel()
	f := &fakeTelegram{replies: []fakeReply{{body: photoSent}}}
	w, users := photoSendWorker(t, f, &http.Client{Transport: noNetwork{}}, 101)
	img := &image{key: testImageURL}
	s := w.sender("test")
	now := time.Now()
	s.images.learn(photoOutcome{key: img.key, uploadedID: "id", downloadedAt: now}, now)
	s.images.learn(photoOutcome{key: img.key, timedOut: true}, now)
	sendImages(w, users, []*image{img}, 1)
	if !slices.Equal(f.sent, []string{"id"}) {
		t.Errorf("sent %v, want the photo by its id", f.sent)
	}
}

// A photo queued while its image's uploads are paused still takes the image's bytes:
// it goes as text while the pause lasts, and uploads them once it lapses.
func TestPausedImageStillDownloads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		lapsed   bool
		reply    fakeReply
		wantSent string
	}{
		{"while the pause lasts", false, fakeReply{body: textSent}, "text"},
		{"once it lapses", true, fakeReply{body: photoSent}, "upload"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, client, hits := imageServer(t)
			f := &fakeTelegram{replies: []fakeReply{tc.reply}}
			w, users := photoSendWorker(t, f, client, 101)
			s := w.sender("test")
			s.cooling = true
			img := &image{key: srv.URL + "/a.jpg"}
			s.images.learn(photoOutcome{key: img.key, timedOut: true}, time.Now())
			queueImages(w, users, []*image{img})
			w.completeDownload(<-w.imageDownloader.results)
			if tc.lapsed {
				e := s.images.images[img.key]
				e.gaveUpUntil = time.Now()
				s.images.images[img.key] = e
			}
			w.onSendDone("test")
			completeSends(w, 1)
			if !slices.Equal(f.sent, []string{tc.wantSent}) {
				t.Errorf("sent %v, want %s", f.sent, tc.wantSent)
			}
			if got := hits.Load(); got != 1 {
				t.Errorf("downloads = %d, want 1", got)
			}
		})
	}
}

// A photo whose download failed still goes by an id serving it, with no placeholder logged.
func TestFailedDownloadGoesByID(t *testing.T) {
	t.Parallel()
	srv, client, _ := imageServer(t)
	f := &fakeTelegram{replies: []fakeReply{{body: photoSent}}}
	w, users := photoSendWorker(t, f, client, 101)
	s := w.sender("test")
	s.cooling = true
	img := &image{key: srv.URL + "/missing"}
	queueImages(w, users, []*image{img})
	w.completeDownload(<-w.imageDownloader.results)
	// Another chat's upload of a later download gives the bot an id.
	s.images.learn(photoOutcome{key: img.key, uploadedID: "id", downloadedAt: time.Now()}, time.Now())
	s.cooling = false
	w.trySend("test")
	completeSends(w, 1)
	if !slices.Equal(f.sent, []string{"id"}) {
		t.Errorf("sent %v, want the photo by its id", f.sent)
	}
}

// A photo that reaches its turn with neither an id nor bytes goes as text,
// opening with the image-unavailable line.
func TestPhotoWithoutImageGoesAsText(t *testing.T) {
	t.Parallel()
	expire := func(s *endpointSender, key string) {
		e := s.images.images[key]
		e.idExpires = time.Now()
		s.images.images[key] = e
	}
	tests := []struct {
		name     string
		known    photoOutcome
		atTurn   func(s *endpointSender, key string)
		replies  []fakeReply
		wantSent []string
	}{
		{"its id refused", photoOutcome{uploadedID: "stale"}, nil,
			[]fakeReply{{body: unknownFileID}, {body: textSent}}, []string{"stale", "text"}},
		{"its id expired", photoOutcome{uploadedID: "id"}, expire, []fakeReply{{body: textSent}}, []string{"text"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, client, hits := imageServer(t)
			f := &fakeTelegram{replies: tc.replies}
			w, users := photoSendWorker(t, f, client, 101)
			s := w.sender("test")
			s.cooling = true
			img := &image{key: srv.URL + "/a.jpg"}
			now := time.Now()
			o := tc.known
			o.key, o.downloadedAt = img.key, now
			s.images.learn(o, now)
			queueImages(w, users, []*image{img})
			if tc.atTurn != nil {
				tc.atTurn(s, img.key)
			}
			s.cooling = false
			w.trySend("test")
			completeSends(w, len(tc.replies))
			if !slices.Equal(f.sent, tc.wantSent) {
				t.Errorf("sent %v, want %v", f.sent, tc.wantSent)
			}
			if !strings.HasPrefix(f.texts[0], "[[ 🚫 image unavailable ]]") {
				t.Errorf("text = %q, want it to open with the image-unavailable line", f.texts[0])
			}
			if got := hits.Load(); got != 0 {
				t.Errorf("downloads = %d, want 0", got)
			}
		})
	}
}

// An image that fails to download gives way to the placeholder,
// whether its host answers without it or not at all.
func TestUnreachableImageShowsPlaceholder(t *testing.T) {
	t.Parallel()
	tooMany := fakeReply{
		body: `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`,
	}
	sent := fakeReply{body: photoSent}
	tests := []struct {
		name     string
		path     string
		chats    int
		replies  []fakeReply
		wantSent []string
	}{
		{"a missing image", "/missing", 2, []fakeReply{sent, sent}, []string{"upload", "large"}},
		{"an image its host fails to answer", "/down", 2, []fakeReply{sent, sent}, []string{"upload", "large"}},
		{"a placeholder alert sent again after a 429", "/missing", 2,
			[]fakeReply{tooMany, sent, sent}, []string{"upload", "upload", "large"}},
		{"and uploaded again", "/missing", 1, []fakeReply{tooMany, sent}, []string{"upload", "upload"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, client, hits := imageServer(t)
			f := &fakeTelegram{replies: tc.replies}
			w, users := photoSendWorker(t, f, client, []int64{101, 102}[:tc.chats]...)
			img := &image{key: srv.URL + tc.path}
			sendImages(w, users, slices.Repeat([]*image{img}, tc.chats), len(tc.replies))
			if !slices.Equal(f.sent, tc.wantSent) {
				t.Errorf("sent %v, want %v", f.sent, tc.wantSent)
			}
			for _, upload := range f.uploads {
				if upload != "placeholder" {
					t.Errorf("uploaded %q, want the placeholder", upload)
				}
			}
			if got := hits.Load(); got != 1 {
				t.Errorf("downloads = %d, want 1", got)
			}
			now := time.Now()
			s := w.sender("test")
			if id, _ := s.images.lookup(img, now, now); id != "" {
				t.Errorf("the image's URL got the id %q, want none", id)
			}
			if id, _ := s.images.lookup(shippedImage(w.tr["test"].ImageUnavailable), now, now); id != "large" {
				t.Errorf("the placeholder's id = %q, want %q", id, "large")
			}
		})
	}
}

// A failed download goes as text when the translations ship no placeholder.
func TestFailedDownloadWithoutPlaceholderGoesAsText(t *testing.T) {
	t.Parallel()
	srv, client, _ := imageServer(t)
	f := &fakeTelegram{replies: []fakeReply{{body: textSent}}}
	w, users := photoSendWorker(t, f, client, 101)
	w.tr["test"].ImageUnavailable = &cmdlib.Translation{}
	sendImages(w, users, []*image{{key: srv.URL + "/missing"}}, 1)
	if !slices.Equal(f.sent, []string{"text"}) {
		t.Errorf("sent %v, want text", f.sent)
	}
}

// Each dispatch chooses what a photo goes as, so a resend goes by an id the bot learned meanwhile,
// whether the first send carried the placeholder or went as text.
func TestResendGoesByALaterID(t *testing.T) {
	t.Parallel()
	tooMany := fakeReply{
		body: `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`,
	}
	// The resend after the placeholder is refused, so the refusal must drop the image's id,
	// and the next send uploads the placeholder again.
	tests := []struct {
		name     string
		path     string
		gaveUp   bool
		replies  []fakeReply
		wantSent []string
	}{
		{"after the placeholder", "/missing", false,
			[]fakeReply{tooMany, {body: unknownFileID}, {body: photoSent}}, []string{"upload", "id", "upload"}},
		{"after the text", "/a.jpg", true, []fakeReply{tooMany, {body: photoSent}}, []string{"text", "id"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, client, _ := imageServer(t)
			f := &fakeTelegram{replies: tc.replies}
			w, users := photoSendWorker(t, f, client, 101)
			s := w.sender("test")
			img := &image{key: srv.URL + tc.path}
			if tc.gaveUp {
				s.images.learn(photoOutcome{key: img.key, timedOut: true}, time.Now())
			}
			queueImages(w, users, []*image{img})
			completeSends(w, 1)
			now := time.Now()
			s.images.learn(photoOutcome{key: img.key, uploadedID: "id", downloadedAt: now}, now)
			completeSends(w, len(tc.replies)-1)
			if !slices.Equal(f.sent, tc.wantSent) {
				t.Errorf("sent %v, want %v", f.sent, tc.wantSent)
			}
		})
	}
}
