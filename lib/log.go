package lib

import "log"

// Lerr logs an error
func Lerr(format string, v ...interface{}) { log.Printf("[ERROR] "+format, v...) }

// Linf logs an info message
func Linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }

// Ldbg logs a debug message
func Ldbg(format string, v ...interface{}) { log.Printf("[DEBUG] "+format, v...) }
