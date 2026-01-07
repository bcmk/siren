// This program prints out all Stripchat models that are currently online
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
var userID = flag.String("user_id", "", "your user_id")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *userID == "" {
		fmt.Println("specify user_id")
		return
	}
	client := cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies)
	checker := &checkers.StripchatChecker{}
	checker.Init(cmdlib.CheckerConfig{
		UsersOnlineEndpoints: []string{*endpoint},
		Clients:              []*cmdlib.Client{client},
		SpecificConfig:       map[string]string{"user_id": *userID},
		Dbg:                  *verbose,
	})
	models, images, err := checker.CheckStatusesMany(cmdlib.AllModels, cmdlib.CheckOnline)
	if err != nil {
		fmt.Printf("error occurred: %v\n", err)
		return
	}
	for model := range models {
		fmt.Printf("%s %s\n", model, images[model])
	}
}
