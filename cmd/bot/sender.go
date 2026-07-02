// Outgoing message scheduling. The main goroutine owns all scheduling state;
// each send runs on a deliver goroutine that does I/O only.

package main

import (
	"container/heap"
	"time"

	"github.com/bcmk/siren/v3/internal/db"
)

const (
	commonCooldown         = 60 * time.Millisecond // minimum gap between any two sends
	chatCooldown           = time.Second           // minimum gap between sends to one chat
	groupCooldown          = 3 * time.Second       // minimum gap to one group (Telegram's 20 msg/min cap)
	tooManyRequestsBackoff = 8 * time.Second       // wait after a 429 before retrying
	networkErrorBackoff    = time.Second           // wait after a timeout or network error before retrying
	// maxHeapLen caps the outgoing queue; a broadcast past it drops its tail.
	// A streamer's image is shared across subscribers,
	// so even a full queue is cheap in memory.
	maxHeapLen = 100000
	// sendChanCap sizes sendResults and cooledChats.
	// Single-flight delivery keeps both nearly empty.
	sendChanCap = 64
)

type queuedMessage struct {
	chatID         int64
	endpoint       string
	message        sendable
	priority       db.Priority
	kind           db.PacketKind
	notificationID int
	requestedAt    time.Time
	seq            uint64
}

// sendHeap keeps the best sendable message at the top. A cooling-down chat
// sinks below every ready one, so the top is sendable
// whenever any ready message exists.
type sendHeap struct {
	items       []*queuedMessage
	chatCooling map[int64]struct{}
	// chatCount tracks queued messages per chat,
	// so a cooling flip for a chat with nothing else queued
	// can skip the O(n) re-init.
	chatCount map[int64]int
}

func newSendHeap() sendHeap {
	return sendHeap{
		chatCooling: map[int64]struct{}{},
		chatCount:   map[int64]int{},
	}
}

func (h *sendHeap) Len() int { return len(h.items) }

func (h *sendHeap) Less(i, j int) bool {
	_, ci := h.chatCooling[h.items[i].chatID]
	_, cj := h.chatCooling[h.items[j].chatID]
	if ci != cj {
		return !ci
	}
	if h.items[i].priority != h.items[j].priority {
		return h.items[i].priority < h.items[j].priority
	}
	return h.items[i].seq < h.items[j].seq
}

func (h *sendHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

func (h *sendHeap) Push(x any) {
	it := x.(*queuedMessage)
	h.chatCount[it.chatID]++
	h.items = append(h.items, it)
}

func (h *sendHeap) Pop() any {
	n := len(h.items)
	it := h.items[n-1]
	h.items[n-1] = nil
	h.items = h.items[:n-1]
	h.chatCount[it.chatID]--
	if h.chatCount[it.chatID] == 0 {
		delete(h.chatCount, it.chatID)
	}
	return it
}

// enqueueMessage adds a message and tries to start a send. Main goroutine only.
// notificationID is the notification_queue row to clear, or 0 for a reply.
func (w *worker) enqueueMessage(
	priority db.Priority,
	endpoint string,
	msg sendable,
	kind db.PacketKind,
	notificationID int,
) {
	w.enqueueMessageAt(time.Now(), priority, endpoint, msg, kind, notificationID)
}

// enqueueMessageAt is enqueueMessage with an explicit request time,
// so a re-queued message (the photo-to-text fallback)
// keeps the latency baseline of its first attempt.
func (w *worker) enqueueMessageAt(
	requestedAt time.Time,
	priority db.Priority,
	endpoint string,
	msg sendable,
	kind db.PacketKind,
	notificationID int,
) {
	if w.sendQueue.Len() >= maxHeapLen {
		lerr("the outgoing message heap is full")
		// A notification (notificationID != 0) is put back for a later fetch.
		// A reply or broadcast (notificationID 0) is dropped here, logged above:
		// an accepted, bounded loss when the queue overflows.
		if notificationID != 0 {
			w.db.RequeueNotification(notificationID)
		}
		return
	}
	chatID := msg.chatID()
	w.sendSeq++
	q := &queuedMessage{
		chatID:         chatID,
		endpoint:       endpoint,
		message:        msg,
		priority:       priority,
		kind:           kind,
		notificationID: notificationID,
		requestedAt:    requestedAt,
		seq:            w.sendSeq,
	}
	heap.Push(&w.sendQueue, q)
	w.trySend()
}

// onChatCooled frees a chat after its per-chat cooldown. Main goroutine only.
func (w *worker) onChatCooled(chatID int64) {
	delete(w.sendQueue.chatCooling, chatID)
	// chatID's queued items all flip back to ready at once; re-init
	// the whole queue, as trySend does on the cooling flip.
	// With nothing queued for the chat, no key changed and no re-init is due.
	if w.sendQueue.chatCount[chatID] > 0 {
		heap.Init(&w.sendQueue)
	}
	w.trySend()
}

// onSendDone frees the global slot after a delivery's final attempt.
// Main goroutine only; the caller has already logged and acted on the result.
func (w *worker) onSendDone() {
	w.commonCooling = false
	w.trySend()
}

func (w *worker) trySend() {
	if w.commonCooling || w.sendQueue.Len() == 0 {
		return
	}
	if _, cooling := w.sendQueue.chatCooling[w.sendQueue.items[0].chatID]; cooling {
		return // every queued chat is cooling down
	}
	q := heap.Pop(&w.sendQueue).(*queuedMessage)
	w.commonCooling = true
	w.sendQueue.chatCooling[q.chatID] = struct{}{}
	// q.chatID's remaining items all flip to cooling at once,
	// but heap.Fix only repairs a single changed key, so per-item fixing
	// can leave a cooling item at the root, starving a ready one.
	// With no remaining items for the chat, no key changed at all.
	// The re-init is O(n) in the queue length.
	// The guard limits it to a chat with several items queued;
	// a single-message chat is already optimized away.
	// A lazy pop-time skip would avoid the O(n) but is more complex,
	// and is warranted only if storm-time main-loop latency is measured.
	if w.sendQueue.chatCount[q.chatID] > 0 {
		heap.Init(&w.sendQueue)
	}
	// Track the in-flight send so shutdown can wait for it.
	w.deliverWG.Go(func() {
		w.deliver(q)
	})
}

// isRetriableSendError reports whether a send result is a transient failure
// worth retrying: a timeout, a network blip, or a rate-limit.
func isRetriableSendError(result int) bool {
	return result == messageTimeout ||
		result == messageUnknownNetworkError ||
		result == messageTooManyRequests
}

// deliver sends one message on its own goroutine, retrying and pacing,
// then reports back. It touches no shared state.
func (w *worker) deliver(q *queuedMessage) {
	migrated := false
	for {
		now := time.Now()
		result, migrateTo := w.sendMessageInternal(q.endpoint, q.message)
		latency := int(time.Since(q.requestedAt).Milliseconds())
		retry := isRetriableSendError(result) ||
			(result == messageMigrate && migrateTo != 0 && !migrated)
		// A photo to a no-photo-rights group falls back to text,
		// re-queued so it waits out the chat's cooldown, not resent in place.
		var retryAsText sendable
		if p, ok := q.message.(*photoParams); ok && result == messageNoPhotoRights {
			retryAsText = p.toText()
		}
		if !retry {
			// Pace the global rate before releasing the slot.
			time.Sleep(commonCooldown)
		}
		w.sendResults <- msgSendResult{
			priority:        q.priority,
			timestamp:       int(now.Unix()),
			result:          result,
			endpoint:        q.endpoint,
			chatID:          q.message.chatID(),
			migrateToChatID: migrateTo,
			latency:         latency,
			kind:            q.kind,
			notificationID:  q.notificationID,
			requestedAt:     q.requestedAt,
			retry:           retry,
			retryAsText:     retryAsText,
		}
		if !retry {
			break
		}
		// No attempt cap: this retries in place until success or shutdown.
		// A retry keeps retry=true, so the slot stays held for the whole loop,
		// and one recipient that keeps failing blocks every other send.
		// Deliberate: a 429, timeout, or network error never says
		// whether its cause is this chat or the whole bot,
		// so we back off for everyone rather than risk hammering a global limit.
		// The stall on a chat that never recovers is the accepted cost.
		var backoff time.Duration
		switch result {
		case messageTooManyRequests:
			backoff = tooManyRequestsBackoff
		case messageMigrate:
			migrated = true
			q.message.setChatID(migrateTo)
			backoff = commonCooldown
		default:
			backoff = networkErrorBackoff
		}
		// Wake at once on shutdown instead of sleeping the whole backoff
		// (a 429 is 8s), which would eat the shared grace window.
		// The retry is abandoned; a notification row stays sending=1
		// and re-arms next start.
		select {
		case <-time.After(backoff):
		case <-w.shutdownCh:
			return
		}
	}
	// The cooldown tail runs detached,
	// so the deliverWG wait at shutdown covers the POST and its result,
	// not a pacing sleep.
	// A non-private chat (chatID < 0) caps tighter than the 1s per-chat gap:
	// a group is 20 messages/min.
	// Pace groups, supergroups, and channels at the slower gap
	// to avoid self-triggering a 429.
	cooldown := chatCooldown
	if q.message.chatID() < 0 {
		cooldown = groupCooldown
	}
	// Capture just the id: closing over q would pin the whole message,
	// image payload included, for the sleep.
	id := q.chatID
	go func() {
		time.Sleep(cooldown - commonCooldown)
		// Release the id trySend cooled.
		// A migrate resend target is deliberately never cooled:
		// its cool and release would pair across two channels, which can race.
		// The rare price is one un-paced send to the new id, a 429 at worst,
		// absorbed by the retry backoff.
		// Give up if the shutdown drain stopped reading cooledChats,
		// so this detached tail never blocks forever on the send.
		select {
		case w.cooledChats <- id:
		case <-w.shutdownCh:
		}
	}()
}
