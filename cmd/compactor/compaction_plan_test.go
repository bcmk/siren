package main

import (
	"reflect"
	"testing"

	"github.com/bcmk/siren/v6/internal/db"
)

// TestChunksToVacuum pins the vacuums a run queues: a chunk once a band passes its end
// by the rule's threshold plus the delay, rewritten when a rule finishing it says so
func TestChunksToVacuum(t *testing.T) {
	t.Parallel()
	chunks := []db.StatusChangesChunk{{Name: "a", Start: 0, End: 7 * day}, {Name: "b", Start: 7 * day, End: 14 * day}}
	rule := db.ShortOfflineRule{After: 7 * day, ShorterThan: 900}
	rewriting := db.ShortOfflineRule{After: 60 * day, ShorterThan: 3600, Rewrite: true}
	tests := []struct {
		name  string
		steps []step
		delay int
		want  []db.StatusChangesVacuum
	}{
		{"short of the end", []step{{rule, 7*day - 1000, 7*day + 899}}, 0, nil},
		{"at the end", []step{{rule, 7*day - 1000, 7*day + 900}}, 0, []db.StatusChangesVacuum{{Chunk: "a"}}},
		{"short of the delay", []step{{rule, 7*day - 1000, 7*day + 2699}}, 1800, nil},
		{"past the delay", []step{{rule, 7*day - 1000, 7*day + 2700}}, 1800, []db.StatusChangesVacuum{{Chunk: "a"}}},
		// An earlier step queued it
		{"begun past the end", []step{{rule, 7*day + 900, 7*day + 2000}}, 0, nil},
		{"two ends", []step{{rule, 0, 14*day + 900}}, 0, []db.StatusChangesVacuum{{Chunk: "a"}, {Chunk: "b"}}},
		{"a rewrite", []step{{rewriting, 0, 7*day + 3600}}, 0, []db.StatusChangesVacuum{{Chunk: "a", Rewrite: true}}},
		{
			"two rules on one chunk",
			[]step{{rule, 0, 7*day + 900}, {rewriting, 0, 7*day + 3600}},
			0,
			[]db.StatusChangesVacuum{{Chunk: "a", Rewrite: true}},
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
