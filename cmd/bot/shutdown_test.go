package main

import (
	"context"
	"testing"
	"time"
)

// TestDrainIncomingStops checks the shutdown drain honors the deadline and
// finishes cleanly, the bound the old unbounded drain lacked. Neither case
// reads an update, so processTGUpdate never runs and no database is needed.
func TestDrainIncomingStops(t *testing.T) {
	tests := []struct {
		name  string
		setup func(cancel context.CancelFunc, botsDone chan struct{})
	}{
		{
			// Deadline already hit; the flush never signals done.
			name:  "returns when the deadline has passed",
			setup: func(cancel context.CancelFunc, _ chan struct{}) { cancel() },
		},
		{
			// Flush finished with an empty channel and time to spare.
			name:  "returns once the flush is done and nothing is left",
			setup: func(_ context.CancelFunc, botsDone chan struct{}) { close(botsDone) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &worker{}
			incoming := make(chan incomingPacket, 8)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			botsDone := make(chan struct{})
			tt.setup(cancel, botsDone)

			done := make(chan struct{})
			go func() {
				w.drainIncoming(ctx, incoming, botsDone)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("drainIncoming did not return")
			}
		})
	}
}
