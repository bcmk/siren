package sitelib

import (
	"strings"
)

// Icon represents an icon
type Icon struct {
	Name    string
	Enabled bool
}

// UnmarshalYAML unmarshals an icon
func (i *Icon) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if strings.HasPrefix(s, "^") {
		*i = Icon{
			Name:    s[1:],
			Enabled: false,
		}
		return nil
	}
	*i = Icon{
		Name:    s,
		Enabled: true,
	}
	return nil
}

// MarshalYAML marshals an icon
func (i *Icon) MarshalYAML() (interface{}, error) {
	if !i.Enabled {
		return "^" + i.Name, nil
	}
	return i.Name, nil
}
