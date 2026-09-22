// Package main implements compactor: a daemon that thins a bot's old status change history.
// Each run it deletes the short offline periods that just aged into a rule,
// as docs/status-changes.md defines them.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/pflag"

	"github.com/bcmk/siren/v5/internal/db"
	"github.com/bcmk/siren/v5/lib/cmdlib"
)

var linf = cmdlib.Linf

const (
	hour = 60 * 60
	day  = 24 * hour
)

func main() {
	version := pflag.BoolP("version", "v", false, "prints current version")
	cfgPath := pflag.StringP("config", "c", "", "path to the config file (required)")
	pflag.Parse()
	if *version {
		fmt.Println(cmdlib.Version)
		os.Exit(0)
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "--config is required")
		pflag.Usage()
		os.Exit(2)
	}
	cfg := readConfig(*cfgPath)

	cfgString, err := json.MarshalIndent(cfg, "", "    ")
	cmdlib.CheckErr(err)
	linf("compactor config: " + string(cfgString))
	rules := cfg.dbRules()
	d := db.NewDatabase(string(cfg.DSN), false, 0)
	d.Throttle()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	period := time.Duration(cfg.PeriodSeconds) * time.Second
	jitter := time.Duration(cfg.PeriodJitterSeconds) * time.Second
	// From the database's name: the same at every restart,
	// and apart between the compactors sharing a server
	vacuumDelay := vacuumDelayFor(d.CurrentDatabase(), cfg.VacuumDelayMaxSeconds)
	linf("vacuum delay picked: %d s", vacuumDelay)
	for {
		_, err := run(ctx, &d, cfg.StepSeconds, rules, vacuumDelay, int(time.Now().Unix()))
		switch {
		case errors.Is(err, db.ErrLockBusy) || errors.Is(err, db.ErrMigrating):
			linf("%v, skipping the run", err)
		case err != nil && ctx.Err() == nil:
			cmdlib.CheckErr(err)
		}
		// Up to the jitter either way, so the compactors sharing a server drift apart
		wait := period
		if jitter > 0 {
			wait += rand.N(2*jitter) - jitter
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		// A timer can be ready beside the cancel, and no run starts on a done context
		if ctx.Err() != nil {
			linf("shutting down")
			cmdlib.CheckErr(d.Close())
			return
		}
	}
}

// vacuumDelayFor hashes a database's name to a delay below delayMax
func vacuumDelayFor(database string, delayMax int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(database))
	return int(h.Sum32() % uint32(delayMax))
}

// run makes one compactor run, docs/status-changes.md's "A run", and returns its compaction
func run(
	ctx context.Context,
	d *db.Database,
	stepSeconds int,
	rules []db.ShortOfflineRule,
	vacuumDelay int,
	now int,
) (compaction, error) {
	start := time.Now()
	// One chunk a run, before the deletes, so the newest dead rows in it are a period old
	rewrites := slices.ContainsFunc(rules, func(r db.ShortOfflineRule) bool { return r.Rewrite })
	vacuumed, err := d.VacuumNextStatusChangesChunk(ctx, rewrites)
	if err != nil {
		return compaction{}, err
	}
	compactionStart := time.Now()
	// Before the transaction: the lookup locks every chunk, and so for the statement alone
	oldest, err := d.OldestStatusChange(ctx, now)
	if err != nil {
		return compaction{}, err
	}
	tx, err := d.Begin()
	if err != nil {
		return compaction{}, err
	}
	// Its own context: the run's may be done, and the connection must leave the transaction
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := d.LockStatusChanges(ctx, tx); err != nil {
		return compaction{}, err
	}
	c, err := compactStatusChanges(ctx, d, tx, rules, stepSeconds, oldest, now)
	if err != nil {
		return compaction{}, err
	}
	if err := queueVacuums(ctx, d, tx, c.steps, vacuumDelay); err != nil {
		return compaction{}, err
	}
	// From here on its own context, so a run the server committed logs its row on a shutdown too
	if err := tx.Commit(context.Background()); err != nil {
		return compaction{}, err
	}
	queued, err := d.QueuedStatusChangesVacuums(context.Background())
	if err != nil {
		return compaction{}, err
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
	d.LogPerformance(now, db.PerformanceLogCompaction, int(time.Since(start).Milliseconds()), data)
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
