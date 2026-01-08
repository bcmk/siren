// This program checks if a specific LiveJasmin model is online
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
var psID = flag.String("ps_id", "", "your ps_id")
var accessKey = flag.String("access_key", "", "your access_key")

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
	if !cmdlib.CommonChannelIDRegexp.MatchString(modelID) {
		fmt.Println("invalid model ID")
		return
	}
	if *psID == "" {
		fmt.Println("specify ps_id")
		return
	}
	if *accessKey == "" {
		fmt.Println("specify access_key")
		return
	}
	client := cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &checkers.LiveJasminChecker{}
	checker.Init(cmdlib.CheckerConfig{
		Clients:        []*cmdlib.Client{client},
		Dbg:            *verbose,
		SpecificConfig: map[string]string{"ps_id": *psID, "access_key": *accessKey}})
	fmt.Println(checker.CheckStatusSingle(modelID))
}
