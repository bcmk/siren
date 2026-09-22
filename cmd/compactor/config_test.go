package main

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bcmk/siren/v5/internal/db"
)

// clearEnv keeps the process environment out of the config under test:
// viper reads an empty variable as unset
func clearEnv(t *testing.T) {
	for _, kv := range os.Environ() {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(key, "XRN_COMPACTOR_") {
			t.Setenv(key, "")
		}
	}
}

// decodeWith decodes a file of a dsn and a 30 s period plus extra,
// with env the only XRN_COMPACTOR_ vars
func decodeWith(t *testing.T, extra string, env map[string]string) (*config, error) {
	t.Helper()
	clearEnv(t)
	for key, value := range env {
		t.Setenv(key, value)
	}
	path := filepath.Join(t.TempDir(), "compactor.json")
	if err := os.WriteFile(path, []byte(`{"dsn": "postgres://", "period_seconds": 30`+extra+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return decodeConfig(path)
}

// validConfig is a config checkConfig accepts, so a test can mangle one field and see it refused
func validConfig() *config {
	return &config{
		DSN:                   "postgres://",
		PeriodSeconds:         30,
		PeriodJitterSeconds:   10,
		StepSeconds:           1800,
		VacuumDelayMaxSeconds: 3600,
		ShortOfflineRules: []shortOfflineRule{
			{AfterDays: 7, ShorterThanMinutes: 15},
			{AfterDays: db.BotReachDays, ShorterThanMinutes: 30, Rewrite: true},
		},
	}
}

func TestCheckConfig(t *testing.T) {
	tests := []struct {
		name    string
		mangle  func(*config)
		wantErr bool
	}{
		{name: "complete", mangle: func(*config) {}},
		{name: "zero period_seconds", mangle: func(c *config) { c.PeriodSeconds = 0 }, wantErr: true},
		{name: "negative period_seconds", mangle: func(c *config) { c.PeriodSeconds = -1 }, wantErr: true},
		{name: "no jitter", mangle: func(c *config) { c.PeriodJitterSeconds = 0 }},
		{name: "negative jitter", mangle: func(c *config) { c.PeriodJitterSeconds = -1 }, wantErr: true},
		{name: "jitter of a whole period", mangle: func(c *config) { c.PeriodJitterSeconds = 30 }, wantErr: true},
		{name: "zero step_seconds", mangle: func(c *config) { c.StepSeconds = 0 }, wantErr: true},
		{name: "step_seconds not above period_seconds", mangle: func(c *config) { c.StepSeconds = 30 }, wantErr: true},
		{name: "no dsn", mangle: func(c *config) { c.DSN = "" }, wantErr: true},
		{name: "zero vacuum_delay_max_seconds", mangle: func(c *config) { c.VacuumDelayMaxSeconds = 0 }, wantErr: true},
		{name: "period over an integer", mangle: func(c *config) { c.PeriodSeconds = math.MaxInt32 + 1 }, wantErr: true},
		{name: "step over an integer", mangle: func(c *config) { c.StepSeconds = math.MaxInt32 + 1 }, wantErr: true},
		{
			name:    "vacuum delay over an integer",
			mangle:  func(c *config) { c.VacuumDelayMaxSeconds = math.MaxInt32 + 1 },
			wantErr: true,
		},
		{
			name:    "days over an integer of seconds",
			mangle:  func(c *config) { c.ShortOfflineRules[1].AfterDays = 24856 },
			wantErr: true,
		},
		{
			name:    "minutes over an integer of seconds",
			mangle:  func(c *config) { c.ShortOfflineRules[1].ShorterThanMinutes = 35791395 },
			wantErr: true,
		},
		{name: "one rule", mangle: func(c *config) { c.ShortOfflineRules = c.ShortOfflineRules[:1] }},
		{name: "no rules", mangle: func(c *config) { c.ShortOfflineRules = nil }, wantErr: true},
		{name: "zero days", mangle: func(c *config) { c.ShortOfflineRules[0].AfterDays = 0 }, wantErr: true},
		{name: "zero minutes", mangle: func(c *config) { c.ShortOfflineRules[0].ShorterThanMinutes = 0 }, wantErr: true},
		{
			name:    "days not ascending",
			mangle:  func(c *config) { c.ShortOfflineRules[1].AfterDays = 7 },
			wantErr: true,
		},
		{
			name:    "minutes not ascending",
			mangle:  func(c *config) { c.ShortOfflineRules[1].ShorterThanMinutes = 15 },
			wantErr: true,
		},
		{
			name:    "a rewrite within the bot's reads",
			mangle:  func(c *config) { c.ShortOfflineRules[1].AfterDays = db.BotReachDays - 1 },
			wantErr: true,
		},
		{
			name: "a rewrite on every rule past the bot's reads",
			mangle: func(c *config) {
				c.ShortOfflineRules[0] = shortOfflineRule{AfterDays: db.BotReachDays, ShorterThanMinutes: 15, Rewrite: true}
				c.ShortOfflineRules[1].AfterDays = db.BotReachDays + 1
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mangle(cfg)
			if err := checkConfig(cfg); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// TestReadConfigRules pins the rules' default, their file form,
// and the errors: an empty list fails the check instead of defaulting,
// as do a stray key, a stray key in a rule and bad JSON
func TestReadConfigRules(t *testing.T) {
	defaults := []shortOfflineRule{
		{AfterDays: 7, ShorterThanMinutes: 15},
		{AfterDays: 30, ShorterThanMinutes: 30},
		{AfterDays: 60, ShorterThanMinutes: 60, Rewrite: true},
	}
	tests := []struct {
		name    string
		rules   string
		want    []shortOfflineRule
		wantErr bool
	}{
		{name: "absent", want: defaults},
		{name: "empty", rules: `, "short_offline_rules": []`, wantErr: true},
		{name: "unknown key", rules: `, "rules": [{"after_days": 14, "shorter_than_minutes": 20}]`, wantErr: true},
		{
			name:    "unknown key in a rule",
			rules:   `, "short_offline_rules": [{"after_days": 14, "shorter_than_minutes": 20, "full": true}]`,
			wantErr: true,
		},
		{name: "malformed", rules: `, "short_offline_rules": [{"after_days": 14`, wantErr: true},
		{
			name: "configured",
			rules: `, "short_offline_rules": [` +
				`{"after_days": 14, "shorter_than_minutes": 20}, ` +
				`{"after_days": 45, "shorter_than_minutes": 50, "rewrite": true}]`,
			want: []shortOfflineRule{
				{AfterDays: 14, ShorterThanMinutes: 20},
				{AfterDays: 45, ShorterThanMinutes: 50, Rewrite: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := decodeWith(t, tt.rules, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if err == nil && !reflect.DeepEqual(cfg.ShortOfflineRules, tt.want) {
				t.Errorf("short_offline_rules = %v, want %v", cfg.ShortOfflineRules, tt.want)
			}
		})
	}
}

// TestDecodeConfigReadsIntegersInBase10 pins the integers' spelling in env vars:
// base 10 only, with no leading zero, so none reads as octal
func TestDecodeConfigReadsIntegersInBase10(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		wantPeriod int
		wantErr    bool
	}{
		{name: "an env var", env: map[string]string{"XRN_COMPACTOR_PERIOD_SECONDS": "100"}, wantPeriod: 100},
		{
			name:    "an env var with a leading zero",
			env:     map[string]string{"XRN_COMPACTOR_PERIOD_SECONDS": "0100"},
			wantErr: true,
		},
		{name: "a hex env var", env: map[string]string{"XRN_COMPACTOR_PERIOD_SECONDS": "0x100"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := decodeWith(t, "", tt.env)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if err == nil && cfg.PeriodSeconds != tt.wantPeriod {
				t.Errorf("period_seconds = %d, want %d", cfg.PeriodSeconds, tt.wantPeriod)
			}
		})
	}
}

// TestDecodeConfigDefaults pins the defaults of the options a file may leave out
func TestDecodeConfigDefaults(t *testing.T) {
	cfg, err := decodeWith(t, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StepSeconds != 1800 || cfg.VacuumDelayMaxSeconds != 3600 {
		t.Errorf(
			"step_seconds = %d, vacuum_delay_max_seconds = %d, want 1800 and 3600",
			cfg.StepSeconds,
			cfg.VacuumDelayMaxSeconds)
	}
}

// TestDecodeConfigVacuumDelayMax pins vacuum_delay_max_seconds: an hour unless configured
func TestDecodeConfigVacuumDelayMax(t *testing.T) {
	tests := []struct {
		name   string
		config string
		env    map[string]string
		want   int
	}{
		{name: "absent", want: 3600},
		{name: "configured", config: `, "vacuum_delay_max_seconds": 7200`, want: 7200},
		{
			name:   "from the env",
			config: `, "vacuum_delay_max_seconds": 7200`,
			env:    map[string]string{"XRN_COMPACTOR_VACUUM_DELAY_MAX_SECONDS": "600"},
			want:   600,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := decodeWith(t, tt.config, tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.VacuumDelayMaxSeconds != tt.want {
				t.Errorf("vacuum_delay_max_seconds = %d, want %d", cfg.VacuumDelayMaxSeconds, tt.want)
			}
		})
	}
}

// TestDecodeConfigPeriodJitter pins period_jitter_seconds: a third of the period unless configured
func TestDecodeConfigPeriodJitter(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   int
	}{
		{name: "absent", want: 10},
		{name: "absent, with a period", config: `, "period_seconds": 60`, want: 20},
		{name: "none", config: `, "period_jitter_seconds": 0`, want: 0},
		{name: "configured", config: `, "period_jitter_seconds": 5`, want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := decodeWith(t, tt.config, nil)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PeriodJitterSeconds != tt.want {
				t.Errorf("period_jitter_seconds = %d, want %d", cfg.PeriodJitterSeconds, tt.want)
			}
		})
	}
}

// TestVacuumDelayFor pins the vacuum delay: the same for a database every time, below the maximum,
// and apart for another database
func TestVacuumDelayFor(t *testing.T) {
	tests := []struct {
		name     string
		database string
	}{
		{name: "cb", database: "prod_bot_cb"},
		{name: "sc", database: "prod_bot_sc"},
	}
	delays := map[int]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, again := vacuumDelayFor(tt.database, 7200), vacuumDelayFor(tt.database, 7200)
			if first != again || first < 0 || first >= 7200 {
				t.Errorf("delays %d and %d, want one below 7200", first, again)
			}
			delays[first] = true
		})
	}
	if len(delays) != len(tests) {
		t.Errorf("delays %v, want one per database", delays)
	}
}
