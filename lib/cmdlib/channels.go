package cmdlib

import (
	"regexp"
	"strings"
)

// CommonChannelIDRegexp is a regular expression to check channel IDs
var CommonChannelIDRegexp = regexp.MustCompile(`^[a-z0-9\-_@]+$`)

// CanonicalChannelID preprocesses channel ID string to canonical form
func CanonicalChannelID(name string) string {
	return strings.ToLower(name)
}
