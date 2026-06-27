package main

import (
	"container/heap"
	"context"
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

// TestSendHeapOrder checks the ordering the old packetHeap test covered:
// a cooling user sinks below every ready one, then priority wins,
// then push sequence breaks ties.
func TestSendHeapOrder(t *testing.T) {
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
			name:    "a cooling user sinks below a ready one despite higher priority",
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
			h := newSendHeap()
			for _, c := range tt.cooling {
				h.userCooling[c] = struct{}{}
			}
			for _, it := range tt.items {
				heap.Push(&h, &queuedMessage{userID: it.userID, priority: it.priority, seq: it.seq})
			}
			var got []db.UserID
			for h.Len() > 0 {
				got = append(got, heap.Pop(&h).(*queuedMessage).userID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("pop order = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSendHeapUserCount checks the per-user counter the heap keeps,
// so a cooling flip can skip the O(n) re-init when the user has
// nothing else queued.
func TestSendHeapUserCount(t *testing.T) {
	h := newSendHeap()
	for _, userID := range []db.UserID{1, 1, 2} {
		heap.Push(&h, &queuedMessage{userID: userID})
	}
	if h.userCount[1] != 2 || h.userCount[2] != 1 {
		t.Fatalf("userCount = %v, want {1:2, 2:1}", h.userCount)
	}
	for h.Len() > 0 {
		heap.Pop(&h)
	}
	if len(h.userCount) != 0 {
		t.Errorf("userCount not drained to empty: %v", h.userCount)
	}
}

// TestEnqueueAtPreservesRequestTime checks that a re-queued message keeps
// the request time of its first attempt, so its latency log spans the whole
// journey rather than restarting at the re-queue.
func TestEnqueueAtPreservesRequestTime(t *testing.T) {
	w := &worker{
		sendQueue:     newSendHeap(),
		commonCooling: true, // parks the message instead of sending it
	}
	past := time.Now().Add(-42 * time.Second)
	w.enqueueMessageAt(past, db.PriorityHigh, "ep", &okMessage{id: 1}, db.MessagePacket, 0, 0)
	q := heap.Pop(&w.sendQueue).(*queuedMessage)
	if !q.requestedAt.Equal(past) {
		t.Errorf("requestedAt = %v, want the preserved %v", q.requestedAt, past)
	}
}

// TestDeliverTiming runs a delivery on a fake clock and checks the durations
// the old readyAt test asserted: the result is reported after the global
// pacing gap, and the user is freed only after the full per-user cooldown.
func TestDeliverTiming(t *testing.T) {
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

// tooManyRequests always 429s, so deliver keeps retrying on the 8s backoff.
type tooManyRequests struct{ id int64 }

func (m *tooManyRequests) chatID() int64      { return m.id }
func (m *tooManyRequests) setChatID(id int64) { m.id = id }
func (m *tooManyRequests) send(context.Context, *bot.Bot) (*models.Message, error) {
	return nil, &bot.TooManyRequestsError{}
}

// deliver must abandon the retry the instant shutdown starts,
// not sleep the whole 8s backoff and eat the shared grace window.
func TestDeliverAbandonsBackoffOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledUsers: make(chan db.UserID, 16),
			shutdownCh:  make(chan struct{}),
		}
		done := make(chan struct{})
		go func() {
			w.deliver(&queuedMessage{
				userID:   1,
				endpoint: "ep",
				message:  &tooManyRequests{id: 1},
				priority: db.PriorityHigh,
				kind:     db.MessagePacket,
			})
			close(done)
		}()

		// The first 429 result lands, then deliver settles into the backoff wait.
		if r := <-w.sendResults; r.result != messageTooManyRequests {
			t.Fatalf("result = %d, want messageTooManyRequests", r.result)
		}
		synctest.Wait()

		// Shutdown wakes it at once, well before the 8s backoff would elapse.
		start := time.Now()
		close(w.shutdownCh)
		<-done
		if d := time.Since(start); d != 0 {
			t.Errorf("deliver returned after %v, want immediately on shutdown", d)
		}
	})
}
