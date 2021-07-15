package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bcmk/siren/lib"
)

var verbose = flag.Bool("v", false, "verbose output")
var timeout = flag.Int("t", 10, "timeout in seconds")
var address = flag.String("a", "", "source IP address")
var cookies = flag.Bool("c", false, "use cookies")
var endpoints = lib.StringSetFlag{}

func toSlice(xs map[string]bool) []string {
	var result []string
	for s := range xs {
		result = append(result, s)
	}
	return result
}

func main() {
	flag.Var(&endpoints, "e", "online query endpoints")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	client := lib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &lib.LiveJasminChecker{}
	checker.Init(checker, lib.CheckerConfig{UsersOnlineEndpoints: toSlice(endpoints), Clients: []*lib.Client{client}, Dbg: *verbose})
	models, images, err := checker.CheckStatusesMany(nil, lib.CheckOnline)
	if err != nil {
		fmt.Printf("error occurred: %v", err)
		return
	}
	for model := range models {
		fmt.Printf("%s %s\n", model, images[model])
	}
}
