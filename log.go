package main

import "log"

func lerr(format string, v ...interface{}) { log.Printf("[ERROR] "+format, v...) }
