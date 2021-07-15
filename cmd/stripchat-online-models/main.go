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
var endpoint = flag.String("e", "", "online query endpoint")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	client := lib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &lib.StripchatChecker{}
	checker.Init(checker, lib.CheckerConfig{UsersOnlineEndpoints: []string{*endpoint}, Clients: []*lib.Client{client}, Dbg: *verbose})
	models, images, err := checker.CheckStatusesMany(nil, lib.CheckOnline)
	if err != nil {
		fmt.Printf("error occurred: %v", err)
		return
	}
	for model := range models {
		fmt.Printf("%s %s\n", model, images[model])
	}
}
