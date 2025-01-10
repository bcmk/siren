package cmdlib

import (
	"regexp"
	"strings"
)

// ModelIDRegexp is a regular expression to check model IDs
var ModelIDRegexp = regexp.MustCompile(`^[a-z0-9\-_@]+$`)

// CanonicalModelID preprocesses model ID string to canonical form
func CanonicalModelID(name string) string {
	return strings.ToLower(name)
}
