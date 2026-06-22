// This program prints out all streamers that are currently online
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bcmk/siren/v3/internal/checkers"
	"github.com/bcmk/siren/v3/lib/cmdlib"
)

var verbose = flag.Bool("v", false, "verbose output")
var checkerCfgPath = flag.String("checker-config", "", "path to <site>-checker.json (overrides default search)")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s [options] <site>\n\n"+
				"sites: %s\n\n"+
				"Per-site checker settings (HTTP timeout, secrets, endpoint URLs)\n"+
				"are read from <site>-checker.json, searched in the current directory,\n"+
				"$XDG_CONFIG_HOME/siren/, and ~/.config/siren/.\n"+
				"Override the path with -checker-config.\n\n",
			os.Args[0],
			strings.Join(checkers.CLISites(), ", "),
		)
		flag.PrintDefaults()
	}
	flag.Parse()
	cmdlib.SetVerbosity(*verbose)
	if flag.NArg() != 1 {
		flag.Usage()
		return
	}
	site := flag.Arg(0)

	checker, err := checkers.New(site)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	caps := checker.Capabilities()
	if !caps.SupportsCLI {
		fmt.Fprintf(os.Stderr, "%s is not supported in CLI tools\n", site)
		os.Exit(1)
	}
	if !caps.SupportsQueryOnlineStreamers {
		fmt.Fprintf(os.Stderr, "online list is not supported for %s\n", site)
		os.Exit(1)
	}
	if err := checker.Init(*checkerCfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	streamers, err := checker.QueryOnlineStreamers()
	if err != nil {
		fmt.Printf("error occurred: %v\n", err)
		return
	}
	for model, info := range streamers {
		fmt.Printf("%s %s\n", model, info.ImageURL)
	}
}
