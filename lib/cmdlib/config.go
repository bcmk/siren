package cmdlib

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// StrictConfigDecoder configures a viper Unmarshal to reject unknown keys
// and apply the standard config decode-hook chain: comma-split strings
// into slices, duration strings, RFC3339 strings into time.Time,
// encoding.TextUnmarshaler types, and JSON strings into maps (the last
// few let XRN_ env vars carry slice/map/duration/time values).
func StrictConfigDecoder(dc *mapstructure.DecoderConfig) {
	dc.ErrorUnused = true
	dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
		StringToSliceHookFunc(","),
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToTimeHookFunc(time.RFC3339),
		mapstructure.TextUnmarshallerHookFunc(),
		StringToMapHookFunc(),
	)
}

// BindEnvForConfig walks cfg's mapstructure tags and binds each resulting
// dotted key to v so AutomaticEnv picks up env overrides without an
// explicit list. cfg must be a pointer to a struct.
func BindEnvForConfig(v *viper.Viper, cfg any) {
	bindEnvForStructType(v, reflect.TypeOf(cfg), "")
}

func bindEnvForStructType(v *viper.Viper, t reflect.Type, prefix string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name, opts, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
			squash := slices.Contains(strings.Split(opts, ","), "squash")
			if name == "-" || (name == "" && !squash) {
				continue
			}
			key := prefix
			if !squash {
				if key != "" {
					key += "."
				}
				key += name
			}
			bindEnvForStructType(v, f.Type, key)
		}
	case reflect.Map:
		// Maps are file-only: there's no sane single-env-var
		// representation, so leave them unbound.
	default:
		_ = v.BindEnv(prefix)
	}
}

// ConfigDirs lists where FindConfig looks, in order,
// so a usage message can name the directories this machine actually searches
// rather than describe them.
// The working directory is not among them: these files carry production credentials,
// and a repo checkout is a working directory.
func ConfigDirs() []string {
	var dirs []string
	addDistinctDir := func(d string) {
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	// $XDG_CONFIG_HOME where it is set, and ~/Library/Application Support on macOS.
	if cfg, err := os.UserConfigDir(); err == nil {
		addDistinctDir(filepath.Join(cfg, "siren"))
	}
	// macOS looks in ~/.config too, where Linux has already been.
	if home, err := os.UserHomeDir(); err == nil {
		addDistinctDir(filepath.Join(home, ".config", "siren"))
	}
	return dirs
}

// FindConfig returns the path to name in the first config directory holding it.
// Callers take an explicit path for anywhere else.
func FindConfig(name string) (string, error) {
	dirs := ConfigDirs()
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("config %q not found in %v", name, dirs)
}
