package main

import (
	"context"
	"math"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bcmk/siren/v3/internal/botconfig"
	"github.com/bcmk/siren/v3/internal/db"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// countingMessage records concurrent sends so a test can assert single-flight.
type countingMessage struct {
	id          int64
	inflight    *int32
	maxInflight *int32
}

func (m *countingMessage) chatID() int64      { return m.id }
func (m *countingMessage) setChatID(id int64) { m.id = id }

func (m *countingMessage) send(context.Context, *bot.Bot) (*models.Message, error) {
	n := atomic.AddInt32(m.inflight, 1)
	for {
		peak := atomic.LoadInt32(m.maxInflight)
		if n <= peak || atomic.CompareAndSwapInt32(m.maxInflight, peak, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	atomic.AddInt32(m.inflight, -1)
	return nil, nil
}

// TestSenderScheduling drives the real worker and checks priority order,
// per-user cooldown and the single-flight slot.
func TestSenderScheduling(t *testing.T) {
	t.Parallel()
	type enq struct {
		userIdx int
		pri     db.Priority
	}
	tests := []struct {
		name    string
		users   int
		enqueue []enq
		want    []int
	}{
		{
			// 0 sends at once; its second message waits out the cooldown,
			// so 1 slips in front.
			name:    "per-user cooldown defers the same user",
			users:   2,
			enqueue: []enq{{0, db.PriorityLow}, {0, db.PriorityLow}, {1, db.PriorityLow}},
			want:    []int{0, 1, 0},
		},
		{
			// 0 sends at once; 1 and 2 queue behind it,
			// and the high-priority 2 jumps the low-priority 1.
			name:    "priority wins among queued messages",
			users:   3,
			enqueue: []enq{{0, db.PriorityLow}, {1, db.PriorityLow}, {2, db.PriorityHigh}},
			want:    []int{0, 2, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.commonCooling = false

			userIDs := make([]db.UserID, tt.users)
			idxOf := map[db.UserID]int{}
			for i := range userIDs {
				userIDs[i], _ = w.db.AddUser(int64(100+i), 3, 0, "private")
				idxOf[userIDs[i]] = i
			}

			var inflight, maxInflight int32
			for _, e := range tt.enqueue {
				w.enqueueMessage(e.pri, "test",
					&countingMessage{inflight: &inflight, maxInflight: &maxInflight},
					db.MessagePacket, userIDs[e.userIdx], 0)
			}

			var order []int
			for len(order) < len(tt.enqueue) {
				select {
				case r := <-w.sendResults:
					order = append(order, idxOf[r.userID])
					w.onSendDone()
				case u := <-w.cooledUsers:
					w.onUserCooled(u)
				}
			}

			if !slices.Equal(order, tt.want) {
				t.Errorf("delivery order = %v, want %v", order, tt.want)
			}
			if maxInflight != 1 {
				t.Errorf("max concurrent sends = %d, want 1", maxInflight)
			}
		})
	}
}

// okMessage delivers successfully and instantly, for timing assertions.
type okMessage struct{ id int64 }

func (m *okMessage) chatID() int64      { return m.id }
func (m *okMessage) setChatID(id int64) { m.id = id }

func (m *okMessage) send(context.Context, *bot.Bot) (*models.Message, error) {
	return nil, nil
}

// TestSendQueueOrder checks dispatch ordering across users:
// a cooling user's messages wait until its release,
// then priority wins, then push sequence breaks ties.
func TestSendQueueOrder(t *testing.T) {
	t.Parallel()
	type item struct {
		userID   db.UserID
		priority db.Priority
		seq      uint64
	}
	tests := []struct {
		name    string
		items   []item
		cooling []db.UserID
		want    []db.UserID
	}{
		{
			name:  "priority beats push sequence",
			items: []item{{1, db.PriorityLow, 1}, {2, db.PriorityHigh, 2}},
			want:  []db.UserID{2, 1},
		},
		{
			name:  "sequence preserves FIFO within a priority",
			items: []item{{1, db.PriorityLow, 1}, {2, db.PriorityLow, 2}},
			want:  []db.UserID{1, 2},
		},
		{
			name:    "a cooling user waits despite higher priority",
			items:   []item{{1, db.PriorityHigh, 1}, {2, db.PriorityLow, 2}},
			cooling: []db.UserID{1},
			want:    []db.UserID{2, 1},
		},
		{
			name:    "ready users drain before the cooling one",
			items:   []item{{1, db.PriorityLow, 1}, {2, db.PriorityLow, 2}, {3, db.PriorityLow, 3}},
			cooling: []db.UserID{1},
			want:    []db.UserID{2, 3, 1},
		},
		{
			name:  "a single message pops",
			items: []item{{7, db.PriorityLow, 1}},
			want:  []db.UserID{7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSendQueue()
			for _, c := range tt.cooling {
				s.startCooling(c)
			}
			for _, it := range tt.items {
				s.push(&queuedMessage{userID: it.userID, priority: it.priority, seq: it.seq})
			}
			var got []db.UserID
			for s.Len() > 0 {
				q := s.pop()
				if q == nil {
					// Every remaining user is cooling; release them all.
					for _, c := range tt.cooling {
						s.stopCooling(c)
					}
					continue
				}
				got = append(got, q.userID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("pop order = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSendQueueBookkeeping checks the queue's internal accounting:
// a cooling user's messages are withheld and resurface on release,
// and the per-user map and size counter drain to empty.
func TestSendQueueBookkeeping(t *testing.T) {
	t.Parallel()
	s := newSendQueue()
	for i, userID := range []db.UserID{1, 1, 2} {
		s.push(&queuedMessage{userID: userID, seq: uint64(i)})
	}
	if s.Len() != 3 || len(s.byUser) != 2 {
		t.Fatalf("size = %d, users = %d, want 3 and 2", s.Len(), len(s.byUser))
	}
	s.startCooling(1)
	if q := s.pop(); q == nil || q.userID != 2 {
		t.Fatalf("pop = %v, want user 2's message", q)
	}
	if q := s.pop(); q != nil {
		t.Fatalf("pop returned a cooling user's message: %v", q)
	}
	s.stopCooling(1)
	for range 2 {
		if q := s.pop(); q == nil || q.userID != 1 {
			t.Fatalf("pop = %v, want user 1's message", q)
		}
	}
	if s.Len() != 0 || len(s.byUser) != 0 {
		t.Errorf("not drained: size = %d, users = %v", s.Len(), s.byUser)
	}
}

// TestTooManyRequestsDelay pins the three branches of the 429 pause:
// the fallback when retry_after is absent, the value itself,
// and the cap that also guards the nanosecond conversion from overflowing.
func TestTooManyRequestsDelay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		retryAfter int
		want       time.Duration
	}{
		{"absent retry_after falls back", 0, tooManyRequestsBackoff},
		{"negative retry_after falls back", -5, tooManyRequestsBackoff},
		{"normal retry_after wins", 30, 30 * time.Second},
		{"huge retry_after is capped", 100000, tooManyRequestsMaxBackoff},
		{"max int retry_after is capped", math.MaxInt, tooManyRequestsMaxBackoff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tooManyRequestsDelay(tt.retryAfter); got != tt.want {
				t.Errorf("tooManyRequestsDelay(%d) = %v, want %v", tt.retryAfter, got, tt.want)
			}
		})
	}
}

// TestTransientDelay pins the transient classification and its pauses.
func TestTransientDelay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		result        int
		retryAfter    int
		wantPause     time.Duration
		wantTransient bool
	}{
		{"429 waits out retry_after", messageTooManyRequests, 30, 30 * time.Second, true},
		{"timeout postpones", messageTimeout, 0, transientErrorPostpone, true},
		{"network error postpones", messageUnknownNetworkError, 0, transientErrorPostpone, true},
		{"sent is not transient", messageSent, 30, 0, false},
		{"migrate is not transient", messageMigrate, 30, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pause, transient := transientDelay(tt.result, tt.retryAfter)
			if pause != tt.wantPause || transient != tt.wantTransient {
				t.Errorf("transientDelay(%d, %d) = (%v, %t), want (%v, %t)",
					tt.result, tt.retryAfter, pause, transient, tt.wantPause, tt.wantTransient)
			}
		})
	}
}

// TestDeliverTiming runs a delivery on a fake clock and checks the durations
// the old readyAt test asserted: the result is reported after the global
// pacing gap, and the user is freed only after the full per-user cooldown.
func TestDeliverTiming(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledUsers: make(chan db.UserID, 16),
		}
		start := time.Now()
		go w.deliver(&queuedMessage{
			userID:   1,
			endpoint: "ep",
			message:  &okMessage{id: 1},
			priority: db.PriorityHigh,
			kind:     db.MessagePacket,
		})

		if r := <-w.sendResults; r.result != messageSent {
			t.Fatalf("result = %d, want messageSent", r.result)
		}
		if d := time.Since(start); d != commonCooldown {
			t.Errorf("result reported after %v, want the %v pacing gap", d, commonCooldown)
		}

		<-w.cooledUsers
		if d := time.Since(start); d != userCooldown {
			t.Errorf("user freed after %v, want the %v cooldown", d, userCooldown)
		}
	})
}

// tooManyRequests always 429s, carrying retryAfter as Telegram's retry_after.
type tooManyRequests struct {
	id         int64
	retryAfter int
}

func (m *tooManyRequests) chatID() int64      { return m.id }
func (m *tooManyRequests) setChatID(id int64) { m.id = id }
func (m *tooManyRequests) send(context.Context, *bot.Bot) (*models.Message, error) {
	return nil, &bot.TooManyRequestsError{RetryAfter: m.retryAfter}
}

// TestDeliverDropsMaintenanceOnTransientFailure checks
// that a maintenance send's transient failure is dropped, not re-queued:
// one result with no resend, and deliver returns at once.
func TestDeliverDropsMaintenanceOnTransientFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledUsers: make(chan db.UserID, 16),
		}
		done := make(chan struct{})
		go func() {
			w.deliver(&queuedMessage{
				endpoint: "ep",
				message:  &tooManyRequests{id: 1},
				priority: db.PriorityHigh,
				kind:     db.MaintenancePacket,
			})
			close(done)
		}()

		r := <-w.sendResults
		if r.result != messageTooManyRequests || r.resend != nil {
			t.Fatalf("result = {result %d, resend set %t}, want a dropping 429",
				r.result, r.resend != nil)
		}
		<-done
		// A maintenance send cools no user; nothing else may arrive.
		synctest.Wait()
		select {
		case r := <-w.sendResults:
			t.Errorf("unexpected extra result %d", r.result)
		case id := <-w.cooledUsers:
			t.Errorf("unexpected user release of %d", id)
		default:
		}
	})
}

// TestDeliverPostponesTooManyRequests checks
// that a regular message's 429 postpones instead of retrying in place:
// deliver reports a single non-retry result carrying the message to re-queue,
// and frees the user only after Telegram's retry_after.
func TestDeliverPostponesTooManyRequests(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledUsers: make(chan db.UserID, 16),
		}
		q := &queuedMessage{
			userID:   1,
			endpoint: "ep",
			message:  &tooManyRequests{id: 1, retryAfter: 30},
			priority: db.PriorityHigh,
			kind:     db.MessagePacket,
		}
		start := time.Now()
		done := make(chan struct{})
		go func() {
			w.deliver(q)
			close(done)
		}()

		r := <-w.sendResults
		if r.result != messageTooManyRequests || r.resend != q {
			t.Fatalf("result = {result %d, resend set %t}, want a postponing 429 carrying the message",
				r.result, r.resend != nil)
		}
		// deliver returns after the global pace (1s here, the bot-wide throttle),
		// not the 30s retry_after: that long park lives only in the detached
		// release timer, outside deliverWG, so a shutdown never waits it out.
		<-done
		if d := time.Since(start); d != tooManyRequestsGlobalPause {
			t.Errorf("deliver returned after %v, want the %v global pace", d, tooManyRequestsGlobalPause)
		}
		<-w.cooledUsers
		if d := time.Since(start); d != 30*time.Second {
			t.Errorf("user freed after %v, want the 30s retry_after", d)
		}
	})
}

// TestDeliverGlobalPaceOn429 pins the pace held before the slot frees:
// any 429 widens it to throttle a bot-wide limit, regardless of chat type,
// while a success keeps the common gap.
func TestDeliverGlobalPaceOn429(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		message  sendable
		wantPace time.Duration
	}{
		{"private 429 widens the global pace", &tooManyRequests{id: 1, retryAfter: 30}, tooManyRequestsGlobalPause},
		{"group 429 widens it too", &tooManyRequests{id: -100, retryAfter: 30}, tooManyRequestsGlobalPause},
		{"success keeps the common pace", &okMessage{id: 1}, commonCooldown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := &worker{
					cfg:         &botconfig.Config{},
					bots:        map[string]*bot.Bot{"ep": nil},
					sendResults: make(chan msgSendResult, 16),
					cooledUsers: make(chan db.UserID, 16),
				}
				start := time.Now()
				done := make(chan struct{})
				go func() {
					w.deliver(&queuedMessage{
						userID:   1,
						endpoint: "ep",
						message:  tt.message,
						priority: db.PriorityHigh,
						kind:     db.MessagePacket,
					})
					close(done)
				}()
				<-w.sendResults
				<-done
				if d := time.Since(start); d != tt.wantPace {
					t.Errorf("deliver returned after %v, want %v", d, tt.wantPace)
				}
			})
		})
	}
}

// TestDeliverPaceEndsOnShutdown checks the 429 global pace is abandoned
// at once on shutdown, so the drain never sleeps out the full second.
func TestDeliverPaceEndsOnShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledUsers: make(chan db.UserID, 16),
			shutdownCh:  make(chan struct{}),
		}
		close(w.shutdownCh)
		start := time.Now()
		done := make(chan struct{})
		go func() {
			w.deliver(&queuedMessage{
				userID:   1,
				endpoint: "ep",
				message:  &tooManyRequests{id: 1, retryAfter: 30},
				priority: db.PriorityHigh,
				kind:     db.MessagePacket,
			})
			close(done)
		}()
		<-w.sendResults
		<-done
		if d := time.Since(start); d != 0 {
			t.Errorf("deliver returned after %v, want immediately on shutdown", d)
		}
	})
}

// TestDrainKeepsPostponedNotificationArmed checks the shutdown drain:
// a postponed send (dispResend) must not finalize its notification,
// so the sending=1 row re-arms next start instead of silently dropping;
// a delivered send must finalize and delete its row.
func TestDrainKeepsPostponedNotificationArmed(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.shuttingDown.Store(true) // the drain runs only during shutdown

	userID, _ := w.db.AddUser(100, 3, 0, "private")
	streamerID := insertTestStreamer(&w.db, db.Streamer{Nickname: "s1"})
	w.db.StoreNotifications([]db.Notification{
		{Endpoint: "test", UserID: userID, StreamerID: &streamerID},
		{Endpoint: "test", UserID: userID, StreamerID: &streamerID},
	})
	nots := w.db.NewNotifications() // marks both sending=1, as during a send
	if len(nots) != 2 {
		t.Fatalf("fetched %d notifications, want 2", len(nots))
	}
	postponed, delivered := nots[0], nots[1]

	w.sendResults <- msgSendResult{
		result:         messageTimeout,
		endpoint:       "test",
		userID:         userID,
		kind:           db.NotificationPacket,
		notificationID: postponed.ID,
		resend: &queuedMessage{
			userID:         userID,
			endpoint:       "test",
			message:        &okMessage{id: 100},
			kind:           db.NotificationPacket,
			notificationID: postponed.ID,
		},
	}
	w.sendResults <- msgSendResult{
		result:         messageSent,
		endpoint:       "test",
		userID:         userID,
		kind:           db.NotificationPacket,
		notificationID: delivered.ID,
	}
	w.drainSendResults()

	sending := w.db.MustInt("select sending from notification_queue where id = $1", postponed.ID)
	if sending != 1 {
		t.Errorf("postponed notification sending = %d, want 1 (armed for restart)", sending)
	}
	left := w.db.MustInt("select count(*) from notification_queue where id = $1", delivered.ID)
	if left != 0 {
		t.Errorf("delivered notification rows = %d, want 0 (finalized)", left)
	}
}
