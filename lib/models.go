package lib

import "regexp"

var ModelIDRegexp = regexp.MustCompile(`^[a-z0-9\-_]+$`)
