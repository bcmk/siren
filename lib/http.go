package siren

import (
	"net/http"
)

// NoRedirect tells HTTP client to not to redirect
func NoRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
