package cmdlib

import "log"

// VerbosityKind represents logging verbosity
type VerbosityKind int

// Verbosity constants
const (
	SilentVerbosity = 0
	ErrVerbosity    = 1
	InfVerbosity    = 2
	DbgVerbosity    = 3
	TraceVerbosity  = 4
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

// Ltrace logs a trace message — finest-grained level, intended for raw
// protocol payloads and similar high-volume diagnostic data.
func Ltrace(format string, v ...interface{}) {
	if Verbosity >= TraceVerbosity {
		log.Printf("[TRACE] "+format, v...)
	}
}
