package cmdlib

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-viper/mapstructure/v2"
)

// StringToMapHookFunc decodes a JSON string into a map field. Lets env
// vars supply structured map values.
func StringToMapHookFunc() mapstructure.DecodeHookFunc {
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() == reflect.String && to.Kind() == reflect.Map {
			if s := data.(string); s != "" {
				m := reflect.New(to).Interface()
				if err := json.Unmarshal([]byte(s), m); err != nil {
					return data, err
				}
				return reflect.ValueOf(m).Elem().Interface(), nil
			}
		}
		return data, nil
	}
}

// IntegerHookFunc decodes into an integer type only a base 10 string or a whole number that fits,
// where mapstructure reads "010" as octal, "0x10" as hex, "" as 0, true as 1 and 1.5 as 1
func IntegerHookFunc() mapstructure.DecodeHookFunc {
	return func(from, to reflect.Type, data any) (any, error) {
		var signed bool
		switch to.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			signed = true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		default:
			return data, nil
		}
		switch from.Kind() {
		case reflect.String:
			s := reflect.ValueOf(data).String()
			if signed {
				n, err := strconv.ParseInt(s, 10, to.Bits())
				if err != nil || strconv.FormatInt(n, 10) != s {
					return data, fmt.Errorf("%q is not a base 10 integer that fits %s", s, to)
				}
				return n, nil
			}
			n, err := strconv.ParseUint(s, 10, to.Bits())
			if err != nil || strconv.FormatUint(n, 10) != s {
				return data, fmt.Errorf("%q is not a base 10 integer that fits %s", s, to)
			}
			return n, nil
		case reflect.Float32, reflect.Float64:
			f := reflect.ValueOf(data).Float()
			if signed {
				if f != math.Trunc(f) || f < math.MinInt64 || f >= math.MaxInt64 || reflect.Zero(to).OverflowInt(int64(f)) {
					return data, fmt.Errorf("%v is not a whole number that fits %s", f, to)
				}
				return int64(f), nil
			}
			if f != math.Trunc(f) || f < 0 || f >= math.MaxUint64 || reflect.Zero(to).OverflowUint(uint64(f)) {
				return data, fmt.Errorf("%v is not a whole number that fits %s", f, to)
			}
			return uint64(f), nil
		case reflect.Bool:
			return data, fmt.Errorf("%v is not an integer", data)
		}
		return data, nil
	}
}

// StringToSliceHookFunc decodes a sep-split string into a slice field.
// Lets env vars supply list values.
func StringToSliceHookFunc(sep string) mapstructure.DecodeHookFunc {
	return func(f, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}
		if t.Kind() != reflect.Slice {
			return data, nil
		}

		raw := data.(string)
		if raw == "" {
			return []string{}, nil
		}

		result := strings.Split(raw, sep)
		for k, v := range result {
			result[k] = strings.TrimLeft(v, " ")
		}
		return result, nil
	}
}
