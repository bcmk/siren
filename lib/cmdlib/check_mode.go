package cmdlib

// CheckMode  represents check mode
type CheckMode int

// Streamer statuses check mode
const (
	CheckOnline CheckMode = iota
	CheckStatuses
)
