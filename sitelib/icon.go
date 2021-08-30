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
	name := strings.TrimPrefix(s, "^")
	*i = Icon{
		Name:    name,
		Enabled: len(name) == len(s),
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
