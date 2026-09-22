package main

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/spf13/viper"

	"github.com/bcmk/siren/v5/internal/db"
	"github.com/bcmk/siren/v5/lib/cmdlib"
)

type config struct {
	DSN           cmdlib.Secret `mapstructure:"dsn"` // the bot's own login, which owns the tables
	PeriodSeconds int           `mapstructure:"period_seconds"`
	// PeriodJitterSeconds is how far a wait between runs strays from PeriodSeconds either way,
	// defaults to a third of it
	PeriodJitterSeconds int `mapstructure:"period_jitter_seconds"`
	// StepSeconds is the history a run works through, defaults to 1800
	StepSeconds int `mapstructure:"step_seconds"`
	// VacuumDelayMaxSeconds bounds the vacuum delay, in seconds of history, defaults to 3600
	VacuumDelayMaxSeconds int `mapstructure:"vacuum_delay_max_seconds"`
	// ShortOfflineRules ascend in both age and threshold,
	// defaulting to 7 days at 15 minutes, 30 at 30, and 60 at 60, which rewrites
	ShortOfflineRules []shortOfflineRule `mapstructure:"short_offline_rules"`
}

// shortOfflineRule says an offline period ending AfterDays ago goes
// when shorter than ShorterThanMinutes, and Rewrite rewrites a chunk once the rule is done with it
type shortOfflineRule struct {
	AfterDays          int  `mapstructure:"after_days"`
	ShorterThanMinutes int  `mapstructure:"shorter_than_minutes"`
	Rewrite            bool `mapstructure:"rewrite"`
}

// dbRules converts the rules to seconds
func (c *config) dbRules() []db.ShortOfflineRule {
	rules := make([]db.ShortOfflineRule, len(c.ShortOfflineRules))
	for i, r := range c.ShortOfflineRules {
		rules[i] = db.ShortOfflineRule{After: r.AfterDays * day, ShorterThan: r.ShorterThanMinutes * 60, Rewrite: r.Rewrite}
	}
	return rules
}

// readConfig loads the JSON config at path, with XRN_COMPACTOR_-prefixed env overrides
func readConfig(path string) *config {
	cfg, err := decodeConfig(path)
	cmdlib.CheckErr(err)
	return cfg
}

// decodeConfig reads the config at path and returns it with what checkConfig says
func decodeConfig(path string) (*config, error) {
	v := viper.New()
	v.SetConfigType("json")
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading %q: %w", path, err)
	}
	cmdlib.Linf("successfully read config %q", path)

	// Its own prefix, as the bot reads period_seconds too
	v.SetEnvPrefix("XRN_COMPACTOR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.SetDefault("short_offline_rules", []map[string]any{
		{"after_days": 7, "shorter_than_minutes": 15},
		{"after_days": 30, "shorter_than_minutes": 30},
		{"after_days": 60, "shorter_than_minutes": 60, "rewrite": true},
	})
	cfg := &config{StepSeconds: 30 * 60, VacuumDelayMaxSeconds: hour}
	cmdlib.BindEnvForConfig(v, cfg)
	if err := v.Unmarshal(cfg, cmdlib.StrictConfigDecoder); err != nil {
		return nil, err
	}
	if !v.IsSet("period_jitter_seconds") {
		cfg.PeriodJitterSeconds = cfg.PeriodSeconds / 3
	}
	return cfg, checkConfig(cfg)
}

func checkConfig(cfg *config) error {
	if cfg.DSN == "" {
		return errors.New("configure dsn")
	}
	if cfg.PeriodSeconds <= 0 {
		return errors.New("configure a positive period_seconds")
	}
	if cfg.PeriodJitterSeconds < 0 || cfg.PeriodJitterSeconds >= cfg.PeriodSeconds {
		return errors.New("configure period_jitter_seconds from 0 to below period_seconds")
	}
	// A lagging rule catches up only with a step above the period plus a run's duration, unknown here
	if cfg.StepSeconds <= cfg.PeriodSeconds {
		return errors.New("configure step_seconds above period_seconds")
	}
	if cfg.VacuumDelayMaxSeconds <= 0 {
		return errors.New("configure a positive vacuum_delay_max_seconds")
	}
	// They go to integer columns, the vacuum delay's 32-bit modulo and timers
	if cfg.PeriodSeconds > math.MaxInt32 || cfg.StepSeconds > math.MaxInt32 || cfg.VacuumDelayMaxSeconds > math.MaxInt32 {
		return errors.New("period_seconds, step_seconds and vacuum_delay_max_seconds must fit an integer")
	}
	if len(cfg.ShortOfflineRules) == 0 {
		return errors.New("configure short_offline_rules")
	}
	for i, r := range cfg.ShortOfflineRules {
		if r.AfterDays <= 0 || r.ShorterThanMinutes <= 0 {
			return errors.New("short_offline_rules: after_days and shorter_than_minutes must be positive")
		}
		// In seconds they go to PostgreSQL integer columns and parameters
		if r.AfterDays > math.MaxInt32/day || r.ShorterThanMinutes > math.MaxInt32/60 {
			return errors.New("short_offline_rules: after_days and shorter_than_minutes must fit an integer of seconds")
		}
		if i > 0 && (r.AfterDays <= cfg.ShortOfflineRules[i-1].AfterDays ||
			r.ShorterThanMinutes <= cfg.ShortOfflineRules[i-1].ShorterThanMinutes) {
			return errors.New("short_offline_rules: after_days and shorter_than_minutes must ascend")
		}
		if r.Rewrite && r.AfterDays < db.BotReachDays {
			return fmt.Errorf(
				"short_offline_rules: a rewrite needs after_days of %d or more, past the bot's reads",
				db.BotReachDays)
		}
	}
	return nil
}
