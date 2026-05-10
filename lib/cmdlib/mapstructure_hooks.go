package cmdlib

import (
	"encoding/json"
	"reflect"
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
