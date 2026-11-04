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
	"syscall"
	"time"

	"github.com/spf13/pflag"

	"github.com/bcmk/siren/v6/internal/db"
	"github.com/bcmk/siren/v6/lib/cmdlib"
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
		_, err := d.RunCompaction(ctx, cfg.StepSeconds, rules, vacuumDelay, int(time.Now().Unix()))
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
