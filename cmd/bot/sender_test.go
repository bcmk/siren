package main

import (
	"container/heap"
	"context"
	"testing"
	"time"

	"github.com/bcmk/siren/v2/internal/db"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type stubMessage struct{ id int64 }

func (s *stubMessage) chatID() int64 { return s.id }

func (s *stubMessage) send(_ context.Context, _ *bot.Bot) (*models.Message, error) {
	return nil, nil
}

func newPacketHeap() packetHeap {
	return packetHeap{
		maxReadyAt:  map[int64]readyAtValue{},
		chatPackets: map[int64]map[*outgoingPacket]struct{}{},
	}
}

func pushPacket(h *packetHeap, chatID int64, priority db.Priority) {
	heap.Push(h, &outgoingPacket{
		message:  &stubMessage{chatID},
		priority: priority,
		kind:     db.NotificationPacket,
	})
}

func chatID(id int64) *int64 { return &id }

type testPush struct {
	chatID   int64
	priority db.Priority
}

type testReady struct {
	chatID int64
	offset time.Duration
}

type nextActionStep struct {
	name         string
	now          time.Time
	setupHeap    []testPush
	setupReadyAt []testReady
	wantPacket   *int64
	wantWait     time.Duration
	wantHeapLen  int
	wantReadyAt  map[int64]time.Time
}

func TestNextAction(t *testing.T) {
	t0 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)
	t2 := t0.Add(2 * time.Second)

	tests := []struct {
		name  string
		steps []nextActionStep
	}{
		{
			name: "empty heap",
			steps: []nextActionStep{
				{
					name:        "returns nil and zero wait",
					now:         t0,
					wantPacket:  nil,
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "ready packet",
			steps: []nextActionStep{
				{
					name:        "pops and returns it",
					now:         t0,
					setupHeap:   []testPush{{100, db.PriorityLow}},
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "rate-limited packet",
			steps: []nextActionStep{
				{
					name:         "returns wait duration",
					now:          t0,
					setupHeap:    []testPush{{100, db.PriorityLow}},
					setupReadyAt: []testReady{{100, time.Second}},
					wantPacket:   nil,
					wantWait:     time.Second,
					wantHeapLen:  1,
					wantReadyAt:  map[int64]time.Time{100: t1},
				},
			},
		},
		{
			name: "readyAt beats priority",
			steps: []nextActionStep{
				{
					name:         "set earlier readyAt for low priority chat",
					now:          t0,
					setupReadyAt: []testReady{{200, time.Second}},
					wantPacket:   nil,
					wantWait:     0,
					wantHeapLen:  0,
					wantReadyAt:  map[int64]time.Time{200: t1},
				},
				{
					name:         "set later readyAt for high priority chat",
					now:          t0,
					setupReadyAt: []testReady{{100, 2 * time.Second}},
					wantPacket:   nil,
					wantWait:     0,
					wantHeapLen:  0,
					wantReadyAt:  map[int64]time.Time{100: t2, 200: t1},
				},
				{
					name: "low priority with earlier readyAt goes first",
					now:  t1,
					setupHeap: []testPush{
						{100, db.PriorityHigh},
						{200, db.PriorityLow},
					},
					wantPacket:  chatID(200),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: map[int64]time.Time{100: t2},
				},
				{
					name:        "high priority with later readyAt waits",
					now:         t1,
					wantPacket:  nil,
					wantWait:    time.Second,
					wantHeapLen: 1,
					wantReadyAt: map[int64]time.Time{100: t2},
				},
				{
					name:        "high priority ready after expiry",
					now:         t2,
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "priority beats seq",
			steps: []nextActionStep{
				{
					name:        "high priority pushed second still goes first",
					now:         t0,
					setupHeap:   []testPush{{100, db.PriorityLow}, {200, db.PriorityHigh}},
					wantPacket:  chatID(200),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: nil,
				},
				{
					name:        "low priority second",
					now:         t0,
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "seq preserves FIFO within same priority",
			steps: []nextActionStep{
				{
					name:        "first pushed goes first",
					now:         t0,
					setupHeap:   []testPush{{100, db.PriorityLow}, {200, db.PriorityLow}},
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: nil,
				},
				{
					name:        "second pushed goes second",
					now:         t0,
					wantPacket:  chatID(200),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "rate-limited skips to ready",
			steps: []nextActionStep{
				{
					name:         "returns ready packet from different chat",
					now:          t0,
					setupHeap:    []testPush{{100, db.PriorityHigh}, {200, db.PriorityLow}},
					setupReadyAt: []testReady{{100, time.Second}},
					wantPacket:   chatID(200),
					wantWait:     0,
					wantHeapLen:  1,
					wantReadyAt:  map[int64]time.Time{100: t1},
				},
			},
		},
		{
			name: "cleanup expired",
			steps: []nextActionStep{
				{
					name:         "removes expired readyAt",
					now:          t0,
					setupHeap:    []testPush{{200, db.PriorityLow}},
					setupReadyAt: []testReady{{100, -time.Second}},
					wantPacket:   chatID(200),
					wantWait:     0,
					wantHeapLen:  0,
					wantReadyAt:  nil,
				},
			},
		},
		{
			name: "same chat multiple packets",
			steps: []nextActionStep{
				{
					name:        "first packet is ready",
					now:         t0,
					setupHeap:   []testPush{{100, db.PriorityLow}, {100, db.PriorityLow}},
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: nil,
				},
				{
					name:         "second packet is rate-limited",
					now:          t0,
					setupReadyAt: []testReady{{100, time.Second}},
					wantPacket:   nil,
					wantWait:     time.Second,
					wantHeapLen:  1,
					wantReadyAt:  map[int64]time.Time{100: t1},
				},
				{
					name:        "second packet ready after expiry",
					now:         t1,
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "readyAt expiry",
			steps: []nextActionStep{
				{
					name:         "set readyAt",
					now:          t0,
					setupReadyAt: []testReady{{100, time.Second}},
					wantPacket:   nil,
					wantWait:     0,
					wantHeapLen:  0,
					wantReadyAt:  map[int64]time.Time{100: t1},
				},
				{
					name:        "entry removed after expiry",
					now:         t2,
					wantPacket:  nil,
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "readyAt not expired prematurely",
			steps: []nextActionStep{
				{
					name:         "set two readyAt values",
					now:          t0,
					setupReadyAt: []testReady{{100, time.Second}, {100, 2 * time.Second}},
					wantPacket:   nil,
					wantWait:     0,
					wantHeapLen:  0,
					wantReadyAt:  map[int64]time.Time{100: t2},
				},
				{
					name:        "second setReadyAt survives first expiry",
					now:         t0.Add(time.Second + 500*time.Millisecond),
					wantPacket:  nil,
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: map[int64]time.Time{100: t2},
				},
			},
		},
		{
			name: "single packet pushed and returned",
			steps: []nextActionStep{
				{
					name:        "ready packet is sent",
					now:         t0,
					setupHeap:   []testPush{{100, db.PriorityHigh}},
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "new packet sorted by priority",
			steps: []nextActionStep{
				{
					name:        "high priority goes first",
					now:         t0,
					setupHeap:   []testPush{{200, db.PriorityLow}, {100, db.PriorityHigh}},
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: nil,
				},
				{
					name:        "low priority sent second",
					now:         t0,
					wantPacket:  chatID(200),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "new packet does not disrupt existing order",
			steps: []nextActionStep{
				{
					name:        "high priority still first after low priority push",
					now:         t0,
					setupHeap:   []testPush{{100, db.PriorityHigh}, {200, db.PriorityLow}, {300, db.PriorityLow}},
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 2,
					wantReadyAt: nil,
				},
				{
					name:        "earlier low priority before later",
					now:         t0,
					wantPacket:  chatID(200),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: nil,
				},
				{
					name:        "later low priority last",
					now:         t0,
					wantPacket:  chatID(300),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "rate limit reorders same-chat packets",
			steps: []nextActionStep{
				{
					name:        "send to 100 first",
					now:         t0,
					setupHeap:   []testPush{{100, db.PriorityLow}, {200, db.PriorityLow}, {100, db.PriorityLow}, {300, db.PriorityLow}},
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 3,
					wantReadyAt: nil,
				},
				{
					name:         "100 is rate-limited, 200 goes next",
					now:          t0,
					setupReadyAt: []testReady{{100, time.Second}},
					wantPacket:   chatID(200),
					wantWait:     0,
					wantHeapLen:  2,
					wantReadyAt:  map[int64]time.Time{100: t1},
				},
				{
					name:        "300 goes next",
					now:         t0,
					wantPacket:  chatID(300),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: map[int64]time.Time{100: t1},
				},
				{
					name:        "100 is still rate-limited",
					now:         t0,
					wantPacket:  nil,
					wantWait:    time.Second,
					wantHeapLen: 1,
					wantReadyAt: map[int64]time.Time{100: t1},
				},
				{
					name:        "100 ready after expiry",
					now:         t1,
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "slow send expires multiple readyAt entries",
			steps: []nextActionStep{
				{
					name: "rate-limited chats wait while ready chat is popped",
					now:  t0,
					setupHeap: []testPush{
						{100, db.PriorityLow},
						{200, db.PriorityHigh},
						{300, db.PriorityLow},
					},
					setupReadyAt: []testReady{{100, time.Second}, {200, 2 * time.Second}},
					wantPacket:   chatID(300),
					wantWait:     0,
					wantHeapLen:  2,
					wantReadyAt:  map[int64]time.Time{100: t1, 200: t2},
				},
				{
					name:        "high priority goes first after both expire",
					now:         t2,
					wantPacket:  chatID(200),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "shorter rate limit expires before longer",
			steps: []nextActionStep{
				{
					name: "different rate limit durations",
					now:  t0,
					setupHeap: []testPush{
						{100, db.PriorityLow},
						{200, db.PriorityLow},
					},
					setupReadyAt: []testReady{{100, 2 * time.Second}, {200, time.Second}},
					wantPacket:   nil,
					wantWait:     time.Second,
					wantHeapLen:  2,
					wantReadyAt:  map[int64]time.Time{100: t2, 200: t1},
				},
				{
					name:        "shorter limit expires first",
					now:         t1,
					wantPacket:  chatID(200),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: map[int64]time.Time{100: t2},
				},
				{
					name:        "longer limit expires second",
					now:         t2,
					wantPacket:  chatID(100),
					wantWait:    0,
					wantHeapLen: 0,
					wantReadyAt: nil,
				},
			},
		},
		{
			name: "out-of-order rate limits both expire at once",
			steps: []nextActionStep{
				{
					name: "longer limit added first, shorter second",
					now:  t0,
					setupHeap: []testPush{
						{100, db.PriorityLow},
						{200, db.PriorityHigh},
					},
					setupReadyAt: []testReady{{100, 2 * time.Second}, {200, time.Second}},
					wantPacket:   nil,
					wantWait:     time.Second,
					wantHeapLen:  2,
					wantReadyAt:  map[int64]time.Time{100: t2, 200: t1},
				},
				{
					name:        "both expire, high priority first",
					now:         t2,
					wantPacket:  chatID(200),
					wantWait:    0,
					wantHeapLen: 1,
					wantReadyAt: nil,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newPacketHeap()

			for _, step := range tt.steps {
				t.Run(step.name, func(t *testing.T) {
					for _, p := range step.setupHeap {
						pushPacket(&h, p.chatID, p.priority)
					}
					for _, r := range step.setupReadyAt {
						h.setReadyAt(r.chatID, step.now.Add(r.offset))
					}

					pkt, wait := h.nextAction(step.now)

					if step.wantPacket == nil {
						if pkt != nil {
							t.Errorf("expected nil packet, got chatID %d", pkt.message.chatID())
						}
					} else {
						if pkt == nil {
							t.Fatal("expected a packet, got nil")
						}
						if pkt.message.chatID() != *step.wantPacket {
							t.Errorf("expected chatID %d, got %d", *step.wantPacket, pkt.message.chatID())
						}
					}

					if wait != step.wantWait {
						t.Errorf("expected wait %v, got %v", step.wantWait, wait)
					}

					if h.Len() != step.wantHeapLen {
						t.Errorf("expected heap len %d, got %d", step.wantHeapLen, h.Len())
					}

					if len(h.maxReadyAt) != len(step.wantReadyAt) {
						t.Errorf("expected %d readyAt entries, got %d", len(step.wantReadyAt), len(h.maxReadyAt))
					}
					for id, want := range step.wantReadyAt {
						got, ok := h.maxReadyAt[id]
						if !ok {
							t.Errorf("missing readyAt entry for chatID %d", id)
						} else if !got.t.Equal(want) {
							t.Errorf("readyAt[%d] = %v, want %v", id, got.t, want)
						}
					}
				})
			}
		})
	}
}
