package main

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/bcmk/siren/v5/internal/db"
	"github.com/bcmk/siren/v5/lib/cmdlib"
)

// sweepDays is the sweep history's length: nine periods a day, 4 to 100 minutes long,
// one on each side of every threshold under test
const sweepDays = 100

// sweepOldest is the sweep history's first row, a day before its first period
func sweepOldest(now int) int { return now - (sweepDays+1)*day }

// sweepPeriods returns the sweep history's periods ending by now,
// their durations keyed by their end
func sweepPeriods(now int) map[int]int {
	periods := map[int]int{}
	for daysBack := sweepDays; daysBack >= 2; daysBack-- {
		for k, dur := range []int{240, 600, 780, 1020, 1500, 2400, 3000, 4500, 6000} {
			// Off the day grid, so no end sits exactly on a rule's age
			periods[now-daysBack*day+k*9000+60+dur] = dur
		}
	}
	return periods
}

// sweepHistory inserts a streamer with the sweep history ending at now
func sweepHistory(d *db.Database, nickname string, now int) {
	insertHistory(d, nickname, []compactionInsert{{sweepOldest(now), cmdlib.StatusOnline}})
	id := d.MustInt("select id from streamers where nickname = $1", nickname)
	periods := sweepPeriods(now)
	var ts, status, prev []int
	for _, end := range slices.Sorted(maps.Keys(periods)) {
		ts = append(ts, end-periods[end], end)
		status = append(status, int(cmdlib.StatusOffline), int(cmdlib.StatusOnline))
		prev = append(prev, int(cmdlib.StatusOnline), int(cmdlib.StatusOffline))
	}
	d.MustExec(`
		insert into status_changes (timestamp, streamer_id, status, prev_status)
		select unnest($1::int[]), $2, unnest($3::int[]), unnest($4::int[])`,
		ts, id, status, prev)
	insertHistory(d, nickname, []compactionInsert{{now - day, cmdlib.StatusOffline}, {now - 3600, cmdlib.StatusOnline}})
}

// periodEnds returns the ends of the streamer's remaining periods
func periodEnds(d *db.Database, streamerID int) map[int]bool {
	ends := map[int]bool{}
	for _, r := range streamerChanges(d, streamerID) {
		if r.status == cmdlib.StatusOnline && r.prevStatus == cmdlib.StatusOffline {
			ends[r.timestamp] = true
		}
	}
	return ends
}

// deletable says whether the rules delete a period of dur seconds ending at end, as of now
func deletable(rules []db.ShortOfflineRule, now, end, dur int) bool {
	for _, r := range rules {
		if now-end > r.After && dur < r.ShorterThan {
			return true
		}
	}
	return false
}

type sweepConfig struct {
	name  string
	rules []db.ShortOfflineRule
}

// sweepConfigs returns every reconfiguration of testRules under test:
// each rule's age kept, moved younger or older, times its threshold kept, lowered or raised,
// then each rule dropped, then a rule added at each position
func sweepConfigs() []sweepConfig {
	ages := [3][3]int{{7, 5, 10}, {30, 20, 45}, {60, 50, 90}}
	ageNames := [3]string{"", "younger", "older"}
	thrs := [3][3]int{{15, 11, 19}, {30, 21, 44}, {60, 46, 90}}
	thrNames := [3]string{"", "below", "above"}
	var configs []sweepConfig
	for a1 := range 3 {
		for t1 := range 3 {
			for a2 := range 3 {
				for t2 := range 3 {
					for a3 := range 3 {
						for t3 := range 3 {
							as, ts := [3]int{a1, a2, a3}, [3]int{t1, t2, t3}
							var rules []db.ShortOfflineRule
							var changes []string
							for i := range 3 {
								rules = append(rules, db.ShortOfflineRule{After: ages[i][as[i]] * day, ShorterThan: thrs[i][ts[i]] * 60})
								if as[i] != 0 || ts[i] != 0 {
									words := []string{fmt.Sprint(i + 1), ageNames[as[i]], thrNames[ts[i]]}
									changes = append(changes, strings.Join(slices.DeleteFunc(words, func(w string) bool { return w == "" }), " "))
								}
							}
							name := "unchanged"
							if len(changes) > 0 {
								name = strings.Join(changes, ", ")
							}
							configs = append(configs, sweepConfig{name, rules})
						}
					}
				}
			}
		}
	}
	for i := range 3 {
		configs = append(configs, sweepConfig{fmt.Sprintf("drop %d", i+1), slices.Delete(slices.Clone(testRules), i, i+1)})
	}
	for _, add := range []db.ShortOfflineRule{
		{After: 3 * day, ShorterThan: 5 * 60},
		{After: 15 * day, ShorterThan: 18 * 60},
		{After: 45 * day, ShorterThan: 40 * 60},
		{After: 90 * day, ShorterThan: 90 * 60},
	} {
		rules := append(slices.Clone(testRules), add)
		slices.SortFunc(rules, func(a, b db.ShortOfflineRule) int { return a.After - b.After })
		configs = append(configs, sweepConfig{fmt.Sprintf("add %d", add.After/day), rules})
	}
	return configs
}

// sweepVariant says how far testRules got before the new rules came, and how much later
type sweepVariant struct {
	name    string
	oldStep int
	elapsed int
}

var sweepVariants = []sweepVariant{
	{"caught up", 400 * day, 0},
	{"lagging", day, 0},
	{"caught up, two days on", 400 * day, 2 * day},
	{"lagging, two days on", day, 2 * day},
}

// sweepSteps are the new rules' steps: a short one first,
// then two long ones, the second finding nothing left
var sweepSteps = []int{day, 400 * day, 400 * day}

const sweepOldNow = 200 * day

// sweepCheck returns what went wrong across a reconfiguration,
// given the periods left after the old rules and after the new:
// the old rules deleted only what they may, the new rules deleted exactly what they must
func sweepCheck(
	periods map[int]int,
	afterOld map[int]bool,
	afterNew map[int]bool,
	rules []db.ShortOfflineRule,
	newNow int,
) []string {
	var violations []string
	for _, end := range slices.Sorted(maps.Keys(periods)) {
		dur := periods[end]
		switch must := deletable(rules, newNow, end, dur); {
		case !afterOld[end] && !deletable(testRules, sweepOldNow, end, dur):
			violations = append(violations,
				fmt.Sprintf("the old rules deleted the %d s period ending %d, which they must not", dur, end))
		case afterNew[end] && must:
			violations = append(violations,
				fmt.Sprintf("the %d s period ending %d stays, which the new rules must delete", dur, end))
		case !afterNew[end] && afterOld[end] && !must:
			violations = append(violations,
				fmt.Sprintf("the new rules deleted the %d s period ending %d, which they must not", dur, end))
		}
	}
	return violations
}

// simulateRun runs the rules' steps over the periods in memory, as planStep plans them,
// marking the periods gone and returning the coverages the run stores
func simulateRun(
	rules []db.ShortOfflineRule,
	coverages []db.CompactionCoverage,
	periods map[int]int,
	gone map[int]bool,
	now int,
	stepSeconds int,
) []db.CompactionCoverage {
	var stored []db.CompactionCoverage
	for i, rule := range rules {
		base, begin, end := planStep(rules, i, coverages, sweepOldest(sweepOldNow), now, now, stepSeconds)
		for e, dur := range periods {
			if begin <= e && e < end && dur < rule.ShorterThan {
				gone[e] = true
			}
		}
		stored = append(stored, db.CompactionCoverage{ShorterThan: rule.ShorterThan, Begin: base, End: end})
	}
	return stored
}

// simulateSweep runs a reconfiguration in memory and returns what went wrong,
// with mutate applied to the coverages every run reads
func simulateSweep(
	cfg sweepConfig,
	v sweepVariant,
	mutate func([]db.CompactionCoverage) []db.CompactionCoverage,
) []string {
	periods := sweepPeriods(sweepOldNow)
	remaining := func(gone map[int]bool) map[int]bool {
		left := map[int]bool{}
		for end := range periods {
			left[end] = !gone[end]
		}
		return left
	}
	gone := map[int]bool{}
	coverages := simulateRun(testRules, nil, periods, gone, sweepOldNow, v.oldStep)
	afterOld := remaining(gone)
	for _, stepSeconds := range sweepSteps {
		coverages = simulateRun(cfg.rules, mutate(coverages), periods, gone, sweepOldNow+v.elapsed, stepSeconds)
	}
	return sweepCheck(periods, afterOld, remaining(gone), cfg.rules, sweepOldNow+v.elapsed)
}

func keepCoverages(coverages []db.CompactionCoverage) []db.CompactionCoverage { return coverages }

// TestCompactStatusChangesPlanSweep runs every reconfiguration from sweepConfigs in memory,
// from testRules caught up or lagging, at once or two days later,
// and checks the new rules get through the whole history
func TestCompactStatusChangesPlanSweep(t *testing.T) {
	t.Parallel()
	for _, cfg := range sweepConfigs() {
		for _, v := range sweepVariants {
			t.Run(cfg.name+" "+v.name, func(t *testing.T) {
				if violations := simulateSweep(cfg, v, keepCoverages); len(violations) > 0 {
					t.Errorf("%d violations, first: %s", len(violations), violations[0])
				}
			})
		}
	}
}

// TestCompactStatusChangesPlanMutants pins the sweep's teeth:
// a plan trusting a coverage it must not fails the sweep
func TestCompactStatusChangesPlanMutants(t *testing.T) {
	t.Parallel()
	mutants := []struct {
		name   string
		mutate func([]db.CompactionCoverage) []db.CompactionCoverage
	}{
		{"trusts a coverage whatever it holds", func(cs []db.CompactionCoverage) []db.CompactionCoverage {
			cs = slices.Clone(cs)
			for i := range cs {
				cs[i].Begin = math.MinInt
			}
			return cs
		}},
		{"trusts a coverage whatever its threshold", func(cs []db.CompactionCoverage) []db.CompactionCoverage {
			cs = slices.Clone(cs)
			for i := range cs {
				cs[i].ShorterThan = math.MaxInt
			}
			return cs
		}},
	}
	for _, m := range mutants {
		t.Run(m.name, func(t *testing.T) {
			failing := 0
			for _, cfg := range sweepConfigs() {
				for _, v := range sweepVariants {
					if len(simulateSweep(cfg, v, m.mutate)) > 0 {
						failing++
					}
				}
			}
			if failing == 0 {
				t.Error("the sweep passes with this mutant")
			}
		})
	}
}

// runSweep runs a reconfiguration on the database and reports what went wrong
func runSweep(t *testing.T, d *db.Database, cfg sweepConfig, v sweepVariant) {
	t.Helper()
	d.MustExec("truncate status_changes, status_changes_lock")
	d.MustExec("delete from streamers")
	sweepHistory(d, "a", sweepOldNow)
	id := streamerID(t, d, "a")
	ctx := context.Background()
	// No vacuums, which other tests pin: each run's queue is cleared instead
	sweepRun := func(rules []db.ShortOfflineRule, stepSeconds, now int) {
		if _, err := run(ctx, d, stepSeconds, rules, 0, now); err != nil {
			t.Fatal(err)
		}
		d.MustExec("delete from status_changes_vacuum_queue")
	}
	sweepRun(testRules, v.oldStep, sweepOldNow)
	afterOld := periodEnds(d, id)
	for _, stepSeconds := range sweepSteps {
		sweepRun(cfg.rules, stepSeconds, sweepOldNow+v.elapsed)
	}
	afterNew := periodEnds(d, id)
	for _, violation := range sweepCheck(sweepPeriods(sweepOldNow), afterOld, afterNew, cfg.rules, sweepOldNow+v.elapsed) {
		t.Error(violation)
	}
	checkStatusChangesConsistent(t, d, "a")
}

// TestCompactStatusChangesSweep runs a sample of the reconfigurations on the database,
// where the delete statement and the stored coverages stand in for the simulation
func TestCompactStatusChangesSweep(t *testing.T) {
	t.Parallel()
	configs := sweepConfigs()
	// Every stride-th scenario of the age and threshold changes,
	// a prime stride spreading them over the configs and variants,
	// then every scenario of the few drops and adds after them
	const stride, workers = 59, 4
	reshapes := slices.IndexFunc(configs, func(c sweepConfig) bool { return strings.HasPrefix(c.name, "drop ") })
	var scenarios []int
	for i := 0; i < reshapes*len(sweepVariants); i += stride {
		scenarios = append(scenarios, i)
	}
	for i := reshapes * len(sweepVariants); i < len(configs)*len(sweepVariants); i++ {
		scenarios = append(scenarios, i)
	}
	for w := range workers {
		t.Run(fmt.Sprintf("worker %d", w), func(t *testing.T) {
			t.Parallel()
			tdb := newTestDB(t)
			defer tdb.terminate()
			for j := w; j < len(scenarios); j += workers {
				i := scenarios[j]
				cfg, v := configs[i/len(sweepVariants)], sweepVariants[i%len(sweepVariants)]
				t.Run(cfg.name+" "+v.name, func(t *testing.T) {
					runSweep(t, tdb.Database, cfg, v)
				})
			}
		})
	}
}
