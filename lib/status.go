package lib

// StatusKind represents a status of a model
type StatusKind int

// Model statuses
const (
	StatusUnknown StatusKind = iota
	StatusOffline
	StatusOnline
	StatusNotFound
	StatusDenied
)

func (s StatusKind) String() string {
	switch s {
	case StatusOffline:
		return "offline"
	case StatusOnline:
		return "online"
	case StatusNotFound:
		return "not found"
	}
	return "unknown"
}
