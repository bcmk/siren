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

// SetVerbosity sets the global verbosity from a boolean debug switch:
// debug level when true, info level otherwise.
func SetVerbosity(debug bool) {
	if debug {
		Verbosity = DbgVerbosity
	} else {
		Verbosity = InfVerbosity
	}
}

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

// Lfatalf logs a fatal error and terminates the process. It prints
// regardless of verbosity, then exits with a non-zero status.
func Lfatalf(format string, v ...interface{}) {
	log.Fatalf("[FATAL] "+format, v...)
}
