package lib

import "regexp"

// ModelIDRegexp is a regular expression to check model IDs
var ModelIDRegexp = regexp.MustCompile(`^[a-z0-9\-_@]+$`)

// StatusRequest represents a request of model status
type StatusRequest struct {
	KnownModels  map[string]StatusKind
	ModelsToPoll []string
}

// StatusUpdate represents an update of model status
type StatusUpdate struct {
	ModelID string
	Status  StatusKind
	Image   string
}
