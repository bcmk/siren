// generated by yamlenums -type=ParseKind; DO NOT EDIT

package cmdlib

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

var (
	_ParseKindNameToValue = map[string]ParseKind{
		"ParseRaw":      ParseRaw,
		"ParseHTML":     ParseHTML,
		"ParseMarkdown": ParseMarkdown,
	}

	_ParseKindValueToName = map[ParseKind]string{
		ParseRaw:      "ParseRaw",
		ParseHTML:     "ParseHTML",
		ParseMarkdown: "ParseMarkdown",
	}
)

func init() {
	var v ParseKind
	if _, ok := interface{}(v).(fmt.Stringer); ok {
		_ParseKindNameToValue = map[string]ParseKind{
			interface{}(ParseRaw).(fmt.Stringer).String():      ParseRaw,
			interface{}(ParseHTML).(fmt.Stringer).String():     ParseHTML,
			interface{}(ParseMarkdown).(fmt.Stringer).String(): ParseMarkdown,
		}
	}
}

// MarshalYAML is generated so ParseKind satisfies yaml.Marshaler.
func (r ParseKind) MarshalYAML() ([]byte, error) {
	if s, ok := interface{}(r).(fmt.Stringer); ok {
		return yaml.Marshal(s.String())
	}
	s, ok := _ParseKindValueToName[r]
	if !ok {
		return nil, fmt.Errorf("invalid ParseKind: %d", r)
	}
	return yaml.Marshal(s)
}

// UnmarshalYAML is generated so ParseKind satisfies yaml.Unmarshaler.
func (r *ParseKind) UnmarshalYAML(unmarshal func(v interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("ParseKind should be a string")
	}
	v, ok := _ParseKindNameToValue[s]
	if !ok {
		return fmt.Errorf("invalid ParseKind %q", s)
	}
	*r = v
	return nil
}
