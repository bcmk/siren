package cmdlib

import (
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
