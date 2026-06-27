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
	userCooldown           = time.Second           // minimum gap between sends to one user
	groupCooldown          = 3 * time.Second       // minimum gap to one group (Telegram's 20 msg/min cap)
	tooManyRequestsBackoff = 8 * time.Second       // wait after a 429 before retrying
	networkErrorBackoff    = time.Second           // wait after a timeout or network error before retrying
	// maxHeapLen caps the outgoing queue; a broadcast past it drops its tail.
	// A streamer's image is shared across subscribers,
	// so even a full queue is cheap in memory.
	maxHeapLen = 100000
	// sendChanCap sizes sendResults and cooledUsers.
	// Single-flight delivery keeps both nearly empty.
	sendChanCap = 64
)

type queuedMessage struct {
	userID         db.UserID
	endpoint       string
	message        sendable
	priority       db.Priority
	kind           db.PacketKind
	notificationID int
	requestedAt    time.Time
	seq            uint64
}

// sendHeap keeps the best sendable message at the top. A cooling-down user
// sinks below every ready one, so the top is sendable
// whenever any ready message exists.
type sendHeap struct {
	items       []*queuedMessage
	userCooling map[db.UserID]struct{}
	// userCount tracks queued messages per user,
	// so a cooling flip for a user with nothing else queued
	// can skip the O(n) re-init.
	userCount map[db.UserID]int
}

func newSendHeap() sendHeap {
	return sendHeap{
		userCooling: map[db.UserID]struct{}{},
		userCount:   map[db.UserID]int{},
	}
}

func (h *sendHeap) Len() int { return len(h.items) }

func (h *sendHeap) Less(i, j int) bool {
	_, ci := h.userCooling[h.items[i].userID]
	_, cj := h.userCooling[h.items[j].userID]
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
	h.userCount[it.userID]++
	h.items = append(h.items, it)
}

func (h *sendHeap) Pop() any {
	n := len(h.items)
	it := h.items[n-1]
	h.items[n-1] = nil
	h.items = h.items[:n-1]
	h.userCount[it.userID]--
	if h.userCount[it.userID] == 0 {
		delete(h.userCount, it.userID)
	}
	return it
}

// enqueueMessage adds a message and tries to start a send. Main goroutine only.
// A MaintenancePacket kind marks a maintenance send:
// it keeps the chat id it was built with, skips the per-user cooldown,
// and never touches the database.
// notificationID is the notification_queue row to clear, or 0 for a reply.
func (w *worker) enqueueMessage(
	priority db.Priority,
	endpoint string,
	msg sendable,
	kind db.PacketKind,
	userID db.UserID,
	notificationID int,
) {
	w.enqueueMessageAt(time.Now(), priority, endpoint, msg, kind, userID, notificationID)
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
	userID db.UserID,
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
	w.sendSeq++
	q := &queuedMessage{
		userID:         userID,
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

// onUserCooled frees a user after the per-user cooldown. Main goroutine only.
func (w *worker) onUserCooled(userID db.UserID) {
	delete(w.sendQueue.userCooling, userID)
	// userID's queued items all flip back to ready at once;
	// re-init the whole queue, as trySend does on the cooling flip.
	// With nothing queued for the user, no key changed and no re-init is due.
	if w.sendQueue.userCount[userID] > 0 {
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
	if _, cooling := w.sendQueue.userCooling[w.sendQueue.items[0].userID]; cooling {
		return // every queued user is cooling down
	}
	q := heap.Pop(&w.sendQueue).(*queuedMessage)
	if q.kind != db.MaintenancePacket {
		chatID, ok := w.db.ChatIDForUser(q.userID)
		if !ok {
			// The user has no chat row (no delete path today, so defensive).
			// Resolved before the slot is claimed, so just drop and move on
			// rather than crash the goroutine.
			lerr("dropping send: no chat for user %d", q.userID)
			w.finalizeNotification(q.notificationID, q.userID, false)
			w.trySend()
			return
		}
		w.sendQueue.userCooling[q.userID] = struct{}{}
		// q.userID's remaining items all flip to cooling at once,
		// but heap.Fix only repairs a single changed key, so per-item fixing
		// can leave a cooling item at the root, starving a ready one.
		// With no remaining items for the user, no key changed at all.
		// The re-init is O(n) in the queue length.
		// The guard limits it to a user with several items queued;
		// a single-message user is already optimized away.
		// A lazy pop-time skip would avoid the O(n) but is more complex,
		// and is warranted only if storm-time main-loop latency is measured.
		if w.sendQueue.userCount[q.userID] > 0 {
			heap.Init(&w.sendQueue)
		}
		// Resolve the chat id from the user at dispatch, on every send.
		// Deliberate, not a missed optimization:
		// userID is the single source of truth,
		// so the queue holds no chat id that can go stale on a migration,
		// and nothing threads one through the send path.
		// The lookup is cheap; single-flight pacing bounds dispatch.
		q.message.setChatID(chatID)
	}
	// Claim the global slot only now the send is committed to dispatch,
	// so the no-chat drop above just returns, no set-then-reset.
	w.commonCooling = true
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
		// re-queued so it waits out the user's cooldown, not resent in place.
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
			userID:          q.userID,
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
	if q.kind == db.MaintenancePacket {
		// A maintenance send cools no user, so there is nothing to release.
		return
	}
	// The cooldown tail runs detached,
	// so the deliverWG wait at shutdown covers the POST and its result,
	// not a pacing sleep.
	// A non-private chat (chatID < 0) caps tighter than the 1s per-user gap:
	// a group is 20 messages/min.
	// Pace groups, supergroups, and channels at the slower gap
	// to avoid self-triggering a 429.
	cooldown := userCooldown
	if q.message.chatID() < 0 {
		cooldown = groupCooldown
	}
	// Capture just the id: closing over q would pin the whole message,
	// image payload included, for the sleep.
	id := q.userID
	go func() {
		time.Sleep(cooldown - commonCooldown)
		// Release the user trySend cooled; the id is stable across
		// a chat migration, so there is nothing else to release.
		// Give up if the shutdown drain stopped reading cooledUsers,
		// so this detached tail never blocks forever on the send.
		select {
		case w.cooledUsers <- id:
		case <-w.shutdownCh:
		}
	}()
}
