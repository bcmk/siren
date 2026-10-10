package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bcmk/siren/v6/internal/db"
)

// step is a rule's step: its band, begin to end
type step struct {
	rule  db.ShortOfflineRule
	begin int // included
	end   int // excluded
}

// compaction is what compactStatusChanges did:
// each rule's step, the rows deleted and each step's delete time
type compaction struct {
	steps    []step
	deleted  int64
	deleteMs []int
}

// compactStatusChanges makes the compactor's steps, docs/status-changes.md's Compaction,
// in tx, which holds status_changes_lock, for rules ascending in both age and threshold,
// from oldest, the oldest row's timestamp or now with no rows
func compactStatusChanges(
	ctx context.Context,
	d *db.Database,
	tx pgx.Tx,
	rules []db.ShortOfflineRule,
	stepSeconds int,
	oldest int,
	now int,
) (compaction, error) {
	var c compaction
	coverages, lastRun, err := d.TakeCompactionCoverages(ctx, tx)
	if err != nil {
		return c, err
	}
	// Bases never move back:
	// from a run before the last one they would land behind the coverages holding them.
	// The bands still end by now, so a clock jump ahead only delays compaction.
	latest := max(now, lastRun)

	for i, rule := range rules {
		base, begin, end := planStep(rules, i, coverages, oldest, latest, now, stepSeconds)
		deleteStart := time.Now()
		deleted, err := d.DeleteShortOfflinePeriods(ctx, tx, rule.ShorterThan, begin, end)
		if err != nil {
			return c, err
		}
		c.deleted += deleted
		c.deleteMs = append(c.deleteMs, int(time.Since(deleteStart).Milliseconds()))
		if err := d.StoreCompactionCoverage(ctx, tx, rule, base, end, latest); err != nil {
			return c, err
		}
		c.steps = append(c.steps, step{rule, begin, end})
	}
	return c, nil
}

// queueVacuums queues the chunks the steps finished, as chunksToVacuum picks them, in tx
func queueVacuums(ctx context.Context, d *db.Database, tx pgx.Tx, steps []step, vacuumDelay int) error {
	chunks, err := d.StatusChangesChunks(ctx, tx)
	if err != nil {
		return err
	}
	return d.QueueStatusChangesVacuums(ctx, tx, chunksToVacuum(steps, chunks, vacuumDelay))
}

// ends returns each step's end
func (c compaction) ends() []int {
	ends := make([]int, len(c.steps))
	for i, s := range c.steps {
		ends[i] = s.end
	}
	return ends
}

// lags returns each step's lag behind its rule's age before now, in seconds, zero once caught up
func (c compaction) lags(now int) []int {
	lags := make([]int, len(c.steps))
	for i, s := range c.steps {
		lags[i] = max(0, now-s.rule.After-s.end)
	}
	return lags
}

// planStep plans rule i's step from the coverages, the oldest row, the latest run's time,
// now and the step: where the coverage it stores begins, by the latest run's time,
// and its band, which ends by now
func planStep(
	rules []db.ShortOfflineRule,
	i int,
	coverages []db.CompactionCoverage,
	oldest, latest, now, stepSeconds int,
) (base, begin, end int) {
	rule := rules[i]
	base = oldest
	if i+1 < len(rules) {
		// The ends past the next rule's age are its, whose larger threshold subsumes this one's
		base = max(base, latest-rules[i+1].After)
	}
	// A coverage holding the base at a threshold at least the rule's is skipped:
	// its rule compacted what this one would
	begin = base
	for _, c := range coverages {
		if c.ShorterThan >= rule.ShorterThan && c.Begin <= base {
			begin = max(begin, c.End)
		}
	}
	// By now, as a run that ran ahead must not carry a rule past its age;
	// never backwards, as the history, or a raised age, can be younger than the rule
	end = max(begin, min(begin+stepSeconds, now-rule.After))
	return base, begin, end
}

// chunksToVacuum picks the chunks a run queues: those whose end a step's band passed
// by the rule's threshold plus vacuumDelay, so no later delete of the rule reaches them.
// A chunk a rewriting rule finished is rewritten.
func chunksToVacuum(steps []step, chunks []db.StatusChangesChunk, vacuumDelay int) []db.StatusChangesVacuum {
	var vacuums []db.StatusChangesVacuum
	for _, c := range chunks {
		finished := false
		v := db.StatusChangesVacuum{Chunk: c.Name}
		for _, s := range steps {
			// A deleted period's start lies at most a threshold before its band
			if due := c.End + s.rule.ShorterThan + vacuumDelay; s.begin < due && due <= s.end {
				finished = true
				v.Rewrite = v.Rewrite || s.rule.Rewrite
			}
		}
		if finished {
			vacuums = append(vacuums, v)
		}
	}
	return vacuums
}
