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
// per-chat cooldown and the single-flight slot.
func TestSenderScheduling(t *testing.T) {
	type enq struct {
		chatIdx int
		pri     db.Priority
	}
	tests := []struct {
		name    string
		chats   int
		enqueue []enq
		want    []int
	}{
		{
			// 0 sends at once; its second message waits out the cooldown,
			// so 1 slips in front.
			name:    "per-chat cooldown defers the same chat",
			chats:   2,
			enqueue: []enq{{0, db.PriorityLow}, {0, db.PriorityLow}, {1, db.PriorityLow}},
			want:    []int{0, 1, 0},
		},
		{
			// 0 sends at once; 1 and 2 queue behind it,
			// and the high-priority 2 jumps the low-priority 1.
			name:    "priority wins among queued messages",
			chats:   3,
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

			chatIDs := make([]int64, tt.chats)
			idxOf := map[int64]int{}
			for i := range chatIDs {
				chatIDs[i] = int64(100 + i)
				idxOf[chatIDs[i]] = i
			}

			var inflight, maxInflight int32
			for _, e := range tt.enqueue {
				w.enqueueMessage(e.pri, "test",
					&countingMessage{id: chatIDs[e.chatIdx], inflight: &inflight, maxInflight: &maxInflight},
					db.MessagePacket, 0)
			}

			var order []int
			for len(order) < len(tt.enqueue) {
				select {
				case r := <-w.sendResults:
					order = append(order, idxOf[r.chatID])
					w.onSendDone()
				case c := <-w.cooledChats:
					w.onChatCooled(c)
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
// a cooling chat sinks below every ready one, then priority wins,
// then push sequence breaks ties.
func TestSendHeapOrder(t *testing.T) {
	type item struct {
		chatID   int64
		priority db.Priority
		seq      uint64
	}
	tests := []struct {
		name    string
		items   []item
		cooling []int64
		want    []int64
	}{
		{
			name:  "priority beats push sequence",
			items: []item{{1, db.PriorityLow, 1}, {2, db.PriorityHigh, 2}},
			want:  []int64{2, 1},
		},
		{
			name:  "sequence preserves FIFO within a priority",
			items: []item{{1, db.PriorityLow, 1}, {2, db.PriorityLow, 2}},
			want:  []int64{1, 2},
		},
		{
			name:    "a cooling chat sinks below a ready one despite higher priority",
			items:   []item{{1, db.PriorityHigh, 1}, {2, db.PriorityLow, 2}},
			cooling: []int64{1},
			want:    []int64{2, 1},
		},
		{
			name:    "ready chats drain before the cooling one",
			items:   []item{{1, db.PriorityLow, 1}, {2, db.PriorityLow, 2}, {3, db.PriorityLow, 3}},
			cooling: []int64{1},
			want:    []int64{2, 3, 1},
		},
		{
			name:  "a single message pops",
			items: []item{{7, db.PriorityLow, 1}},
			want:  []int64{7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSendHeap()
			for _, c := range tt.cooling {
				h.chatCooling[c] = struct{}{}
			}
			for _, it := range tt.items {
				heap.Push(&h, &queuedMessage{chatID: it.chatID, priority: it.priority, seq: it.seq})
			}
			var got []int64
			for h.Len() > 0 {
				got = append(got, heap.Pop(&h).(*queuedMessage).chatID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("pop order = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSendHeapChatCount checks the per-chat counter the heap keeps,
// so a cooling flip can skip the O(n) re-init when the chat has
// nothing else queued.
func TestSendHeapChatCount(t *testing.T) {
	h := newSendHeap()
	for _, chatID := range []int64{1, 1, 2} {
		heap.Push(&h, &queuedMessage{chatID: chatID})
	}
	if h.chatCount[1] != 2 || h.chatCount[2] != 1 {
		t.Fatalf("chatCount = %v, want {1:2, 2:1}", h.chatCount)
	}
	for h.Len() > 0 {
		heap.Pop(&h)
	}
	if len(h.chatCount) != 0 {
		t.Errorf("chatCount not drained to empty: %v", h.chatCount)
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
	w.enqueueMessageAt(past, db.PriorityHigh, "ep", &okMessage{id: 1}, db.MessagePacket, 0)
	q := heap.Pop(&w.sendQueue).(*queuedMessage)
	if !q.requestedAt.Equal(past) {
		t.Errorf("requestedAt = %v, want the preserved %v", q.requestedAt, past)
	}
}

// TestDeliverTiming runs a delivery on a fake clock and checks the durations
// the old readyAt test asserted: the result is reported after the global
// pacing gap, and the chat is freed only after the full per-chat cooldown.
func TestDeliverTiming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &worker{
			cfg:         &botconfig.Config{},
			bots:        map[string]*bot.Bot{"ep": nil},
			sendResults: make(chan msgSendResult, 16),
			cooledChats: make(chan int64, 16),
		}
		start := time.Now()
		go w.deliver(&queuedMessage{
			chatID:   1,
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

		<-w.cooledChats
		if d := time.Since(start); d != chatCooldown {
			t.Errorf("chat freed after %v, want the %v cooldown", d, chatCooldown)
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
			cooledChats: make(chan int64, 16),
			shutdownCh:  make(chan struct{}),
		}
		done := make(chan struct{})
		go func() {
			w.deliver(&queuedMessage{
				chatID:   1,
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
