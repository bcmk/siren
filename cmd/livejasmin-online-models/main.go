// This program prints out all LiveJasmin models that are currently online
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bcmk/siren/internal/checkers"
	"github.com/bcmk/siren/lib/cmdlib"
)

var verbose = flag.Bool("v", false, "verbose output")
var timeout = flag.Int("t", 10, "timeout in seconds")
var address = flag.String("a", "", "source IP address")
var cookies = flag.Bool("c", false, "use cookies")
var endpoints = cmdlib.StringSetFlag{}

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
	client := cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &checkers.LiveJasminChecker{}
	checker.Init(cmdlib.CheckerConfig{UsersOnlineEndpoints: toSlice(endpoints), Clients: []*cmdlib.Client{client}, Dbg: *verbose})
	channels, err := checker.QueryOnlineChannels()
	if err != nil {
		fmt.Printf("error occurred: %v\n", err)
		return
	}
	for model, info := range channels {
		fmt.Printf("%s %s\n", model, info.ImageURL)
	}
}
