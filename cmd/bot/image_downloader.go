// The download step downloads the images that requests need, each once and a few at a time,
// and holds each user's requests on each endpoint until none ahead of them awaits an image.

package main

import (
	"container/heap"
	"maps"
	"slices"
	"time"

	"github.com/bcmk/siren/v6/internal/db"
)

const (
	// maxDownloads caps the images downloading at once, so a burst of alerts opens few connections.
	maxDownloads = 8
	// maxUserDownloads caps one user's running downloads, so a long answer leaves the pool to others.
	maxUserDownloads = 3
	// A new request skips the download once the download step holds maxDownloadBacklog requests.
	// It still takes an image downloaded within an imageTTL.
	maxDownloadBacklog = 100000
)

// downloadResult is a finished download, for the main goroutine.
type downloadResult struct {
	key        string
	data       []byte
	err        error
	durationMs int
}

// imageRequest is what the downloader needs of a request: to set its image's download outcome.
type imageRequest interface {
	setDownloadResult(data []byte, downloadedAt time.Time, failed bool)
}

// pendingImageRequest is a request the downloader holds, with its order, image, endpoint and user.
type pendingImageRequest[R imageRequest] struct {
	request R
	order   queueOrder
	// key is the image to download, empty for none.
	key      string
	endpoint string
	userID   db.UserID
	// awaits marks a request whose image is yet to download, holding back the requests behind it.
	awaits bool
}

// endpointUser is a user on one endpoint, whose requests keep their order.
type endpointUser struct {
	endpoint string
	userID   db.UserID
}

// requestHeap is one user's requests in their order.
type requestHeap[R imageRequest] []*pendingImageRequest[R]

func (h requestHeap[R]) Len() int           { return len(h) }
func (h requestHeap[R]) Less(i, j int) bool { return orderLess(h[i].order, h[j].order) }
func (h requestHeap[R]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *requestHeap[R]) Push(x any) { *h = append(*h, x.(*pendingImageRequest[R])) }

func (h *requestHeap[R]) Pop() any {
	n := len(*h)
	p := (*h)[n-1]
	(*h)[n-1] = nil
	*h = (*h)[:n-1]
	return p
}

// imageDownload is the download of an image from a site, waiting to start, under way or done,
// and the requests awaiting it.
type imageDownload[R imageRequest] struct {
	requests []*pendingImageRequest[R]
	// started marks a download under way or done, running in one of userID's slots.
	started bool
	userID  db.UserID
	// data is the image once downloaded, for the requests made within an imageTTL.
	data         []byte
	downloadedAt time.Time
}

// downloadQueue holds the requests whose downloads wait to start:
// each user's requests come out in their order, and the ready heap orders users by their first.
// A user leaves the ready heap while it runs maxUserDownloads downloads.
type downloadQueue[R imageRequest] struct {
	ready  downloadReadyHeap[R]
	byUser map[db.UserID]*userDownloadQueue[R]
	// running counts the downloads under way, at most maxDownloads.
	running int
}

func newDownloadQueue[R imageRequest]() downloadQueue[R] {
	return downloadQueue[R]{byUser: map[db.UserID]*userDownloadQueue[R]{}}
}

// userDownloadQueue is one user's requests waiting to download, and its downloads under way.
type userDownloadQueue[R imageRequest] struct {
	userID  db.UserID
	items   requestHeap[R]
	running int
	// heapIndex is the index in downloadQueue.ready, -1 while out of it.
	heapIndex int
}

// downloadReadyHeap orders the users free to start a download by their first request.
type downloadReadyHeap[R imageRequest] []*userDownloadQueue[R]

func (h downloadReadyHeap[R]) Len() int { return len(h) }

func (h downloadReadyHeap[R]) Less(i, j int) bool {
	return orderLess(h[i].items[0].order, h[j].items[0].order)
}

func (h downloadReadyHeap[R]) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *downloadReadyHeap[R]) Push(x any) {
	u := x.(*userDownloadQueue[R])
	u.heapIndex = len(*h)
	*h = append(*h, u)
}

func (h *downloadReadyHeap[R]) Pop() any {
	n := len(*h)
	u := (*h)[n-1]
	(*h)[n-1] = nil
	*h = (*h)[:n-1]
	u.heapIndex = -1
	return u
}

// settle puts a user in the ready heap when it can start a download, and takes it out otherwise.
// A user with no requests waiting and no downloads running is dropped.
func (dq *downloadQueue[R]) settle(u *userDownloadQueue[R]) {
	ready := len(u.items) > 0 && u.running < maxUserDownloads
	switch {
	case ready && u.heapIndex == -1:
		heap.Push(&dq.ready, u)
	case ready:
		heap.Fix(&dq.ready, u.heapIndex)
	case u.heapIndex != -1:
		heap.Remove(&dq.ready, u.heapIndex)
	}
	if len(u.items) == 0 && u.running == 0 {
		delete(dq.byUser, u.userID)
	}
}

// push adds a request whose download waits to start.
func (dq *downloadQueue[R]) push(p *pendingImageRequest[R]) {
	u := dq.byUser[p.userID]
	if u == nil {
		u = &userDownloadQueue[R]{userID: p.userID, heapIndex: -1}
		dq.byUser[p.userID] = u
	}
	heap.Push(&u.items, p)
	dq.settle(u)
}

// remove takes out a request whose download no longer waits to start.
func (dq *downloadQueue[R]) remove(p *pendingImageRequest[R]) {
	u := dq.byUser[p.userID]
	heap.Remove(&u.items, slices.Index(u.items, p))
	dq.settle(u)
}

// pop takes out the first request of the users free to start a download,
// nil for none or a full pool, and counts the download.
func (dq *downloadQueue[R]) pop() *pendingImageRequest[R] {
	if len(dq.ready) == 0 || dq.running == maxDownloads {
		return nil
	}
	u := dq.ready[0]
	p := heap.Pop(&u.items).(*pendingImageRequest[R])
	u.running++
	dq.running++
	dq.settle(u)
	return p
}

// finish frees the slots of a download that ended, its user's and the pool's.
func (dq *downloadQueue[R]) finish(userID db.UserID) {
	u := dq.byUser[userID]
	u.running--
	dq.running--
	dq.settle(u)
}

// imageDownloader downloads images from sites for requests of type R, one download per image.
// Main goroutine only.
type imageDownloader[R imageRequest] struct {
	// held holds each user's requests on each endpoint until none ahead of them awaits an image.
	held map[endpointUser]*requestHeap[R]
	// size counts the requests it holds.
	size  int
	queue downloadQueue[R]
	// images holds, by URL, each image waiting to download, downloading,
	// or downloaded within an imageTTL.
	images map[string]*imageDownload[R]
	// sweepAt is when the images an imageTTL old are next dropped.
	sweepAt time.Time
	// results takes each finished download to the main goroutine.
	results chan downloadResult
	// downloadImage runs off the main goroutine.
	downloadImage func(url string) ([]byte, error)
}

func newImageDownloader[R imageRequest](downloadImage func(url string) ([]byte, error)) imageDownloader[R] {
	return imageDownloader[R]{
		held:          map[endpointUser]*requestHeap[R]{},
		queue:         newDownloadQueue[R](),
		images:        map[string]*imageDownload[R]{},
		results:       make(chan downloadResult, maxDownloads),
		downloadImage: downloadImage,
	}
}

// push takes a user's request on an endpoint, its order, and the image to download, empty for none,
// and returns, in order, the user's requests on that endpoint that it releases.
func (dl *imageDownloader[R]) push(
	endpoint string,
	userID db.UserID,
	order queueOrder,
	request R,
	key string,
	now time.Time,
) []R {
	dl.sweep(now)
	p := &pendingImageRequest[R]{request: request, order: order, key: key, endpoint: endpoint, userID: userID}
	if key != "" {
		dl.fetch(p, now)
	}
	u := endpointUser{endpoint, userID}
	h := dl.held[u]
	if h == nil {
		h = &requestHeap[R]{}
		dl.held[u] = h
	}
	heap.Push(h, p)
	dl.size++
	return dl.release(u, nil)
}

// fetch gives a request the image downloaded within an imageTTL,
// or has it await the image's download, under way or waiting to start.
// A full step leaves it without the image, since downloads this far behind would only delay it.
func (dl *imageDownloader[R]) fetch(p *pendingImageRequest[R], now time.Time) {
	d := dl.images[p.key]
	if d != nil && d.data != nil {
		if now.Before(d.downloadedAt.Add(imageTTL)) {
			p.request.setDownloadResult(d.data, d.downloadedAt, false)
			return
		}
		d = nil
	}
	if dl.size >= maxDownloadBacklog {
		lerr("the download step is full, sending without the image: endpoint = %s", p.endpoint)
		return
	}
	if d == nil {
		d = &imageDownload[R]{}
		dl.images[p.key] = d
	}
	p.awaits = true
	d.requests = append(d.requests, p)
	if !d.started {
		dl.queue.push(p)
		dl.startDownloads()
	}
}

// release appends to out a user's held requests in order, up to one still awaiting its image.
func (dl *imageDownloader[R]) release(u endpointUser, out []R) []R {
	h := dl.held[u]
	for len(*h) > 0 && !(*h)[0].awaits {
		out = append(out, heap.Pop(h).(*pendingImageRequest[R]).request)
		dl.size--
	}
	if len(*h) == 0 {
		delete(dl.held, u)
	}
	return out
}

// heldRequests returns the requests the downloader holds.
func (dl *imageDownloader[R]) heldRequests() []R {
	var requests []R
	for _, h := range dl.held {
		for _, p := range *h {
			requests = append(requests, p.request)
		}
	}
	return requests
}

// startDownloads starts downloads in queue order while the pool has room,
// taking the other requests of each image out of the queue.
func (dl *imageDownloader[R]) startDownloads() {
	for {
		p := dl.queue.pop()
		if p == nil {
			return
		}
		d := dl.images[p.key]
		d.started, d.userID = true, p.userID
		for _, o := range d.requests {
			if o != p {
				dl.queue.remove(o)
			}
		}
		go dl.download(p.key)
	}
}

// download fetches the image at a URL for the main goroutine.
func (dl *imageDownloader[R]) download(key string) {
	start := time.Now()
	data, err := dl.downloadImage(key)
	d := downloadResult{key: key, data: data, err: err, durationMs: int(time.Since(start).Milliseconds())}
	// Never blocks: the channel holds a result for every download the pool runs.
	dl.results <- d
}

// complete takes in a finished download, gives it to the requests awaiting it,
// and returns the requests it releases, in each user's order.
// It keeps the image for the requests made within an imageTTL, and starts the next downloads.
func (dl *imageDownloader[R]) complete(r downloadResult, now time.Time) []R {
	d := dl.images[r.key]
	dl.queue.finish(d.userID)
	if r.err == nil {
		d.data, d.downloadedAt = r.data, now
	} else {
		delete(dl.images, r.key)
	}
	var released []R
	for _, p := range d.requests {
		p.awaits = false
		p.request.setDownloadResult(r.data, now, r.err != nil)
		released = dl.release(endpointUser{p.endpoint, p.userID}, released)
	}
	d.requests = nil
	dl.startDownloads()
	return released
}

// sweep drops the images an imageTTL old, once an imageTTL.
func (dl *imageDownloader[R]) sweep(now time.Time) {
	if now.Before(dl.sweepAt) {
		return
	}
	dl.sweepAt = now.Add(imageTTL)
	maps.DeleteFunc(dl.images, func(_ string, old *imageDownload[R]) bool {
		return old.data != nil && !now.Before(old.downloadedAt.Add(imageTTL))
	})
}
