package siren

import (
	"log"
	"net/http"
)

// StatusKind represents a status of a model
type StatusKind int

// Model statuses
const (
	StatusUnknown StatusKind = iota
	StatusOffline
	StatusOnline
	StatusNotFound
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

// Lerr logs an error
func Lerr(format string, v ...interface{}) { log.Printf("[ERROR] "+format, v...) }

// Linf logs an info message
func Linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }

// Ldbg logs a debug message
func Ldbg(format string, v ...interface{}) { log.Printf("[DEBUG] "+format, v...) }

// CheckErr panics on an error
func CheckErr(err error) {
	if err != nil {
		panic(err)
	}
}

// NoRedirect tells HTTP client to not to redirect
func NoRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
