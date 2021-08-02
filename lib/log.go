package lib

import "log"

// VerbosityKind represents logging verbosity
type VerbosityKind int

// Verbosity constants
const (
	SilentVerbosity = 0
	ErrVerbosity    = 1
	InfVerbosity    = 2
	DbgVerbosity    = 3
)

// Verbosity is the current logging verbosity
var Verbosity VerbosityKind = DbgVerbosity

// Lerr logs an error
func Lerr(format string, v ...interface{}) {
	if Verbosity >= ErrVerbosity {
		log.Printf("[ERROR] "+format, v...)
	}
}

// Linf logs an info message
func Linf(format string, v ...interface{}) {
	if Verbosity >= InfVerbosity {
		log.Printf("[INFO] "+format, v...)
	}
}

// Ldbg logs a debug message
func Ldbg(format string, v ...interface{}) {
	if Verbosity >= DbgVerbosity {
		log.Printf("[DEBUG] "+format, v...)
	}
}
