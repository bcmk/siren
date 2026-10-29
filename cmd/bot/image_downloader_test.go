package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

// gatedImageServer serves an image once free is called.
func gatedImageServer(t *testing.T) (srv *httptest.Server, free func()) {
	data := pngImage(t)
	release := make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = rw.Write(data)
	}))
	t.Cleanup(srv.Close)
	free = sync.OnceFunc(func() { close(release) })
	t.Cleanup(free)
	return srv, free
}

// An image downloads off the send slot, so the bot's other users get their messages meanwhile.
func TestDownloadLeavesTheSlotFree(t *testing.T) {
	t.Parallel()
	srv, free := gatedImageServer(t)
	f := &fakeTelegram{replies: []fakeReply{{body: textSent}, {body: photoSent}}}
	w, users := photoSendWorker(t, f, srv.Client(), 101, 102)
	queueImages(w, users, []*image{{key: srv.URL + "/a.jpg"}, nil})
	select {
	case r := <-w.sendResults:
		w.completeSendResult(r)
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was sent while the image downloads")
	}
	if !slices.Equal(f.sent, []string{"text"}) {
		t.Errorf("sent %v while the image downloads, want the other user's text", f.sent)
	}
	free()
	completeSends(w, 1)
	if !slices.Equal(f.sent, []string{"text", "upload"}) {
		t.Errorf("sent %v, want the photo after the text", f.sent)
	}
}

// A notification awaiting its image holds back its user's notifications and ads behind it in order,
// but not one ahead of it, nor a reply.
func TestHeldPhotoHoldsBackWhatFollows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		photo          db.Priority
		next           db.Priority
		tag            sendTag
		notificationID int
		wantSent       []string
	}{
		{"a notification behind it waits", db.PriorityLow, db.PriorityLow, unprompted(db.MessagePacket),
			testNotificationID, []string{"upload", "text"}},
		{"one ahead of it passes", db.PriorityLow, db.PriorityHigh, unprompted(db.MessagePacket),
			testNotificationID, []string{"text", "upload"}},
		{"an ad behind it waits", db.PriorityLow, db.PriorityLow, adTag(""), 0, []string{"upload", "text"}},
		{"a reply passes, though it is behind", db.PriorityHigh, db.PriorityHigh, reply("list"), 0,
			[]string{"text", "upload"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, free := gatedImageServer(t)
			f := &fakeTelegram{}
			for _, sent := range tc.wantSent {
				f.replies = append(f.replies, map[string]fakeReply{"upload": {body: photoSent}, "text": {body: textSent}}[sent])
			}
			w, users := photoSendWorker(t, f, srv.Client(), 101)
			tr := &cmdlib.Translation{Key: "online", Parse: cmdlib.ParseHTML}
			img := &image{key: srv.URL + "/a.jpg"}
			w.enqueueTr(tc.photo, "test", users[0], true, tr, tplData{}, img, nil, unprompted(db.MessagePacket),
				testNotificationID)
			w.enqueueMessage(tc.next, "test", textMessage("next", true, true, cmdlib.ParseRaw), tc.tag, users[0],
				tc.notificationID)
			passed := w.imageDownloader.size == 1
			if want := tc.wantSent[0] == "text"; passed != want {
				t.Errorf("the next message passed the photo = %t, want %t", passed, want)
			}
			free()
			completeSends(w, 2)
			if !slices.Equal(f.sent, tc.wantSent) {
				t.Errorf("sent %v, want %v", f.sent, tc.wantSent)
			}
			if n := len(w.imageDownloader.held); n != 0 {
				t.Errorf("the download step holds %d users once all are sent, want 0", n)
			}
			if n := w.imageDownloader.size; n != 0 {
				t.Errorf("the download step counts %d messages once all are sent, want 0", n)
			}
		})
	}
}

// A photo reaching a full download step waits for no download and goes with the placeholder,
// but still takes its image when it downloaded within an imageTTL.
func TestFullDownloadStepStartsNoDownload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		downloaded bool
	}{
		{"an image to download goes with the placeholder", false},
		{"an image downloaded within an imageTTL goes with it", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, client, hits := imageServer(t)
			f := &fakeTelegram{replies: []fakeReply{{body: photoSent}}}
			w, users := photoSendWorker(t, f, client, 101)
			img := &image{key: srv.URL + "/a.jpg"}
			if tc.downloaded {
				w.imageDownloader.images[img.key] = &imageDownload[*queuedMessage]{
					started: true, data: pngImage(t), downloadedAt: time.Now(),
				}
			}
			w.imageDownloader.size = maxDownloadBacklog
			queueImages(w, users, []*image{img})
			completeSends(w, 1)
			if !slices.Equal(f.sent, []string{"upload"}) {
				t.Fatalf("sent %v, want an upload", f.sent)
			}
			if placeholder := f.uploads[0] == "placeholder"; placeholder == tc.downloaded {
				t.Errorf("uploaded the placeholder = %t, want %t", placeholder, !tc.downloaded)
			}
			if got := hits.Load(); got != 0 {
				t.Errorf("downloads = %d, want 0", got)
			}
		})
	}
}

// A user's message awaiting its image on one endpoint holds back none of theirs on another.
func TestHeldMessageHoldsBackItsEndpointAlone(t *testing.T) {
	t.Parallel()
	srv, free := gatedImageServer(t)
	f := &fakeTelegram{replies: []fakeReply{{body: photoSent}}}
	other := &fakeTelegram{replies: []fakeReply{{body: textSent}}}
	w, users := photoSendWorker(t, f, srv.Client(), 101)
	w.bots["other"] = newFakeBot(t, other)
	w.tpl["other"], w.tr["other"] = w.tpl["test"], w.tr["test"]
	tr := &cmdlib.Translation{Key: "online", Parse: cmdlib.ParseHTML}
	w.enqueueTr(db.PriorityLow, "test", users[0], true, tr, tplData{}, &image{key: srv.URL + "/a.jpg"}, nil,
		unprompted(db.MessagePacket), testNotificationID)
	w.enqueueTr(db.PriorityLow, "other", users[0], true, tr, tplData{}, nil, nil, unprompted(db.MessagePacket),
		testNotificationID)
	select {
	case r := <-w.sendResults:
		w.completeSendResult(r)
	case <-time.After(5 * time.Second):
		t.Fatal("the other endpoint sent nothing while the image downloads")
	}
	if len(f.sent) != 0 || !slices.Equal(other.sent, []string{"text"}) {
		t.Errorf("sent %v and %v while the image downloads, want the other endpoint's text alone", f.sent, other.sent)
	}
	free()
	completeSends(w, 1)
	if !slices.Equal(f.sent, []string{"upload"}) {
		t.Errorf("sent %v once the image downloaded, want the upload", f.sent)
	}
}

// A user runs at most maxUserDownloads downloads, the next starting as one ends,
// before any photo is sent.
func TestUserDownloadsCap(t *testing.T) {
	t.Parallel()
	srv, client, hits := imageServer(t)
	w, users := photoSendWorker(t, &fakeTelegram{}, client, 101, 102)
	// The slot stays busy, so nothing is sent.
	w.sender("test").cooling = true
	for i := range maxUserDownloads + 1 {
		queueImages(w, users[:1], []*image{{key: fmt.Sprintf("%s/%d.jpg", srv.URL, i)}})
	}
	if w.imageDownloader.queue.running != maxUserDownloads {
		t.Errorf("%d downloading for one user, want %d", w.imageDownloader.queue.running, maxUserDownloads)
	}
	queueImages(w, users[1:], []*image{{key: srv.URL + "/other.jpg"}})
	if w.imageDownloader.queue.running != maxUserDownloads+1 {
		t.Errorf("%d downloading with another user's photo, want %d", w.imageDownloader.queue.running, maxUserDownloads+1)
	}
	completeDownloads(w)
	if got := int(hits.Load()); got != maxUserDownloads+2 {
		t.Errorf("downloads = %d, want %d", got, maxUserDownloads+2)
	}
	if n := len(w.imageDownloader.queue.byUser); n != 0 {
		t.Errorf("the download queue holds %d users once every download ended, want 0", n)
	}
}

// A notification's photo starts its download, unless the bot has cached an id for its image.
func TestQueuedPhotoStartsDownload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		known photoOutcome
		want  int
	}{
		{"a new image downloads", photoOutcome{}, 1},
		{"an image the bot has cached an id for does not", photoOutcome{uploadedID: "id"}, 0},
		{"an image whose uploads are paused downloads too", photoOutcome{timedOut: true}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, client, _ := imageServer(t)
			w, users := photoSendWorker(t, &fakeTelegram{}, client, 101)
			s := w.sender("test")
			s.cooling = true
			img := &image{key: srv.URL + "/a.jpg"}
			o := tc.known
			o.key, o.downloadedAt = img.key, time.Now()
			s.images.learn(o, time.Now())
			queueImages(w, users, []*image{img})
			if w.imageDownloader.queue.running != tc.want {
				t.Errorf("%d downloading, want %d", w.imageDownloader.queue.running, tc.want)
			}
		})
	}
}

// A freed slot goes to the user's first photo in order, though it was queued last.
func TestFreedSlotGoesToBestPhoto(t *testing.T) {
	t.Parallel()
	srv, free := gatedImageServer(t)
	w, users := photoSendWorker(t, &fakeTelegram{}, srv.Client(), 101)
	w.sender("test").cooling = true
	alerts := make([]string, maxUserDownloads+1)
	for i := range alerts {
		alerts[i] = fmt.Sprintf("%s/%d.jpg", srv.URL, i)
		queueImages(w, users, []*image{{key: alerts[i]}})
	}
	tr := &cmdlib.Translation{Key: "online", Parse: cmdlib.ParseHTML}
	img := &image{key: srv.URL + "/reply.jpg"}
	w.enqueueTr(db.PriorityHigh, "test", users[0], true, tr, tplData{}, img, nil, reply("pics"), testNotificationID)
	free()
	w.completeDownload(<-w.imageDownloader.results)
	if !w.imageDownloader.images[img.key].started || w.imageDownloader.images[alerts[maxUserDownloads]].started {
		t.Error("the freed slot went to the alert queued earlier, not to the reply")
	}
}

// A download starting takes the other photos of its image out of their users' queues.
func TestStartedDownloadTakesItsPhotos(t *testing.T) {
	t.Parallel()
	srv, _ := gatedImageServer(t)
	w, users := photoSendWorker(t, &fakeTelegram{}, srv.Client(), 101, 102)
	w.sender("test").cooling = true
	for i := range maxUserDownloads {
		queueImages(w, users[:1], []*image{{key: fmt.Sprintf("%s/%d.jpg", srv.URL, i)}})
	}
	// The first user's photos wait for a slot, the shared one between two alerts,
	// and the second user's photo starts the shared download.
	first, shared, last := &image{key: srv.URL + "/first.jpg"}, &image{key: srv.URL + "/shared.jpg"},
		&image{key: srv.URL + "/last.jpg"}
	for _, img := range []*image{first, shared, last} {
		queueImages(w, users[:1], []*image{img})
	}
	queueImages(w, users[1:], []*image{shared})
	var waiting []string
	for _, p := range w.imageDownloader.queue.byUser[users[0]].items {
		waiting = append(waiting, p.key)
	}
	slices.Sort(waiting)
	if want := []string{first.key, last.key}; !slices.Equal(waiting, want) {
		t.Errorf("the first user's waiting photos are %v, want %v", waiting, want)
	}
}

// The next photo of an image that failed to download downloads it again.
func TestFailedDownloadIsRetried(t *testing.T) {
	t.Parallel()
	srv, client, _ := imageServer(t)
	w, users := photoSendWorker(t, &fakeTelegram{}, client, 101, 102)
	w.sender("test").cooling = true
	img := &image{key: srv.URL + "/missing"}
	queueImages(w, users[:1], []*image{img})
	w.completeDownload(<-w.imageDownloader.results)
	queueImages(w, users[1:], []*image{img})
	if w.imageDownloader.queue.running != 1 {
		t.Errorf("%d downloading for the next photo, want 1", w.imageDownloader.queue.running)
	}
}

// An image's download gives its bytes to the photos queued within an imageTTL after it.
func TestDownloadServesLaterPhotos(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		age             time.Duration
		wantDownloading int
	}{
		{"a photo queued within an imageTTL takes the bytes", imageTTL / 2, 0},
		{"a photo queued later downloads afresh", imageTTL, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, client, _ := imageServer(t)
			w, users := photoSendWorker(t, &fakeTelegram{}, client, 101, 102)
			s := w.sender("test")
			s.cooling = true
			img := &image{key: srv.URL + "/a.jpg"}
			queueImages(w, users[:1], []*image{img})
			w.completeDownload(<-w.imageDownloader.results)
			d := w.imageDownloader.images[img.key]
			d.downloadedAt = d.downloadedAt.Add(-tc.age)
			queueImages(w, users[1:], []*image{img})
			if w.imageDownloader.queue.running != tc.wantDownloading {
				t.Errorf("%d downloading, want %d", w.imageDownloader.queue.running, tc.wantDownloading)
			}
			var upload []byte
			if u := s.queue.byUser[users[1]]; u != nil {
				p := u.head().message.(*photoParams)
				upload = p.downloaded
				if !p.downloadedAt.Equal(d.downloadedAt) {
					t.Errorf("the later photo's bytes date from %v, want the download's %v", p.downloadedAt, d.downloadedAt)
				}
			}
			if got := bytes.Equal(upload, pngImage(t)); got != (tc.wantDownloading == 0) {
				t.Errorf("the later photo was queued with the downloaded bytes = %t, want %t", got, tc.wantDownloading == 0)
			}
		})
	}
}

// A request drops the downloads an imageTTL old, though it needs no download itself.
func TestOldDownloadsAreDropped(t *testing.T) {
	t.Parallel()
	srv, client, _ := imageServer(t)
	w, users := photoSendWorker(t, &fakeTelegram{}, client, 101)
	w.sender("test").cooling = true
	old, fresh := &image{key: srv.URL + "/old.jpg"}, &image{key: srv.URL + "/fresh.jpg"}
	for _, img := range []*image{old, fresh} {
		queueImages(w, users, []*image{img})
		w.completeDownload(<-w.imageDownloader.results)
	}
	w.imageDownloader.images[old.key].downloadedAt = w.imageDownloader.images[old.key].downloadedAt.Add(-imageTTL)
	w.imageDownloader.sweepAt = time.Time{}
	queueImages(w, users, []*image{nil})
	if w.imageDownloader.images[old.key] != nil || w.imageDownloader.images[fresh.key] == nil {
		t.Errorf("old kept = %t, fresh kept = %t, want only the fresh one",
			w.imageDownloader.images[old.key] != nil, w.imageDownloader.images[fresh.key] != nil)
	}
}

// A user's photo joins a download under way, whatever the user's cap.
func TestCappedPhotoJoinsDownload(t *testing.T) {
	t.Parallel()
	srv, _ := gatedImageServer(t)
	w, users := photoSendWorker(t, &fakeTelegram{}, srv.Client(), 101, 102)
	w.sender("test").cooling = true
	shared := &image{key: srv.URL + "/shared.jpg"}
	queueImages(w, users[1:], []*image{shared})
	for i := range maxUserDownloads {
		queueImages(w, users[:1], []*image{{key: fmt.Sprintf("%s/%d.jpg", srv.URL, i)}})
	}
	queueImages(w, users[:1], []*image{shared})
	if n := len(w.imageDownloader.images[shared.key].requests); n != 2 {
		t.Errorf("%d photos await the shared download, want 2", n)
	}
}

// A download waiting for room in the pool takes the order of its first photo,
// so a reply's image passes an alert's.
func TestPendingDownloadsStartInOrder(t *testing.T) {
	t.Parallel()
	tr := &cmdlib.Translation{Key: "online", Parse: cmdlib.ParseHTML}
	type queued struct {
		priority db.Priority
		name     string
	}
	tests := []struct {
		name   string
		queued []queued
		want   string
	}{
		{"a reply's image passes an alert's",
			[]queued{{db.PriorityLow, "alert"}, {db.PriorityHigh, "reply"}}, "reply"},
		{"a reply joining an alert's download raises it past earlier alerts",
			[]queued{{db.PriorityLow, "earlier"}, {db.PriorityLow, "alert"}, {db.PriorityHigh, "alert"}}, "alert"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := gatedImageServer(t)
			var chatIDs []int64
			for i := range maxDownloads + len(tc.queued) {
				chatIDs = append(chatIDs, int64(101+i))
			}
			w, users := photoSendWorker(t, &fakeTelegram{}, srv.Client(), chatIDs...)
			w.sender("test").cooling = true
			var busy []*image
			for i := range maxDownloads {
				busy = append(busy, &image{key: fmt.Sprintf("%s/%d.jpg", srv.URL, i)})
			}
			queueImages(w, users[:maxDownloads], busy)
			for i, q := range tc.queued {
				img := &image{key: srv.URL + "/" + q.name}
				u := users[maxDownloads+i]
				w.enqueueTr(q.priority, "test", u, true, tr, tplData{}, img, nil, unprompted(db.MessagePacket),
					testNotificationID)
			}
			if got := w.imageDownloader.queue.ready[0].items[0].key; got != srv.URL+"/"+tc.want {
				t.Errorf("next download = %s, want the %s", got, tc.want)
			}
		})
	}
}

// A download releases its photos in order, so a reply that joined an alert's download goes first.
func TestReleasedPhotosGoInOrder(t *testing.T) {
	t.Parallel()
	srv, free := gatedImageServer(t)
	f := &fakeTelegram{replies: []fakeReply{{body: photoSent}, {body: photoSent}}}
	w, users := photoSendWorker(t, f, srv.Client(), 101, 102)
	tr := &cmdlib.Translation{Key: "online", Parse: cmdlib.ParseHTML}
	img := &image{key: srv.URL + "/a.jpg"}
	for i, priority := range []db.Priority{db.PriorityLow, db.PriorityHigh} {
		w.enqueueTr(priority, "test", users[i], true, tr, tplData{}, img, nil, unprompted(db.MessagePacket),
			testNotificationID)
	}
	free()
	completeSends(w, 2)
	if want := []string{"102", "101"}; !slices.Equal(f.chats, want) {
		t.Errorf("sent to chats %v, want %v", f.chats, want)
	}
}

// Bots sending one image share its download, and each uploads it for an id of its own.
func TestBotsShareADownload(t *testing.T) {
	t.Parallel()
	srv, client, hits := imageServer(t)
	f := &fakeTelegram{replies: []fakeReply{{body: photoSent}}}
	other := &fakeTelegram{replies: []fakeReply{{body: photoSent}}}
	w, users := photoSendWorker(t, f, client, 101, 102)
	w.bots["other"] = newFakeBot(t, other)
	w.tpl["other"], w.tr["other"] = w.tpl["test"], w.tr["test"]
	tr := &cmdlib.Translation{Key: "online", Parse: cmdlib.ParseHTML}
	img := &image{key: srv.URL + "/a.jpg"}
	for i, endpoint := range []string{"test", "other"} {
		w.enqueueTr(db.PriorityLow, endpoint, users[i], true, tr, tplData{}, img, nil, unprompted(db.MessagePacket),
			testNotificationID)
	}
	completeSends(w, 2)
	if got := hits.Load(); got != 1 {
		t.Errorf("downloads = %d, want 1", got)
	}
	if !slices.Equal(f.sent, []string{"upload"}) || !slices.Equal(other.sent, []string{"upload"}) {
		t.Errorf("sent %v and %v, want an upload each", f.sent, other.sent)
	}
}

// The pool downloads maxDownloads images at once, and the rest wait their turn.
func TestDownloadPoolCaps(t *testing.T) {
	t.Parallel()
	srv, free := gatedImageServer(t)
	f := &fakeTelegram{}
	var chatIDs []int64
	var imgs []*image
	for i := range maxDownloads + 1 {
		f.replies = append(f.replies, fakeReply{body: photoSent})
		chatIDs = append(chatIDs, int64(101+i))
		imgs = append(imgs, &image{key: fmt.Sprintf("%s/%d.jpg", srv.URL, i)})
	}
	w, users := photoSendWorker(t, f, srv.Client(), chatIDs...)
	queueImages(w, users, imgs)
	d := w.imageDownloader.images[imgs[maxDownloads].key]
	if w.imageDownloader.queue.running != maxDownloads || d == nil || d.started {
		t.Errorf("%d downloading, want %d and the last image waiting", w.imageDownloader.queue.running, maxDownloads)
	}
	free()
	completeSends(w, len(users))
	if want := slices.Repeat([]string{"upload"}, len(users)); !slices.Equal(f.sent, want) {
		t.Errorf("sent %v, want %v", f.sent, want)
	}
}

// A held notification's latency counts from its entry to the send queue, not from its request.
func TestHeldNotificationLatencyStartsInTheQueue(t *testing.T) {
	t.Parallel()
	srv, client, _ := imageServer(t)
	f := &fakeTelegram{replies: []fakeReply{{body: photoSent}}}
	w, users := photoSendWorker(t, f, client, 101)
	queueImages(w, users, []*image{{key: srv.URL + "/a.jpg"}})
	// As if it had waited an hour for its image.
	for _, q := range w.imageDownloader.heldRequests() {
		q.requestedAt = q.requestedAt.Add(-time.Hour)
	}
	completeSends(w, 1)
	if latency := w.db.MustInt(`select latency from sent_message_log`); latency >= int(time.Minute.Milliseconds()) {
		t.Errorf("latency = %d ms, want it to leave out the wait for the image", latency)
	}
}
