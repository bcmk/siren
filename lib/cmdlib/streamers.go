package cmdlib

import (
	"regexp"
	"strings"
)

// CommonStreamerIDRegexp is a regular expression to check streamer IDs
var CommonStreamerIDRegexp = regexp.MustCompile(`^[a-z0-9\-_@]+$`)

// CanonicalStreamerID preprocesses streamer ID string to canonical form
func CanonicalStreamerID(name string) string {
	return strings.ToLower(name)
}
