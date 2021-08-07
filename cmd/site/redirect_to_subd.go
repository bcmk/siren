package main

import (
	"net/http"
)

type redirectSubdHandler struct {
	subd string
	url  string
	code int
}

func newRedirectSubdHandler(subd, url string, code int) http.Handler {
	return &redirectSubdHandler{subd: subd, url: url, code: code}
}

func (rh *redirectSubdHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	url := *r.URL
	url.Host = rh.subd + "." + r.Host
	url.Path = rh.url
	http.Redirect(w, r, url.String(), rh.code)
}
