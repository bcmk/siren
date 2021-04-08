package lib

import "strings"

// StringSetFlag is a flag representing a set of strings
type StringSetFlag map[string]bool

// String implements flag.Value interface
func (s *StringSetFlag) String() string {
	var xs []string
	for i := range *s {
		xs = append(xs, i)
	}
	return strings.Join(xs, ", ")
}

// Set implements flag.Value interface
func (s *StringSetFlag) Set(value string) error {
	(*s)[value] = true
	return nil
}
