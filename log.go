package main

import "log"

func lerr(format string, v ...interface{}) { log.Printf("[ERROR] "+format, v...) }
func linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }
