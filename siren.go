package siren

import "log"

type StatusKind int

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

func Lerr(format string, v ...interface{}) { log.Printf("[ERROR] "+format, v...) }
func Linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }
func Ldbg(format string, v ...interface{}) { log.Printf("[DEBUG] "+format, v...) }

func CheckErr(err error) {
	if err != nil {
		panic(err)
	}
}
