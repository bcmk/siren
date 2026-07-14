// Outgoing message scheduling. The main goroutine owns all scheduling state;
// each send runs on a deliver goroutine that does I/O only.

package main

import (
	"container/heap"
	"time"

	"github.com/bcmk/siren/v3/internal/db"
)

const (
	commonCooldown = 60 * time.Millisecond // minimum gap between any two sends
	userCooldown   = time.Second           // minimum gap between sends to one user
	groupCooldown  = 3 * time.Second       // minimum gap to one group (Telegram's 20 msg/min cap)
	// tooManyRequestsBackoff paces a 429 redelivery
	// when Telegram sends no retry_after;
	// otherwise retry_after wins, capped at tooManyRequestsMaxBackoff.
	tooManyRequestsBackoff    = 8 * time.Second
	tooManyRequestsMaxBackoff = 20 * time.Minute
	// transientErrorPostpone delays a timed-out or network-failed message
	// before its redelivery.
	transientErrorPostpone = 10 * time.Second
	// tooManyRequestsGlobalPause is the global gap held after any 429,
	// so a rate limit backs off the whole bot, not just the failing user.
	tooManyRequestsGlobalPause = time.Second
	// maxQueueLen caps the outgoing queue; a broadcast past it drops its tail.
	// A streamer's image is shared across subscribers,
	// so even a full queue is cheap in memory.
	maxQueueLen = 100000
	// sendChanCap sizes sendResults and cooledUsers.
	// sendResults stays near-empty under single-flight delivery.
	// cooledUsers can briefly exceed 64: per-user postpones leave many
	// release timers pending, and a bot-wide 429 anchored to one deadline
	// fires a batch together. The overflow only parks a few blocked timer
	// goroutines on the send until trySend drains cooledUsers — no deadlock,
	// self-healing — so 64 sizes the common near-empty case, not the burst.
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

// messageLess orders messages for dispatch: priority, then FIFO by seq.
func messageLess(a, b *queuedMessage) bool {
	if a.priority != b.priority {
		return a.priority < b.priority
	}
	return a.seq < b.seq
}

// msgHeap is one user's pending messages, ordered by messageLess.
type msgHeap []*queuedMessage

func (h msgHeap) Len() int           { return len(h) }
func (h msgHeap) Less(i, j int) bool { return messageLess(h[i], h[j]) }
func (h msgHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *msgHeap) Push(x any) { *h = append(*h, x.(*queuedMessage)) }

func (h *msgHeap) Pop() any {
	n := len(*h)
	it := (*h)[n-1]
	(*h)[n-1] = nil
	*h = (*h)[:n-1]
	return it
}

// userQueue is one user's message heap plus its ready-heap position.
type userQueue struct {
	items msgHeap
	// heapIndex is the index in sendQueue.ready, or -1 while cooling or empty.
	heapIndex int
}

func (u *userQueue) head() *queuedMessage { return u.items[0] }

// readyHeap ranks dispatchable users by their head message,
// so the top user holds the globally best sendable message.
type readyHeap []*userQueue

func (h readyHeap) Len() int           { return len(h) }
func (h readyHeap) Less(i, j int) bool { return messageLess(h[i].head(), h[j].head()) }

func (h readyHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *readyHeap) Push(x any) {
	u := x.(*userQueue)
	u.heapIndex = len(*h)
	*h = append(*h, u)
}

func (h *readyHeap) Pop() any {
	n := len(*h)
	u := (*h)[n-1]
	(*h)[n-1] = nil
	*h = (*h)[:n-1]
	u.heapIndex = -1
	return u
}

// sendQueue is a heap of per-user heaps.
// Each user's messages are ordered by (priority, seq);
// the ready heap ranks non-cooling users with pending messages
// by their head message.
// Every operation — push, pop, cooling flip — costs O(log n),
// so no cooling transition ever rebuilds a whole heap.
type sendQueue struct {
	ready   readyHeap
	byUser  map[db.UserID]*userQueue
	cooling map[db.UserID]struct{}
	size    int
}

func newSendQueue() sendQueue {
	return sendQueue{
		byUser:  map[db.UserID]*userQueue{},
		cooling: map[db.UserID]struct{}{},
	}
}

// Len returns the total number of queued messages.
func (s *sendQueue) Len() int { return s.size }

// hasReady reports whether any message is dispatchable now.
func (s *sendQueue) hasReady() bool { return len(s.ready) > 0 }

// push adds a message to its user's queue
// and surfaces the user in the ready heap unless it is cooling.
func (s *sendQueue) push(q *queuedMessage) {
	u := s.byUser[q.userID]
	if u == nil {
		u = &userQueue{heapIndex: -1}
		s.byUser[q.userID] = u
	}
	heap.Push(&u.items, q)
	s.size++
	if _, cooling := s.cooling[q.userID]; cooling {
		return
	}
	if u.heapIndex == -1 {
		heap.Push(&s.ready, u)
	} else if u.head() == q {
		// The new message displaced the head; re-rank the user.
		heap.Fix(&s.ready, u.heapIndex)
	}
}

// pop removes and returns the best dispatchable message,
// or nil when every queued user is cooling or the queue is empty.
func (s *sendQueue) pop() *queuedMessage {
	if len(s.ready) == 0 {
		return nil
	}
	u := s.ready[0]
	q := heap.Pop(&u.items).(*queuedMessage)
	s.size--
	if len(u.items) == 0 {
		heap.Pop(&s.ready)
		// Every message in u shared this userID, so the popped one identifies it.
		delete(s.byUser, q.userID)
	} else {
		heap.Fix(&s.ready, 0)
	}
	return q
}

// startCooling parks a user: it leaves the ready heap
// and its messages wait until stopCooling.
func (s *sendQueue) startCooling(userID db.UserID) {
	s.cooling[userID] = struct{}{}
	if u := s.byUser[userID]; u != nil && u.heapIndex != -1 {
		heap.Remove(&s.ready, u.heapIndex)
	}
}

// stopCooling frees a user, surfacing its queue in the ready heap again.
// The heapIndex guard only protects heap integrity —
// a second insert of the same queue would corrupt it.
// It does not make a stale release safe:
// one racing a re-park would un-park a fresh penalty early.
// Safe today because at most one release is ever pending per user
// (cooling-gated dispatch arms no second timer while one is outstanding).
func (s *sendQueue) stopCooling(userID db.UserID) {
	delete(s.cooling, userID)
	if u := s.byUser[userID]; u != nil && u.heapIndex == -1 {
		heap.Push(&s.ready, u)
	}
}

// enqueueMessage adds a new message and tries to start a send.
// Main goroutine only.
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
	w.sendSeq++
	w.enqueue(&queuedMessage{
		userID:         userID,
		endpoint:       endpoint,
		message:        msg,
		priority:       priority,
		kind:           kind,
		notificationID: notificationID,
		requestedAt:    time.Now(),
		seq:            w.sendSeq,
	})
}

// enqueue inserts a prebuilt message and tries to start a send.
// Main goroutine only.
// A re-queued message (a fallback, postpone, or migrate) arrives here
// as the original envelope: its seq keeps the queue position,
// so a later same-priority message cannot overtake it
// and deliver a stale status last
// (all status notifications share PriorityLow).
func (w *worker) enqueue(q *queuedMessage) {
	if w.sendQueue.Len() >= maxQueueLen {
		lerr("the outgoing message queue is full")
		// A notification (notificationID != 0) is put back for a later fetch.
		// A reply or broadcast (notificationID 0) is dropped here, logged above:
		// an accepted, bounded loss when the queue overflows.
		if q.notificationID != 0 {
			w.db.RequeueNotification(q.notificationID)
		}
		return
	}
	w.sendQueue.push(q)
	w.trySend()
}

// onUserCooled frees a user after the per-user cooldown. Main goroutine only.
func (w *worker) onUserCooled(userID db.UserID) {
	w.sendQueue.stopCooling(userID)
	w.trySend()
}

// onSendDone frees the global slot after a delivery's final attempt.
// Main goroutine only; the caller has already logged and acted on the result.
func (w *worker) onSendDone() {
	w.commonCooling = false
	w.trySend()
}

func (w *worker) trySend() {
	if w.commonCooling || !w.sendQueue.hasReady() {
		return
	}
	q := w.sendQueue.pop()
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
		w.sendQueue.startCooling(q.userID)
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

// tooManyRequestsDelay returns Telegram's requested 429 pause
// capped at tooManyRequestsMaxBackoff, or the fallback when it is absent.
// Capping in the seconds domain keeps an absurd retry_after
// from overflowing the nanosecond conversion.
func tooManyRequestsDelay(retryAfterSeconds int) time.Duration {
	if retryAfterSeconds <= 0 {
		return tooManyRequestsBackoff
	}
	capped := min(retryAfterSeconds, int(tooManyRequestsMaxBackoff/time.Second))
	return time.Duration(capped) * time.Second
}

// transientDelay maps a transient send failure to the pause
// before its next attempt: Telegram's retry_after for a 429,
// transientErrorPostpone for a timeout or network error.
// Any other result is not transient and gets no pause.
func transientDelay(result int, retryAfterSeconds int) (pause time.Duration, transient bool) {
	switch result {
	case messageTooManyRequests:
		return tooManyRequestsDelay(retryAfterSeconds), true
	case messageTimeout, messageUnknownNetworkError:
		return transientErrorPostpone, true
	}
	return 0, false
}

// deliver sends one message on its own goroutine — a single attempt, paced —
// then reports back. It touches no shared state.
// Every failure worth another try comes back as a resend
// for the main loop to re-queue.
func (w *worker) deliver(q *queuedMessage) {
	// Captured at entry: the main loop owns the envelope once the result is sent,
	// so the cooldown tail works only from these locals, never from q.
	// Not a live race today — the resend cannot dispatch
	// until the tail itself releases the cooling user —
	// but ownership by capture keeps the property structural.
	// Capturing just the id also keeps the release closure
	// from pinning the whole message, image payload included, for the whole pause.
	// isGroup survives the fallback's payload swap: toText copies the chat id.
	isGroup := q.message.chatID() < 0
	kind := q.kind
	userID := q.userID
	now := time.Now()
	result, migrateTo, retryAfter := w.sendMessageInternal(q.endpoint, q.message)
	latency := int(time.Since(q.requestedAt).Milliseconds())
	// A 429, timeout, or network blip postpones the message:
	// the main loop re-queues it,
	// and the cooldown tail below keeps its user cooling for the whole postpone,
	// so other sends keep flowing meanwhile.
	// The failing user parks; a 429 also holds the global slot 1s below.
	// A migrate re-queues the same way: dispatch re-resolves the chat id
	// from the user, which by then points at the migrated chat
	// (ChatIDForUser follows migrated_to);
	// migrations are one-way, so this cannot loop.
	// A maintenance send is never redelivered — any failure drops it.
	// It has no user cooldown to pace a re-queue,
	// so an immediate redispatch would hammer Telegram on a 429.
	// A migrate bounce does carry the fresh chat id,
	// so retargeting it would be possible;
	// it is dropped anyway: the notice is best-effort,
	// and a group migrating mid-notice is not worth a special path.
	pause, transient := transientDelay(result, retryAfter)
	// A migrate to the same chat id is degenerate (Telegram breaking its
	// one-way migration contract): re-queuing it would loop forever
	// on the same address, so require a genuinely new target.
	migrated := result == messageMigrate && migrateTo != 0 && migrateTo != q.message.chatID()
	var resend *queuedMessage
	if kind != db.MaintenancePacket {
		if transient || migrated {
			resend = q
		}
		// A photo to a no-photo-rights group falls back to text:
		// swap the payload and re-queue the same envelope,
		// so it waits out the user's cooldown.
		// The swap precedes the result send, which orders it for the main loop.
		if p, ok := q.message.(*photoParams); ok && result == messageNoPhotoRights {
			q.message = p.toText()
			resend = q
		}
	}
	// Pace the global rate before releasing the slot.
	// A 429 holds it a full second, so a rate limit backs off the whole bot,
	// not just the failing user.
	// The wait ends at once on shutdown, so the drain never sleeps out the 1s.
	globalPace := commonCooldown
	if result == messageTooManyRequests {
		globalPace = tooManyRequestsGlobalPause
	}
	select {
	case <-time.After(globalPace):
	case <-w.shutdownCh:
	}
	w.sendResults <- msgSendResult{
		priority:        q.priority,
		timestamp:       int(now.Unix()),
		result:          result,
		endpoint:        q.endpoint,
		chatID:          q.message.chatID(),
		userID:          userID,
		migrateToChatID: migrateTo,
		latency:         latency,
		kind:            kind,
		notificationID:  q.notificationID,
		resend:          resend,
	}
	if kind == db.MaintenancePacket {
		// A maintenance send cools no user, so there is nothing to release.
		return
	}
	// The cooldown tail runs detached,
	// so the deliverWG wait at shutdown covers the POST and its result,
	// not a pacing sleep.
	// The timer spawns the release goroutine only when it fires,
	// so a long postpone parks a timer entry, not a goroutine stack.
	// A non-private chat (chatID < 0) caps tighter than the 1s per-user gap:
	// a group is 20 messages/min.
	// Pace groups, supergroups, and channels at the slower gap
	// to avoid self-triggering a 429.
	cooldown := userCooldown
	if isGroup {
		cooldown = groupCooldown
	}
	// A postponed message's user stays cooling for the whole postpone,
	// so its re-queued send dispatches no sooner than that.
	// A small retry_after must not undercut the chat's pacing gap,
	// so the larger of the two wins.
	if transient {
		cooldown = max(cooldown, pause)
	}
	// globalPace was already slept before the result, so charge it against
	// the user's total cooldown, not commonCooldown.
	time.AfterFunc(cooldown-globalPace, func() {
		// Release the user trySend cooled; the id is stable across
		// a chat migration, so there is nothing else to release.
		// The select may pick shutdownCh even while the drain still reads
		// cooledUsers (both arms ready), dropping the release and leaving that
		// user cooling for its remaining drain-window sends.
		// Accepted: those sends stay queued and re-arm (notifications)
		// or are dropped (replies) at shutdown anyway.
		select {
		case w.cooledUsers <- userID:
		case <-w.shutdownCh:
		}
	})
}
