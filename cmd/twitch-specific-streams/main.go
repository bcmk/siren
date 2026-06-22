// This program prints out specified Twitch channels that are currently online
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bcmk/siren/v3/internal/checkers"
	"github.com/bcmk/siren/v3/lib/cmdlib"
)

var verbose = flag.Bool("v", false, "verbose output")
var checkerCfgPath = flag.String("checker-config", "", "path to twitch-checker.json (overrides default search)")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s [options] <channel>...\n\n"+
				"Per-site checker settings (HTTP timeout, secrets, endpoint URLs)\n"+
				"are read from twitch-checker.json, searched in the current directory,\n"+
				"$XDG_CONFIG_HOME/siren/, and ~/.config/siren/.\n"+
				"Override the path with -checker-config.\n\n",
			os.Args[0],
		)
		flag.PrintDefaults()
	}
	flag.Parse()
	cmdlib.SetVerbosity(*verbose)
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	checker, err := checkers.New("twitch")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	caps := checker.Capabilities()
	if !caps.SupportsCLI {
		fmt.Fprintln(os.Stderr, "twitch is not supported in CLI tools")
		os.Exit(1)
	}
	if !caps.SupportsQueryFixedListOnlineStreamers {
		fmt.Fprintln(os.Stderr, "fixed-list online query is not supported for twitch")
		os.Exit(1)
	}
	if err := checker.Init(*checkerCfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	channels, err := checker.QueryFixedListOnlineStreamers(flag.Args(), cmdlib.CheckStatuses)
	if err != nil {
		fmt.Printf("error occurred: %v\n", err)
		return
	}
	for channel, info := range channels {
		fmt.Printf("%s %s\n", channel, info.ImageURL)
	}
}
