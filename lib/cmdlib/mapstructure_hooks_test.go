package cmdlib

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

// TestStrictConfigDecoderIntegers pins the integers StrictConfigDecoder takes:
// strings in base 10 alone, and numbers only when whole and within the type
func TestStrictConfigDecoderIntegers(t *testing.T) {
	type port int
	type config struct {
		I int           `mapstructure:"i"`
		S int8          `mapstructure:"s"`
		U uint8         `mapstructure:"u"`
		P port          `mapstructure:"p"`
		D time.Duration `mapstructure:"d"`
	}
	tests := []struct {
		name    string
		key     string
		value   any
		want    config
		wantErr bool
	}{
		{"a base 10 string", "i", "10", config{I: 10}, false},
		{"a leading zero", "i", "010", config{}, true},
		{"hex", "i", "0x10", config{}, true},
		{"a plus sign", "i", "+5", config{}, true},
		{"an empty string", "i", "", config{}, true},
		{"a bool", "i", true, config{}, true},
		{"a string over the unsigned type", "u", "300", config{}, true},
		{"a leading zero unsigned", "u", "010", config{}, true},
		{"a named type", "p", "10", config{P: 10}, false},
		{"a leading zero in a named type", "p", "010", config{}, true},
		{"a whole number", "i", 10.0, config{I: 10}, false},
		{"a fraction", "i", 1.5, config{}, true},
		{"a number over the type", "i", 1e19, config{}, true},
		{"a number over a small type", "s", 300.0, config{}, true},
		{"a negative unsigned number", "u", -1.0, config{}, true},
		{"a number over the unsigned type", "u", 300.0, config{}, true},
		{"a duration string", "d", "1s", config{D: time.Second}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			v.Set(tt.key, tt.value)
			var got config
			err := v.Unmarshal(&got, StrictConfigDecoder)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("decoded %+v, want %+v", got, tt.want)
			}
		})
	}
}
