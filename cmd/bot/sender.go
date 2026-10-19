// Outgoing message scheduling. The main goroutine owns all scheduling state;
// each send runs on a deliver goroutine that does I/O only.
// Each endpoint is its own bot token, rated separately by Telegram,
// so every limit here paces one endpoint: one send in flight per endpoint.

package main

import (
	"container/heap"
	"maps"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

const (
	endpointCooldown = 60 * time.Millisecond // minimum gap between an endpoint's sends
	userCooldown     = time.Second           // minimum gap between an endpoint's sends to one user
	groupCooldown    = 3 * time.Second       // minimum gap to one group (Telegram's 20 msg/min cap)
	// tooManyRequestsBackoff paces a 429 redelivery
	// when Telegram sends no retry_after;
	// otherwise retry_after wins, capped at tooManyRequestsMaxBackoff.
	tooManyRequestsBackoff    = 8 * time.Second
	tooManyRequestsMaxBackoff = 20 * time.Minute
	// transientErrorPostpone delays a timed-out or network-failed message
	// before its redelivery.
	transientErrorPostpone = 10 * time.Second
	// timedOutUploadPostpone delays the retry of a timed-out upload, which goes without the image.
	timedOutUploadPostpone = 2 * time.Second
	// tooManyRequestsEndpointPause is the gap an endpoint holds after any 429,
	// so a rate limit backs off that whole endpoint, not just the failing user.
	tooManyRequestsEndpointPause = time.Second
	// maxQueueLen caps an endpoint's outgoing queue; a broadcast past it drops its tail.
	// A message parks its render data, so a full queue costs over a hundred megabytes.
	maxQueueLen = 100000
	// sendChanCap sizes sendResults and cooledUsers.
	// sendResults stays near-empty under single-flight-per-endpoint delivery.
	// cooledUsers can briefly exceed 64:
	// per-user postpones leave many release timers pending,
	// and an endpoint-wide 429 anchored to one deadline fires a batch together.
	// The overflow only parks a few blocked timer goroutines on the send
	// until trySend drains cooledUsers — no deadlock, self-healing —
	// so 64 sizes the common near-empty case, not the burst.
	sendChanCap = 64
	// imageTTL is how long a downloaded image lasts, since sites reuse URLs,
	// and how long a timeout pauses uploads.
	imageTTL = time.Minute
	// imageIDTTL is how long a bot keeps the id of an image it uploaded.
	imageIDTTL = time.Hour
)

// imageCache is what one bot learned of the images it sends.
// It keeps an id for imageIDTTL and any other fact for imageTTL.
type imageCache struct {
	images map[string]imageState
	// sweepAt is when the expired entries are next dropped.
	sweepAt time.Time
}

// imageState is what a bot learned of one image.
type imageState struct {
	id string
	// downloadedAt is when an image from a site was downloaded;
	// its id serves messages queued up to an imageTTL later.
	downloadedAt time.Time
	idExpires    time.Time
	// gaveUpUntil is when uploads may resume after one timed out.
	gaveUpUntil time.Time
}

// photoOutcome is what a photo's send found out about its image.
type photoOutcome struct {
	// key names the image, empty for any other message.
	key string
	// rejected marks an id Telegram refused, uploadedID is the id the upload got.
	rejected   bool
	uploadedID string
	// timedOut marks an upload that ran out of time.
	timedOut bool
	// downloadedAt is when an image from a site was downloaded, the time its id ages from.
	downloadedAt time.Time
	// fileUpload marks a send that carried the image's bytes, whatever its result.
	fileUpload bool
}

// lookup returns the id to send img by in a message queued at queuedAt, empty to upload it,
// and whether it may upload.
func (c *imageCache) lookup(img *image, now, queuedAt time.Time) (id string, uploads bool) {
	e := c.images[img.key]
	if now.Before(e.idExpires) && (img.data != nil || queuedAt.Before(e.downloadedAt.Add(imageTTL))) {
		id = e.id
	}
	return id, !now.Before(e.gaveUpUntil)
}

// learn takes in what a photo's send found out about its image.
func (c *imageCache) learn(o photoOutcome, now time.Time) {
	c.sweep(now)
	e := c.images[o.key]
	switch {
	case o.uploadedID != "":
		e.id, e.downloadedAt, e.idExpires = o.uploadedID, o.downloadedAt, now.Add(imageIDTTL)
	case o.timedOut:
		e.gaveUpUntil = now.Add(imageTTL)
	case o.rejected:
		e.id = ""
	default:
		return
	}
	c.images[o.key] = e
}

// sweep makes the map on first use and drops the expired entries once an imageTTL.
func (c *imageCache) sweep(now time.Time) {
	if c.images == nil {
		c.images = map[string]imageState{}
	}
	if now.Before(c.sweepAt) {
		return
	}
	c.sweepAt = now.Add(imageTTL)
	maps.DeleteFunc(c.images, func(_ string, e imageState) bool {
		return !now.Before(e.idExpires) && !now.Before(e.gaveUpUntil)
	})
}

// sendTag identifies an outgoing message:
// its kind, the command it answers, empty when neither log names one,
// and the reply's place in the answer.
type sendTag struct {
	kind     db.PacketKind
	command  string
	replySeq int
}

// reply tags the first message answering an inbound event.
// The command names that event when the received log names it,
// and is empty when neither log does, as for an owner command.
func reply(command string) sendTag {
	return replyNth(command, 0)
}

// replyNth tags the seq-th message of an answer that runs to several.
func replyNth(command string, seq int) sendTag {
	return sendTag{kind: db.ReplyPacket, command: command, replySeq: seq}
}

// receivedMessage is the inbound message a handler answers.
// Its command is the name both logs record, empty when neither does,
// so a reply can never name a command the received log lacks.
// Record it with logReceived rather than calling the database directly:
// every field is a plain string or int, so a hand-written call
// can transpose two of them and still compile.
type receivedMessage struct {
	timestamp int
	endpoint  string
	userID    db.UserID
	chatID    int64
	command   string
	// replySeq is this reply's place in the answer, zero for the first.
	replySeq int
}

// newReceivedMessage pairs the chat with its user, so the two cannot disagree,
// and reports whether the chat is new. chatType is empty where the update does not name one.
func (w *worker) newReceivedMessage(
	now int,
	endpoint string,
	chatID int64,
	chatType string,
	command string,
) (receivedMessage, bool) {
	userID, created := w.db.AddUser(chatID, w.cfg.MaxSubs, now, chatType)
	return receivedMessage{
		timestamp: now,
		endpoint:  endpoint,
		userID:    userID,
		chatID:    chatID,
		command:   command,
	}, created
}

// next returns the message tagged as the reply after this one.
func (m receivedMessage) next() receivedMessage {
	m.replySeq++
	return m
}

// logReceived records the inbound message that replies are tagged from,
// keeping the row and the tag in step.
func (w *worker) logReceived(m receivedMessage) {
	w.db.LogReceivedMessage(m.timestamp, m.endpoint, m.userID, m.command)
}

// tag marks a send as answering this message.
func (m receivedMessage) tag() sendTag {
	return replyNth(m.command, m.replySeq)
}

// replyTr answers with a translation, always to the sender.
// A send to anyone else is not a reply and must say so at its own call site.
func (w *worker) replyTr(
	m receivedMessage,
	priority db.Priority,
	notify bool,
	translation *cmdlib.Translation,
	data tplData,
) {
	w.replyTrMarkup(m, priority, notify, translation, data, nil)
}

// replyTrMarkup is replyTr with a keyboard riding along.
func (w *worker) replyTrMarkup(
	m receivedMessage,
	priority db.Priority,
	notify bool,
	translation *cmdlib.Translation,
	data tplData,
	markup models.ReplyMarkup,
) {
	w.enqueueTr(priority, m.endpoint, m.userID, notify, translation, withButton(data, markup),
		nil, markup, m.tag(), 0)
}

// withButton tells the translation whether a keyboard rode along,
// so a line reading into the button is left out of a chat that gets none.
func withButton(data tplData, markup models.ReplyMarkup) tplData {
	if data == nil {
		data = tplData{}
	}
	data["has_button"] = markup != nil
	return data
}

// replyToOwner answers an owner command with raw text.
// Owner commands are absent from knownCommands,
// so the received log names none and neither can this.
func (w *worker) replyToOwner(endpoint, text string) {
	w.replyToOwnerNth(endpoint, 0, cmdlib.ParseRaw, text)
}

// replyToOwnerNth answers as the seq-th message of an owner answer.
func (w *worker) replyToOwnerNth(endpoint string, seq int, parse cmdlib.ParseKind, text string) {
	w.sendText(db.PriorityHigh, endpoint, w.ownerUserID, false, true, parse, text, replyNth("", seq))
}

// adTag tags an advertisement,
// naming the command that triggered it when one did.
func adTag(command string) sendTag {
	return sendTag{kind: db.AdPacket, command: command}
}

// unprompted tags a message no inbound event asked for:
// a status notification, a broadcast, a maintenance notice.
// A reply is never one of those, named or not, and uses reply instead.
func unprompted(kind db.PacketKind) sendTag {
	return sendTag{kind: kind}
}

// notificationTag rebuilds the tag a queued notification was stored with,
// so a deferred reply still names the command it answers.
func notificationTag(n db.Notification) sendTag {
	return sendTag{kind: n.Kind, command: n.Command, replySeq: n.ReplySeq}
}

type queuedMessage struct {
	userID         db.UserID
	endpoint       string
	message        sendable
	order          queueOrder
	tag            sendTag
	notificationID int
	requestedAt    time.Time
	stalls         int
	// enqueuedAt is when the message entered the send queue, which its latency counts from.
	enqueuedAt time.Time
}

// msgHeap is one user's pending messages, ordered by orderLess.
// Stalls are absent on purpose: a newer status must not pass a stalled one.
type msgHeap []*queuedMessage

func (h msgHeap) Len() int           { return len(h) }
func (h msgHeap) Less(i, j int) bool { return orderLess(h[i].order, h[j].order) }
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

// userLess ranks a stalling chat behind healthy ones of the same priority.
func userLess(a, b *userQueue) bool {
	x, y := a.head(), b.head()
	if x.order.priority != y.order.priority {
		return x.order.priority < y.order.priority
	}
	if x.stalls != y.stalls {
		return x.stalls < y.stalls
	}
	return x.order.seq < y.order.seq
}

// readyHeap ranks dispatchable users by userLess,
// so the top user holds the endpoint's best sendable message.
type readyHeap []*userQueue

func (h readyHeap) Len() int           { return len(h) }
func (h readyHeap) Less(i, j int) bool { return userLess(h[i], h[j]) }

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

// endpointSender is one endpoint's scheduling state:
// the queue of its pending messages and the single-flight slot pacing that bot's sends.
// Main goroutine only, like the queue it holds.
type endpointSender struct {
	queue sendQueue
	// cooling claims the endpoint's send slot
	// from dispatch until endpointCooldown after the delivery.
	cooling bool
	images  imageCache
}

// cooledUser names the (endpoint, user) pair a cooldown release frees.
type cooledUser struct {
	endpoint string
	userID   db.UserID
}

// sender returns the endpoint's scheduling state, creating it on first use.
// Main goroutine only.
func (w *worker) sender(endpoint string) *endpointSender {
	s := w.senders[endpoint]
	if s == nil {
		s = &endpointSender{queue: newSendQueue()}
		w.senders[endpoint] = s
	}
	return s
}

// sendQueue is a heap of per-user heaps.
// Each user's messages are ordered by (priority, seq);
// the ready heap ranks non-cooling users with pending messages
// by their head message, sinking one whose head has stalled.
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

// enqueue inserts a prebuilt message and tries to start a send.
// Main goroutine only.
// A re-queued message (a fallback, postpone, or migrate) arrives here
// as the original envelope: its seq keeps the queue position,
// so a later same-priority message cannot overtake it
// and deliver a stale status last
// (all status notifications share PriorityLow).
func (w *worker) enqueue(q *queuedMessage) {
	s := w.sender(q.endpoint)
	if s.queue.Len() >= maxQueueLen {
		lerr("the outgoing message queue is full: endpoint = %s", q.endpoint)
		// A notification (notificationID != 0) is put back for a later fetch.
		// A reply or broadcast (notificationID 0) is dropped here, logged above:
		// an accepted, bounded loss when the queue overflows.
		if q.notificationID != 0 {
			w.db.RequeueNotification(q.notificationID)
		}
		return
	}
	s.queue.push(q)
	w.trySend(q.endpoint)
}

// onUserCooled frees an endpoint's user after the per-user cooldown. Main goroutine only.
func (w *worker) onUserCooled(c cooledUser) {
	w.sender(c.endpoint).queue.stopCooling(c.userID)
	w.trySend(c.endpoint)
}

// onSendDone frees the endpoint's slot after a delivery's final attempt.
// Main goroutine only; the caller has already logged and acted on the result.
func (w *worker) onSendDone(endpoint string) {
	w.sender(endpoint).cooling = false
	w.trySend(endpoint)
}

// pickImage sets the image the photo sends, by id or upload, and reports whether it can send one.
// A failed download shows the placeholder, if shipped, unless an id serves the image.
func (p *photoParams) pickImage(images *imageCache, placeholder *image, now, queuedAt time.Time) bool {
	id, uploads := images.lookup(p.image, now, queuedAt)
	p.sentImage = p.image
	if id == "" && p.downloadFailed && placeholder != nil {
		p.sentImage = placeholder
		id, uploads = images.lookup(placeholder, now, queuedAt)
	}
	p.fileID = id
	if p.sentImage.data != nil {
		p.upload = p.sentImage.data
	}
	return id != "" || (uploads && p.upload != nil)
}

// logDroppedAd logs an ad dropped before its send as a send result.
func (w *worker) logDroppedAd(q *queuedMessage, now time.Time) {
	r := msgSendResult{
		priority:  q.order.priority,
		timestamp: int(now.Unix()),
		result:    messageAdDropped,
		endpoint:  q.endpoint,
		userID:    q.userID,
		latency:   int(now.Sub(q.enqueuedAt).Milliseconds()),
		tag:       q.tag,
	}
	w.resolveResultUser(&r)
	w.handleSendResult(r)
}

func (w *worker) trySend(endpoint string) {
	s := w.sender(endpoint)
	if s.cooling || !s.queue.hasReady() {
		return
	}
	q := s.queue.pop()
	// msg is what this dispatch sends: the queued message, or the text its photo goes as.
	// It is chosen afresh on every dispatch, so a resend sees what the bot learned meanwhile.
	msg := q.message
	if q.tag.kind != db.MaintenancePacket {
		if p, ok := q.message.(*photoParams); ok {
			now := time.Now()
			placeholder := shippedImage(w.tr[q.endpoint].ImageUnavailable)
			// A photo with no image to send goes as text; an ad is dropped instead.
			if !p.pickImage(&s.images, placeholder, now, q.requestedAt) {
				if q.tag.kind == db.AdPacket {
					w.logDroppedAd(q, now)
					w.trySend(endpoint)
					return
				}
				msg = p.toUnavailableText()
			}
		}
		chatID, ok := w.db.ChatIDForUser(q.userID)
		if !ok {
			// The user has no chat row (no delete path today, so defensive).
			// A missing row is recoverable state, not a broken invariant,
			// and it lands before the slot is claimed, so drop this send and carry on.
			lerr("dropping send: no chat for user %d", q.userID)
			w.finalizeNotification(q.notificationID)
			w.trySend(endpoint)
			return
		}
		s.queue.startCooling(q.userID)
		// Resolve the chat id from the user at dispatch, on every send.
		// Deliberate, not a missed optimization:
		// userID is the single source of truth,
		// so the queue holds no chat id that can go stale on a migration,
		// and nothing threads one through the send path.
		// The lookup is cheap; single-flight pacing bounds dispatch.
		msg.setChatID(chatID)
	}
	// Outside the branch above: a message tagged maintenance renders here too.
	msg.render(w.botMentionIfNeeded(q.endpoint, msg.chatID()))
	// Claim the endpoint's slot only now the send is committed to dispatch,
	// so the no-chat drop above just returns, no set-then-reset.
	s.cooling = true
	// Track the in-flight send so shutdown can wait for it.
	w.deliverWG.Go(func() {
		w.deliver(q, msg)
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
// stalled marks a transport failure, as against a 429 Telegram answered.
func transientDelay(result int, retryAfterSeconds int) (pause time.Duration, transient, stalled bool) {
	switch result {
	case messageTooManyRequests:
		return tooManyRequestsDelay(retryAfterSeconds), true, false
	case messageTimeout, messageUnknownNetworkError:
		return transientErrorPostpone, true, true
	}
	return 0, false, false
}

// attempt sends the message once.
// A photo goes by its id, or uploads the bytes dispatch gave it.
func (w *worker) attempt(
	endpoint string,
	msg sendable,
) (result int, migrateTo int64, retryAfter int, photo photoOutcome) {
	sent, result, migrateTo, retryAfter := w.sendMessageInternal(endpoint, msg)
	p, ok := msg.(*photoParams)
	if !ok {
		return result, migrateTo, retryAfter, photo
	}
	photo = photoOutcome{
		key:          p.sentImage.key,
		rejected:     p.fileID != "" && result == messageFileIDRejected,
		timedOut:     p.fileID == "" && result == messageTimeout,
		downloadedAt: p.downloadedAt,
		fileUpload:   p.fileID == "" && result != messageSkipped,
	}
	if p.fileID == "" && sent != nil && len(sent.Photo) > 0 {
		// Sizes come smallest first; the largest is the image as uploaded.
		photo.uploadedID = sent.Photo[len(sent.Photo)-1].FileID
	}
	return result, migrateTo, retryAfter, photo
}

// deliver sends msg, what this dispatch of q sends, on its own goroutine —
// a single attempt, paced — then reports back. It touches no shared state.
// Every failure worth another try comes back as a resend
// for the main loop to re-queue.
func (w *worker) deliver(q *queuedMessage, msg sendable) {
	// Captured at entry: the main loop owns the envelope once the result is sent,
	// so the cooldown tail works only from these locals, never from q.
	// Not a live race today — the resend cannot dispatch
	// until the tail itself releases the cooling user —
	// but ownership by capture keeps the property structural.
	// Capturing just the endpoint and the id also keeps the release closure
	// from pinning the whole message for the whole pause.
	// isGroup survives the fallback's payload swap: toText copies the chat id.
	isGroup := isGroupOrChannel(msg.chatID())
	tag := q.tag
	userID := q.userID
	endpoint := q.endpoint
	now := time.Now()
	result, migrateTo, retryAfter, photo := w.attempt(q.endpoint, msg)
	latency := int(time.Since(q.enqueuedAt).Milliseconds())
	// A 429, timeout, or network blip postpones the message:
	// the main loop re-queues it,
	// and the cooldown tail below keeps its user cooling for the whole postpone,
	// so other sends keep flowing meanwhile.
	// The failing user parks; a 429 also holds the endpoint's slot 1s below.
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
	pause, transient, stalled := transientDelay(result, retryAfter)
	if photo.timedOut {
		pause = timedOutUploadPostpone
	}
	// A migrate to the same chat id is degenerate (Telegram breaking its
	// one-way migration contract): re-queuing it would loop forever
	// on the same address, so require a genuinely new target.
	migrated := result == messageMigrate && migrateTo != msg.chatID()
	var resend *queuedMessage
	p, isPhoto := msg.(*photoParams)
	switch {
	case tag.kind == db.MaintenancePacket:
		// A maintenance send is never redelivered.
	case isPhoto && (result == messageNoPhotoRights || photo.timedOut):
		// The photo goes again as text: a group may take no photos,
		// and a timed-out upload may have reached the chat after all.
		// Ahead of the transient case, so a timed-out upload counts no stall.
		// The swap precedes the result send, so the main loop sees it.
		// An ad goes with its image or not at all.
		if tag.kind != db.AdPacket {
			q.message = p.toText()
			resend = q
		}
	case transient || migrated:
		if stalled {
			q.stalls++
		}
		resend = q
	case photo.rejected:
		// The resend goes by an id gained meanwhile, uploads the bytes it holds,
		// or else goes as text; an ad is dropped instead.
		resend = q
	}
	// Pace the endpoint's rate before releasing its slot.
	// A 429 holds it a full second, so a rate limit backs off the whole endpoint,
	// not just the failing user, while other endpoints keep sending.
	// The wait ends at once on shutdown, so the drain never sleeps out the 1s.
	endpointPace := endpointCooldown
	if result == messageTooManyRequests {
		endpointPace = tooManyRequestsEndpointPause
	}
	select {
	case <-time.After(endpointPace):
	case <-w.shutdownCh:
	}
	w.sendResults <- msgSendResult{
		priority:        q.order.priority,
		timestamp:       int(now.Unix()),
		result:          result,
		endpoint:        q.endpoint,
		chatID:          msg.chatID(),
		userID:          userID,
		migrateToChatID: migrateTo,
		latency:         latency,
		tag:             tag,
		notificationID:  q.notificationID,
		resend:          resend,
		photo:           photo,
	}
	if tag.kind == db.MaintenancePacket {
		// A maintenance send cools no user, so there is nothing to release.
		return
	}
	// The cooldown tail runs detached,
	// so the deliverWG wait at shutdown covers the POST and its result,
	// not a pacing sleep.
	// The timer spawns the release goroutine only when it fires,
	// so a long postpone parks a timer entry, not a goroutine stack.
	// A group or channel caps tighter than the 1s per-user gap, at 20 messages/min,
	// so pace it slower to avoid self-triggering a 429.
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
	// endpointPace was already slept before the result,
	// so charge it against the user's total cooldown, not endpointCooldown.
	time.AfterFunc(cooldown-endpointPace, func() {
		// Release the user trySend cooled; the id is stable across
		// a chat migration, so there is nothing else to release.
		// The select may pick shutdownCh even while the drain still reads
		// cooledUsers (both arms ready), dropping the release and leaving that
		// user cooling for its remaining drain-window sends.
		// Accepted: those sends stay queued and re-arm (notifications)
		// or are dropped (replies) at shutdown anyway.
		select {
		case w.cooledUsers <- cooledUser{endpoint: endpoint, userID: userID}:
		case <-w.shutdownCh:
		}
	})
}
