package db

import (
	"reflect"
	"testing"
)

// TestChunksToVacuum pins the vacuums a run queues: a chunk once a band passes its end
// by the rule's threshold plus the delay, rewritten when a rule finishing it says so
func TestChunksToVacuum(t *testing.T) {
	t.Parallel()
	chunks := []statusChangesChunk{{name: "a", start: 0, end: 7 * day}, {name: "b", start: 7 * day, end: 14 * day}}
	rule := ShortOfflineRule{After: 7 * day, ShorterThan: 900}
	rewriting := ShortOfflineRule{After: 60 * day, ShorterThan: 3600, Rewrite: true}
	tests := []struct {
		name  string
		steps []compactionStep
		delay int
		want  []statusChangesVacuum
	}{
		{"short of the end", []compactionStep{{rule, 7*day - 1000, 7*day + 899}}, 0, nil},
		{"at the end", []compactionStep{{rule, 7*day - 1000, 7*day + 900}}, 0, []statusChangesVacuum{{chunk: "a"}}},
		{"short of the delay", []compactionStep{{rule, 7*day - 1000, 7*day + 2699}}, 1800, nil},
		{"past the delay", []compactionStep{{rule, 7*day - 1000, 7*day + 2700}}, 1800, []statusChangesVacuum{{chunk: "a"}}},
		// An earlier step queued it
		{"begun past the end", []compactionStep{{rule, 7*day + 900, 7*day + 2000}}, 0, nil},
		{"two ends", []compactionStep{{rule, 0, 14*day + 900}}, 0, []statusChangesVacuum{{chunk: "a"}, {chunk: "b"}}},
		{"a rewrite", []compactionStep{{rewriting, 0, 7*day + 3600}}, 0, []statusChangesVacuum{{chunk: "a", rewrite: true}}},
		{
			"two rules on one chunk",
			[]compactionStep{{rule, 0, 7*day + 900}, {rewriting, 0, 7*day + 3600}},
			0,
			[]statusChangesVacuum{{chunk: "a", rewrite: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := chunksToVacuum(tt.steps, chunks, tt.delay); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("chunksToVacuum = %v, want %v", got, tt.want)
			}
		})
	}
}
