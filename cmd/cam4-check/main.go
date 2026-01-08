// This program checks if a specific CAM4 model is online
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
		fmt.Fprintf(os.Stderr, "usage: %s [options] <model ID>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		return
	}
	modelID := flag.Arg(0)
	modelID = checkers.Cam4CanonicalModelID(modelID)
	if !cmdlib.CommonChannelIDRegexp.MatchString(modelID) {
		fmt.Println("invalid model ID")
		return
	}
	client := cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &checkers.Cam4Checker{}
	checker.Init(cmdlib.CheckerConfig{Clients: []*cmdlib.Client{client}, Dbg: *verbose})
	fmt.Println(checker.CheckStatusSingle(modelID))
}
