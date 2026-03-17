package cmdlib

import (
	"regexp"
	"strings"
)

// CommonNicknameRegexp is a regular expression to check nicknames
var CommonNicknameRegexp = regexp.MustCompile(`^[a-z0-9\-_@]+$`)

// CanonicalNicknamePreprocessing preprocesses nickname to canonical form
func CanonicalNicknamePreprocessing(name string) string {
	return strings.ToLower(name)
}
