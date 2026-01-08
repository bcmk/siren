// This program checks if a specific Stripchat model is online
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

var headers = [][2]string{
	{"authority", "stripchat.com"},
	{"dnt", "1"},
	{"upgrade-insecure-requests", "1"},
	{"user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Safari/537.36"},
	{"accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3"},
	{"sec-fetch-site", "none"},
	{"sec-fetch-mode", "navigate"},
	{"accept-language", "en-US,en;q=0.9"}}

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
	client := cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &checkers.StripchatChecker{}
	checker.Init(cmdlib.CheckerConfig{Clients: []*cmdlib.Client{client}, Headers: headers, Dbg: *verbose})
	fmt.Println(checker.CheckStatusSingle(modelID))
}
