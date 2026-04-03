// This program checks if a specific streamer is online
package main

import (
	"errors"
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
var clientID = flag.String("id", "", "Twitch client id")
var secret = flag.String("secret", "", "Twitch client secret")
var psID = flag.String("ps_id", "", "LiveJasmin ps_id")
var accessKey = flag.String("access_key", "", "LiveJasmin access_key")

var sites = map[string]cmdlib.Checker{
	"bongacams":  &checkers.BongaCamsChecker{},
	"cam4":       &checkers.Cam4Checker{},
	"camsoda":    &checkers.CamSodaChecker{},
	"chaturbate": &checkers.ChaturbateChecker{},
	"flirt4free": &checkers.Flirt4FreeChecker{},
	"livejasmin": &checkers.LiveJasminChecker{},
	"streamate":  &checkers.StreamateChecker{},
	"stripchat":  &checkers.StripchatChecker{},
	"twitch":     &checkers.TwitchChecker{},
}

func main() {
	siteNames := strings.Join(slices.Sorted(maps.Keys(sites)), ", ")
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s [options] <site> <model ID>\n\nsites: %s\n\n",
			os.Args[0],
			siteNames,
		)
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		return
	}
	site := flag.Arg(0)
	modelID := flag.Arg(1)

	checker, ok := sites[site]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown site: %s\nsites: %s\n", site, siteNames)
		os.Exit(1)
	}

	config := cmdlib.CheckerConfig{
		Clients: []*cmdlib.Client{
			cmdlib.HTTPClientWithTimeoutAndAddress(*timeout, *address, *cookies),
		},
		Dbg: *verbose,
	}

	switch site {
	case "twitch":
		if *clientID == "" {
			fmt.Println("specify id")
			return
		}
		if *secret == "" {
			fmt.Println("specify secret")
			return
		}
		config.SpecificConfig = map[string]cmdlib.Secret{
			"client_id":     cmdlib.Secret(*clientID),
			"client_secret": cmdlib.Secret(*secret),
		}
	case "livejasmin":
		if *psID == "" {
			fmt.Println("specify ps_id")
			return
		}
		if *accessKey == "" {
			fmt.Println("specify access_key")
			return
		}
		config.SpecificConfig = map[string]cmdlib.Secret{
			"ps_id":      cmdlib.Secret(*psID),
			"access_key": cmdlib.Secret(*accessKey),
		}
	}

	checker.Init(config)
	modelID = checker.NicknamePreprocessing(modelID)
	if !checker.NicknameRegexp().MatchString(modelID) {
		fmt.Println("invalid model ID")
		return
	}
	status, err := checker.CheckStatusSingle(modelID)
	if errors.Is(err, cmdlib.ErrNotImplemented) {
		fmt.Fprintf(os.Stderr, "single check is not supported for %s\n", site)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(status)
}
