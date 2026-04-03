// This program prints out all streamers that are currently online
package main

import (
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/bcmk/siren/v2/internal/checkers"
	"github.com/bcmk/siren/v2/lib/cmdlib"
)

var verbose = flag.Bool("v", false, "verbose output")
var timeout = flag.Int("t", 10, "timeout in seconds")
var address = flag.String("a", "", "source IP address")
var cookies = flag.Bool("c", false, "use cookies")
var endpoints = cmdlib.StringSetFlag{}
var userID = flag.String("user_id", "", "Stripchat user_id")

var sites = map[string]cmdlib.Checker{
	"bongacams":  &checkers.BongaCamsChecker{},
	"cam4":       &checkers.Cam4Checker{},
	"camsoda":    &checkers.CamSodaChecker{},
	"chaturbate": &checkers.ChaturbateChecker{},
	"flirt4free": &checkers.Flirt4FreeChecker{},
	"livejasmin": &checkers.LiveJasminChecker{},
	"streamate":  &checkers.StreamateChecker{},
	"stripchat":  &checkers.StripchatChecker{},
}

var streamateDefaultEndpoint = "http://affiliate.streamate.com/SMLive/SMLResult.xml"

func toSlice(xs map[string]bool) []string {
	var result []string
	for s := range xs {
		result = append(result, s)
	}
	return result
}

func main() {
	siteNames := strings.Join(slices.Sorted(maps.Keys(sites)), ", ")
	flag.Var(&endpoints, "e", "online query endpoint (repeatable)")
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s [options] <site>\n\nsites: %s\n\n",
			os.Args[0],
			siteNames,
		)
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		return
	}
	site := flag.Arg(0)

	checker, ok := sites[site]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown site: %s\nsites: %s\n", site, siteNames)
		os.Exit(1)
	}

	endpointSlice := toSlice(endpoints)
	if len(endpointSlice) == 0 {
		switch site {
		case "streamate":
			endpointSlice = []string{streamateDefaultEndpoint}
		default:
			fmt.Fprintln(os.Stderr, "specify endpoint with -e")
			os.Exit(1)
		}
	}

	config := cmdlib.CheckerConfig{
		UsersOnlineEndpoints: endpointSlice,
		Clients: []*cmdlib.Client{
			cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies),
		},
		Dbg: *verbose,
	}

	switch site {
	case "stripchat":
		if *userID == "" {
			fmt.Println("specify user_id")
			return
		}
		config.SpecificConfig = map[string]cmdlib.Secret{
			"user_id": cmdlib.Secret(*userID),
		}
	}

	checker.Init(config)
	streamers, err := checker.QueryOnlineStreamers()
	if err != nil {
		fmt.Printf("error occurred: %v\n", err)
		return
	}
	for model, info := range streamers {
		fmt.Printf("%s %s\n", model, info.ImageURL)
	}
}
