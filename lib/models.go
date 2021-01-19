package lib

import (
	"regexp"
	"strings"
)

// ModelIDRegexp is a regular expression to check model IDs
var ModelIDRegexp = regexp.MustCompile(`^[a-z0-9\-_@]+$`)

// StatusRequest represents a request of model status
type StatusRequest struct {
	SpecialModels []string
}

// OnlineModel represents an update of model status
type OnlineModel struct {
	ModelID string
	Image   string
}

// CanonicalModelID preprocesses model ID string to canonical form
func CanonicalModelID(name string) string {
	return strings.ToLower(name)
}
