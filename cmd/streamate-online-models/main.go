// This program prints out all Streamate models that are currently online
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

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	client := cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &checkers.StreamateChecker{}
	checker.Init(nil, cmdlib.CheckerConfig{
		UsersOnlineEndpoints: []string{"http://affiliate.streamate.com/SMLive/SMLResult.xml"},
		Clients:              []*cmdlib.Client{client},
		Dbg:                  *verbose})
	models, images, err := checker.CheckStatusesMany(cmdlib.AllModels, cmdlib.CheckOnline)

	if err != nil {
		fmt.Printf("error occurred: %v\n", err)
		return
	}
	for model := range models {
		fmt.Printf("%s %s\n", model, images[model])
	}
}
