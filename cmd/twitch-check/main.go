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
var clientID = flag.String("id", "", "your client id")
var secret = flag.String("secret", "", "your client secret")

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
	channel := flag.Arg(0)
	if !lib.ModelIDRegexp.MatchString(channel) {
		fmt.Println("invalid channel name")
		return
	}
	client := lib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := lib.TwitchChecker{}
	checker.Init(nil, []*lib.Client{client}, nil, *verbose, map[string]string{
		"client_id":     *clientID,
		"client_secret": *secret,
	})
	fmt.Println(checker.CheckSingle(channel))
}
