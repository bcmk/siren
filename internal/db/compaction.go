package db

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	hour = 60 * 60
	day  = 24 * hour
)

// compactionStep is a rule's step: its band, begin to end
type compactionStep struct {
	rule  ShortOfflineRule
	begin int // included
	end   int // excluded
}

// Compaction is what compactStatusChanges did:
// each rule's step, the rows deleted and each step's delete time
type Compaction struct {
	steps    []compactionStep
	deleted  int64
	deleteMs []int
}

// compactStatusChanges makes the compactor's steps, docs/status-changes.md's Compaction, in tx.
// tx holds status_changes_lock, and rules ascend in both age and threshold.
// oldest is the oldest row's timestamp, or now with no rows.
func (d *Database) compactStatusChanges(
	ctx context.Context,
	tx pgx.Tx,
	rules []ShortOfflineRule,
	stepSeconds int,
	oldest int,
	now int,
) (Compaction, error) {
	var c Compaction
	coverages, lastRun, err := d.takeCompactionCoverages(ctx, tx)
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
		deleted, err := d.deleteShortOfflinePeriods(ctx, tx, rule.ShorterThan, begin, end)
		if err != nil {
			return c, err
		}
		c.deleted += deleted
		c.deleteMs = append(c.deleteMs, int(time.Since(deleteStart).Milliseconds()))
		if err := d.storeCompactionCoverage(ctx, tx, rule, base, end, latest); err != nil {
			return c, err
		}
		c.steps = append(c.steps, compactionStep{rule, begin, end})
	}
	return c, nil
}

// queueFinishedChunks queues the chunks the steps finished, as chunksToVacuum picks them, in tx
func (d *Database) queueFinishedChunks(
	ctx context.Context,
	tx pgx.Tx,
	steps []compactionStep,
	vacuumDelay int,
) error {
	chunks, err := d.statusChangesChunks(ctx, tx)
	if err != nil {
		return err
	}
	return d.queueStatusChangesVacuums(ctx, tx, chunksToVacuum(steps, chunks, vacuumDelay))
}

// ends returns each step's end
func (c Compaction) ends() []int {
	ends := make([]int, len(c.steps))
	for i, s := range c.steps {
		ends[i] = s.end
	}
	return ends
}

// lags returns each step's lag behind its rule's age before now, in seconds, zero once caught up
func (c Compaction) lags(now int) []int {
	lags := make([]int, len(c.steps))
	for i, s := range c.steps {
		lags[i] = max(0, now-s.rule.After-s.end)
	}
	return lags
}

// planStep plans rule i's step.
// base is where the coverage it stores begins:
// the later of the oldest row and the next rule's age before latest, the latest run's time.
// begin to end is its band, at most a step long and ending by the rule's age before now,
// or empty when begin is past it.
func planStep(
	rules []ShortOfflineRule,
	i int,
	coverages []compactionCoverage,
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
		if c.shorterThan >= rule.ShorterThan && c.begin <= base {
			begin = max(begin, c.end)
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
func chunksToVacuum(steps []compactionStep, chunks []statusChangesChunk, vacuumDelay int) []statusChangesVacuum {
	var vacuums []statusChangesVacuum
	for _, c := range chunks {
		finished := false
		v := statusChangesVacuum{chunk: c.name}
		for _, s := range steps {
			// A deleted period's start lies at most a threshold before its band
			if due := c.end + s.rule.ShorterThan + vacuumDelay; s.begin < due && due <= s.end {
				finished = true
				v.rewrite = v.rewrite || s.rule.Rewrite
			}
		}
		if finished {
			vacuums = append(vacuums, v)
		}
	}
	return vacuums
}

// RunCompaction makes one compactor run, docs/status-changes.md's "A run",
// and returns its compaction
func (d *Database) RunCompaction(
	ctx context.Context,
	stepSeconds int,
	rules []ShortOfflineRule,
	vacuumDelay int,
	now int,
) (Compaction, error) {
	start := time.Now()
	// One chunk a run, before the deletes, so the newest dead rows in it are a period old
	rewrites := slices.ContainsFunc(rules, func(r ShortOfflineRule) bool { return r.Rewrite })
	vacuumed, err := d.vacuumNextStatusChangesChunk(ctx, rewrites)
	if err != nil {
		return Compaction{}, err
	}
	compactionStart := time.Now()
	// Before the transaction: the lookup locks every chunk, and so for the statement alone
	oldest, err := d.oldestStatusChange(ctx, now)
	if err != nil {
		return Compaction{}, err
	}
	tx, err := d.begin()
	if err != nil {
		return Compaction{}, err
	}
	// Its own context: the run's may be done, and the connection must leave the transaction
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := d.lockStatusChanges(ctx, tx); err != nil {
		return Compaction{}, err
	}
	c, err := d.compactStatusChanges(ctx, tx, rules, stepSeconds, oldest, now)
	if err != nil {
		return Compaction{}, err
	}
	if err := d.queueFinishedChunks(ctx, tx, c.steps, vacuumDelay); err != nil {
		return Compaction{}, err
	}
	// From here on its own context, so a run the server committed logs its row on a shutdown too
	if err := tx.Commit(context.Background()); err != nil {
		return Compaction{}, err
	}
	queued, err := d.queuedStatusChangesVacuums(context.Background())
	if err != nil {
		return Compaction{}, err
	}
	data := map[string]any{
		"deleted":         c.deleted,
		"ends":            c.ends(),
		"lag_s":           c.lags(now),
		"compaction_ms":   int(time.Since(compactionStart).Milliseconds()),
		"delete_ms":       c.deleteMs,
		"vacuumed_chunks": vacuumed,
		"queued_chunks":   queued,
	}
	// Only a chunk vacuumed is timed, so the median is a vacuum's;
	// a held chunk's wait shows in the run's duration alone
	if vacuumed > 0 {
		data["vacuum_ms"] = int(compactionStart.Sub(start).Milliseconds())
	}
	d.LogPerformance(now, PerformanceLogCompaction, int(time.Since(start).Milliseconds()), data)
	lags := c.lags(now)
	// A caught-up run that deleted nothing has nothing to say
	if c.deleted > 0 || slices.Max(lags) > 0 {
		linf("status changes compacted: lag = %s, deleted = %d", formatLags(lags), c.deleted)
	}
	return c, nil
}

// formatLags writes lags in seconds as days, hours, minutes and seconds, like [59d3h59m0s 0 0]
func formatLags(lags []int) string {
	parts := make([]string, len(lags))
	for i, lag := range lags {
		parts[i] = "0"
		if lag > 0 {
			parts[i] = fmt.Sprintf("%dd%dh%dm%ds", lag/day, lag%day/hour, lag%hour/60, lag%60)
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}
