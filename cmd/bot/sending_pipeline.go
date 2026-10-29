// The pipeline a message goes through: notifications and ads pass the download step,
// which releases them to the send step in each user's order; other messages skip it.

package main

import (
	"sort"
	"time"

	"github.com/bcmk/siren/v6/internal/db"
)

// enqueueMessage adds a new message and tries to start a send.
// Main goroutine only.
// notificationID is the message's notification_queue row, 0 for a message without one.
func (w *worker) enqueueMessage(
	priority db.Priority,
	endpoint string,
	msg sendable,
	tag sendTag,
	userID db.UserID,
	notificationID int,
) {
	// Under checker_only the owner's startup notices are all that get this far, and neither is sent.
	if w.cfg.CheckerOnly {
		return
	}
	w.sendSeq++
	q := &queuedMessage{
		userID:         userID,
		endpoint:       endpoint,
		message:        msg,
		order:          queueOrder{priority: priority, seq: w.sendSeq},
		tag:            tag,
		notificationID: notificationID,
		requestedAt:    time.Now(),
	}
	// Notifications go through the download step to get their images.
	// Ads go through it too, so one sent after a notification cannot overtake it.
	if notificationID != 0 || tag.kind == db.AdPacket {
		w.hold(q)
		return
	}
	q.enqueuedAt = q.requestedAt
	w.enqueue(q)
}

// hold passes a notification or an ad through the download step.
// Main goroutine only.
func (w *worker) hold(q *queuedMessage) {
	now := time.Now()
	w.sendReady(w.imageDownloader.push(q.endpoint, q.userID, q.order, q, now))
}

// imageToDownload is the URL of the image a photo needs downloaded: its own, or its fallback image.
// It is empty when the bot has cached an id for either, or for any other message.
func (w *worker) imageToDownload(q *queuedMessage, now time.Time) string {
	p, ok := q.message.(*photoParams)
	if !ok || p.image.data != nil {
		return ""
	}
	if img, id, _ := p.imageToSend(&w.sender(q.endpoint).images, now, q.requestedAt); id == "" {
		return img.key
	}
	return ""
}

// completeDownload logs one image's finished download and sends the messages it releases.
// Main goroutine only.
func (w *worker) completeDownload(r downloadResult) {
	now := time.Now()
	ready, fallback := w.imageDownloader.complete(r, now)
	data := map[string]any{"failed": r.err != nil}
	if fallback {
		data["fallback_image"] = true
	}
	w.db.LogPerformance(int(now.Unix()), db.PerformanceLogImageDownload, r.durationMs, data)
	if r.err != nil {
		linf("cannot download image, %v", r.err)
	}
	w.sendReady(ready)
}

// sendReady puts the messages the download step releases into the send queue.
func (w *worker) sendReady(ready []*queuedMessage) {
	// Most urgent first: an idle endpoint sends the first message queued at once.
	sort.Slice(ready, func(i, j int) bool { return orderLess(ready[i].order, ready[j].order) })
	now := time.Now()
	for _, q := range ready {
		q.enqueuedAt = now
		w.enqueue(q)
	}
}

// queueOrder ranks a message in the download and send queues: by priority, then by arrival.
type queueOrder struct {
	priority db.Priority
	seq      uint64
}

// orderLess reports whether a goes before b: by priority, then FIFO by seq.
func orderLess(a, b queueOrder) bool {
	if a.priority != b.priority {
		return a.priority < b.priority
	}
	return a.seq < b.seq
}

// setDownloadResult gives the photo its image's bytes.
func (q *queuedMessage) setDownloadResult(data []byte, downloadedAt time.Time) {
	p := q.message.(*photoParams)
	p.downloaded, p.downloadedAt = data, downloadedAt
}

// takeFallbackImage gives the photo its fallback image
// and reports whether it got one: false without one, or once that failed too.
func (q *queuedMessage) takeFallbackImage() bool {
	p := q.message.(*photoParams)
	if p.fallbackImage != nil || p.image.fallbackURL == "" {
		return false
	}
	p.fallbackImage = &image{key: p.image.fallbackURL}
	return true
}
