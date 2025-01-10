// This program prints out all Chaturbate models that are currently online
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
var endpoint = flag.String("e", "", "online query endpoint")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	client := cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &checkers.ChaturbateChecker{}
	checker.Init(checker, cmdlib.CheckerConfig{UsersOnlineEndpoints: []string{*endpoint}, Clients: []*cmdlib.Client{client}, Dbg: *verbose})
	models, images, err := checker.CheckStatusesMany(cmdlib.AllModels, cmdlib.CheckOnline)
	if err != nil {
		fmt.Printf("error occurred: %v", err)
		return
	}
	for model := range models {
		fmt.Printf("%s %s\n", model, images[model])
	}
}
