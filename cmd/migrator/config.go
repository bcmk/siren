package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/viper"

	"github.com/bcmk/siren/v5/lib/cmdlib"
)

const configName = "migrator.json"

// botConfig is one bot's database. Each has its own, with its own owning role.
type botConfig struct {
	DSN  string `mapstructure:"dsn"`
	Role string `mapstructure:"role"`
}

type config struct {
	WorkMem string               `mapstructure:"work_mem"`
	Bots    map[string]botConfig `mapstructure:"bots"`
}

// readConfig loads migrator.json from ~/.config/siren, or from an explicit path.
func readConfig(path string) (config, error) {
	var cfg config
	if path == "" {
		var err error
		if path, err = cmdlib.FindConfig(configName); err != nil {
			return cfg, err
		}
	}

	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return cfg, fmt.Errorf("reading %q: %w", path, err)
	}
	if err := v.Unmarshal(&cfg, cmdlib.StrictConfigDecoder); err != nil {
		return cfg, fmt.Errorf("parsing %q: %w", path, err)
	}
	cmdlib.Linf("successfully read migrator config %q", path)
	return cfg, nil
}

// bot returns one bot's database settings, naming the configured bots when it has no such entry.
func (c config) bot(name string) (botConfig, error) {
	// viper lowercases every key it reads, so the file's own spelling never matches.
	bot, ok := c.Bots[strings.ToLower(name)]
	if !ok {
		return bot, fmt.Errorf("no bot %q, configured: %v", name, slices.Sorted(maps.Keys(c.Bots)))
	}
	if bot.DSN == "" {
		return bot, fmt.Errorf("configure a dsn for bot %q", name)
	}
	// The bot owns its tables and the login running this does not,
	// so without a role the objects it creates come out owned by the wrong one.
	if bot.Role == "" {
		return bot, fmt.Errorf("configure a role for bot %q", name)
	}
	return bot, nil
}
