package main

import (
	"errors"
	"io/fs"
	"log"
	"strings"
	"time"

	"github.com/bcmk/siren/v2/internal/botconfig"
	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config defaults. MaxSnapshotSize bounds the in-memory online set; if it's
// ever exceeded the session ends with an error and reconnects, rebuilding
// from a fresh bulk (defensive against runaway upstream protocol drift).
// HTTPResponseLimitBytes caps the body size fetchBounded reads from MFC.
const (
	defaultMaxSnapshotSize        = 20000
	defaultHTTPResponseLimitBytes = 8 * 1024 * 1024
)

// config is the daemon's runtime configuration. Loading mirrors the bot:
// JSON file plus optional dev override, with XRN_-prefixed env overrides.
type config struct {
	Debug                  bool          `mapstructure:"debug"`
	Trace                  bool          `mapstructure:"trace"`
	ListenAddress          string        `mapstructure:"listen_address"`
	TimeoutSeconds         int           `mapstructure:"timeout_seconds"`
	SourceIPAddress        string        `mapstructure:"source_ip_address"`
	MaxSnapshotSize        int           `mapstructure:"max_snapshot_size"`
	HTTPResponseLimitBytes int           `mapstructure:"http_response_limit_bytes"`
	WSConnectTimeout       time.Duration `mapstructure:"ws_connect_timeout"`
	WSIdleTimeout          time.Duration `mapstructure:"ws_idle_timeout"`
	ReconnectBackoffMax    time.Duration `mapstructure:"reconnect_backoff_max"`
	ExtDataFetchTimeout    time.Duration `mapstructure:"extdata_fetch_timeout"`
	LookupTTL              time.Duration `mapstructure:"lookup_ttl"`
	NameCacheTTL           time.Duration `mapstructure:"name_cache_ttl"`
}

var (
	cfgPath     = pflag.StringP("config", "c", "", "path to a config file (overrides default search)")
	daemonMode  = pflag.Bool("daemon", false, "run as a long-lived HTTP service instead of fetching once and exiting")
	onceTimeout = pflag.Duration("once-timeout", 20*time.Second, "overall deadline for once-mode (dial through bulk apply); 0 disables")
)

func readConfig() *config {
	pflag.Parse()

	type cfgFile struct {
		name     string
		required bool
	}
	files := []cfgFile{
		{"adapter-mfc.json", false},
		{"adapter-mfc.dev.ignore.json", false},
	}
	if *cfgPath != "" {
		files = []cfgFile{{*cfgPath, true}}
	}

	v := viper.New()
	v.SetConfigType("json")
	for _, f := range files {
		v.SetConfigFile(f.name)
		if err := v.MergeInConfig(); err != nil {
			if errors.Is(err, fs.ErrNotExist) && !f.required {
				log.Printf("skip config %q", f.name)
				continue
			}
			log.Fatalf("error reading %q: %v", f.name, err)
		}
		log.Printf("successfully read config %q", f.name)
	}

	v.SetEnvPrefix("XRN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	cfg := &config{
		TimeoutSeconds:         10,
		MaxSnapshotSize:        defaultMaxSnapshotSize,
		HTTPResponseLimitBytes: defaultHTTPResponseLimitBytes,
		WSConnectTimeout:       30 * time.Second,
		WSIdleTimeout:          90 * time.Second,
		ReconnectBackoffMax:    time.Minute,
		ExtDataFetchTimeout:    30 * time.Second,
		LookupTTL:              5 * time.Minute,
		NameCacheTTL:           24 * time.Hour,
	}
	botconfig.BindEnvForConfig(v, cfg)
	cmdlib.CheckErr(v.Unmarshal(cfg, func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.TextUnmarshallerHookFunc(),
		)
	}))

	if *daemonMode && cfg.ListenAddress == "" {
		log.Fatal("configure listen_address")
	}
	if cfg.TimeoutSeconds <= 0 {
		log.Fatal("configure timeout_seconds")
	}
	if cfg.MaxSnapshotSize <= 0 {
		log.Fatal("configure max_snapshot_size")
	}
	if cfg.HTTPResponseLimitBytes <= 0 {
		log.Fatal("configure http_response_limit_bytes")
	}
	if cfg.WSConnectTimeout <= 0 {
		log.Fatal("configure ws_connect_timeout")
	}
	if cfg.WSIdleTimeout <= 0 {
		log.Fatal("configure ws_idle_timeout")
	}
	if cfg.ReconnectBackoffMax <= 0 {
		log.Fatal("configure reconnect_backoff_max")
	}
	if cfg.ExtDataFetchTimeout <= 0 {
		log.Fatal("configure extdata_fetch_timeout")
	}
	if cfg.LookupTTL <= 0 {
		log.Fatal("configure lookup_ttl")
	}
	if cfg.NameCacheTTL <= 0 {
		log.Fatal("configure name_cache_ttl")
	}
	return cfg
}
