// This program prints out all Twitch streams that are currently online
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
var clientID = flag.String("id", "", "your client id")
var secret = flag.String("secret", "", "your client secret")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	client := cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &checkers.TwitchChecker{}
	checker.Init(nil, cmdlib.CheckerConfig{
		UsersOnlineEndpoints: []string{""},
		Clients:              []*cmdlib.Client{client},
		Dbg:                  *verbose,
		SpecificConfig: map[string]string{
			"client_id":     *clientID,
			"client_secret": *secret,
		}})
	models, images, err := checker.CheckStatusesMany(cmdlib.AllModels, cmdlib.CheckOnline)
	if err != nil {
		fmt.Printf("error occurred: %v\n", err)
		return
	}
	for model := range models {
		fmt.Printf("%s %s\n", model, images[model])
	}
}
