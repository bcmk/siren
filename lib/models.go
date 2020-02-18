package lib

import "regexp"

// ModelIDRegexp is a regular expression to check model IDs
var ModelIDRegexp = regexp.MustCompile(`^[a-z0-9\-_]+$`)

// StatusUpdate represents an update of model status
type StatusUpdate struct {
	ModelID string
	Status  StatusKind
}
