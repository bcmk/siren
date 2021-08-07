package main

import (
	"net/http"
	"strings"
)

func getParam(r *http.Request, key string) (string, bool) {
	params, ok := r.URL.Query()[key]
	if !ok || len(params) != 1 || len(params[0]) == 0 {
		return "", false
	}
	return strings.TrimSpace(params[0]), true
}

func getParamDict(keys []string, r *http.Request) map[string]string {
	paramMap := map[string]string{}
	for _, n := range keys {
		val, _ := getParam(r, n)
		paramMap[n] = val
	}
	return paramMap
}
