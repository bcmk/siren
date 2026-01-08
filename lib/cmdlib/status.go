package cmdlib

import "strings"

// StatusKind represents a status of a channel
type StatusKind int

// Channel statuses
const (
	StatusUnknown  StatusKind = 0
	StatusOffline  StatusKind = 1
	StatusOnline   StatusKind = 2
	StatusNotFound StatusKind = 4
	StatusDenied   StatusKind = 8
)

func (s StatusKind) String() string {
	if s == StatusUnknown || s == StatusOffline|StatusOnline|StatusNotFound|StatusDenied {
		return "unknown"
	}
	var words []string
	if s&StatusOffline != 0 {
		words = append(words, "offline")
	}
	if s&StatusOnline != 0 {
		words = append(words, "online")
	}
	if s&StatusNotFound != 0 {
		words = append(words, "not found")
	}
	if s&StatusDenied != 0 {
		words = append(words, "denied")
	}
	return strings.Join(words, "|")
}
