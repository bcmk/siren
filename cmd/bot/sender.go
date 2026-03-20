// This file implements a priority queue with per-user
// rate limiting for outgoing Telegram messages.
//
// Producers call enqueueMessage which pushes packets into a buffered channel.
// A single sender goroutine owns a packetHeap,
// drains all pending packets from the channel into the heap each iteration,
// and sends the highest-priority ready packet.
//
// The heap is a min-heap ordered by:
//  1. readyAt time — earlier is better.
//     Zero (default) means ready immediately.
//     Rate-limited chats get a future readyAt and sink to the bottom.
//  2. priority — lower value wins (db.PriorityHigh < db.PriorityLow)
//  3. push sequence — global sequence counter
//
// Per-user rate limiting:
// After each send, setReadyAt marks the chat as unavailable for 1 second.
// The heap re-sorts so other chats' packets flow while the rate-limited chat waits.
//
// Cleanup: maxReadyAt entries are removed after they expire.
// A cleanup heap (min-heap by readyAt time) tracks per-chatID entries.
// Superseded entries (seq mismatch) are skipped.
// When an entry expires, its map entry is deleted and
// heap.Fix restores affected packets' heap positions.
package main

import (
	"container/heap"
	"time"

	"github.com/bcmk/siren/v2/internal/db"
)

const maxHeapLen = 20000

type readyAtValue struct {
	t   time.Time
	seq uint64
}

type cleanupEntry struct {
	chatID  int64
	readyAt time.Time
	seq     uint64
}

type cleanupHeap []cleanupEntry

func (h cleanupHeap) Len() int           { return len(h) }
func (h cleanupHeap) Less(i, j int) bool { return h[i].readyAt.Before(h[j].readyAt) }
func (h cleanupHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *cleanupHeap) Push(x any)        { *h = append(*h, x.(cleanupEntry)) }
func (h *cleanupHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type packetHeap struct {
	packets     []*outgoingPacket
	pushSeq     uint64
	maxReadyAt  map[int64]readyAtValue
	chatPackets map[int64]map[*outgoingPacket]struct{}
	readyAtSeq  uint64
	cleanup     cleanupHeap
}

func (h *packetHeap) Len() int { return len(h.packets) }

func (h *packetHeap) Less(i, j int) bool {
	ri := h.maxReadyAt[h.packets[i].message.chatID()].t
	rj := h.maxReadyAt[h.packets[j].message.chatID()].t
	if !ri.Equal(rj) {
		return ri.Before(rj)
	}
	if h.packets[i].priority != h.packets[j].priority {
		return h.packets[i].priority < h.packets[j].priority
	}
	return h.packets[i].seq < h.packets[j].seq
}

func (h *packetHeap) Swap(i, j int) {
	h.packets[i], h.packets[j] = h.packets[j], h.packets[i]
	h.packets[i].heapIndex = i
	h.packets[j].heapIndex = j
}

func (h *packetHeap) Push(x any) {
	pkt := x.(*outgoingPacket)
	h.pushSeq++
	pkt.seq = h.pushSeq
	pkt.heapIndex = len(h.packets)
	h.packets = append(h.packets, pkt)
	chatID := pkt.message.chatID()
	if h.chatPackets[chatID] == nil {
		h.chatPackets[chatID] = map[*outgoingPacket]struct{}{}
	}
	h.chatPackets[chatID][pkt] = struct{}{}
}

func (h *packetHeap) Pop() any {
	old := h.packets
	n := len(old)
	x := old[n-1]
	old[n-1] = nil
	x.heapIndex = -1
	h.packets = old[:n-1]
	chatID := x.message.chatID()
	delete(h.chatPackets[chatID], x)
	if len(h.chatPackets[chatID]) == 0 {
		delete(h.chatPackets, chatID)
	}
	return x
}

func (h *packetHeap) setReadyAt(chatID int64, t time.Time) {
	h.readyAtSeq++
	h.maxReadyAt[chatID] = readyAtValue{t, h.readyAtSeq}
	heap.Push(&h.cleanup, cleanupEntry{chatID, t, h.readyAtSeq})
	for pkt := range h.chatPackets[chatID] {
		heap.Fix(h, pkt.heapIndex)
	}
}

// nextAction cleans up expired readyAt entries, then returns
// the next packet to send or the duration to wait.
// Returns (packet, 0) if a packet is ready — the packet is popped.
// Returns (nil, wait) if the top packet is rate-limited.
// Returns (nil, 0) if the heap is empty.
func (h *packetHeap) nextAction(now time.Time) (*outgoingPacket, time.Duration) {
	for h.cleanup.Len() > 0 {
		entry := h.cleanup[0]
		if entry.readyAt.After(now) {
			break
		}
		heap.Pop(&h.cleanup)
		if val := h.maxReadyAt[entry.chatID]; val.seq == entry.seq {
			delete(h.maxReadyAt, entry.chatID)
			for pkt := range h.chatPackets[entry.chatID] {
				heap.Fix(h, pkt.heapIndex)
			}
		}
	}
	if h.Len() == 0 {
		return nil, 0
	}
	top := h.packets[0]
	if wait := h.maxReadyAt[top.message.chatID()].t.Sub(now); wait > 0 {
		return nil, wait
	}
	pkt := heap.Pop(h).(*outgoingPacket)
	return pkt, 0
}

func (w *worker) enqueueMessage(priority db.Priority, endpoint string, msg sendable, kind db.PacketKind) {
	select {
	case w.outgoingMsgCh <- outgoingPacket{
		message:     msg,
		endpoint:    endpoint,
		requestedAt: time.Now(),
		kind:        kind,
		priority:    priority,
	}:
	default:
		lerr("the outgoing message queue is full")
	}
}

func (w *worker) sender() {
	h := packetHeap{
		maxReadyAt:  map[int64]readyAtValue{},
		chatPackets: map[int64]map[*outgoingPacket]struct{}{},
	}
	var waitFor *time.Timer
	for {
		var timerC <-chan time.Time
		if waitFor != nil {
			timerC = waitFor.C
		}

		select {
		case pkt := <-w.outgoingMsgCh:
			if h.Len() >= maxHeapLen {
				lerr("the outgoing message heap is full")
			} else {
				heap.Push(&h, &pkt)
			}
		case <-timerC:
		}

		if waitFor != nil {
			waitFor.Stop()
			waitFor = nil
		}

	send:
		for {
			// Drain pending packets so the heap can prioritize across the full batch
		drain:
			for {
				if h.Len() >= maxHeapLen {
					break drain
				}
				select {
				case pkt := <-w.outgoingMsgCh:
					heap.Push(&h, &pkt)
				default:
					break drain
				}
			}

			now := time.Now()
			pkt, wait := h.nextAction(now)
			if pkt == nil {
				if wait > 0 {
					waitFor = time.NewTimer(wait)
				}
				break send
			}

			chatID := pkt.message.chatID()
		resend:
			for {
				now = time.Now()
				result := w.sendMessageInternal(pkt.endpoint, pkt.message)
				// time.Now() — cooldown starts after the send completes
				h.setReadyAt(chatID, time.Now().Add(time.Second))
				latency := int(time.Since(pkt.requestedAt).Milliseconds())
				w.outgoingMsgResults <- msgSendResult{
					priority:  pkt.priority,
					timestamp: int(now.Unix()),
					result:    result,
					endpoint:  pkt.endpoint,
					chatID:    chatID,
					latency:   latency,
					kind:      pkt.kind,
				}
				switch result {
				case messageTimeout:
					time.Sleep(1000 * time.Millisecond)
					continue resend
				case messageUnknownNetworkError:
					time.Sleep(1000 * time.Millisecond)
					continue resend
				case messageTooManyRequests:
					time.Sleep(8000 * time.Millisecond)
					continue resend
				case messageNoPhotoRights:
					if p, ok := pkt.message.(*photoParams); ok {
						heap.Push(&h, &outgoingPacket{
							message:     p.toText(),
							endpoint:    pkt.endpoint,
							requestedAt: pkt.requestedAt,
							kind:        pkt.kind,
							priority:    pkt.priority,
						})
					}
					time.Sleep(60 * time.Millisecond)
					break resend
				default:
					time.Sleep(60 * time.Millisecond)
					break resend
				}
			}
		}
	}
}
