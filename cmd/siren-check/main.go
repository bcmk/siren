// This program checks if a specific streamer is online
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
			"usage: %s [options] <site> <model ID>\n\n"+
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
	if flag.NArg() != 2 {
		flag.Usage()
		return
	}
	site := flag.Arg(0)
	modelID := flag.Arg(1)

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
	if !caps.SupportsQueryStatus {
		fmt.Fprintf(os.Stderr, "single check is not supported for %s\n", site)
		os.Exit(1)
	}
	if err := checker.Init(*checkerCfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	modelID = checker.NicknamePreprocessing(modelID)
	if !checker.NicknameRegexp().MatchString(modelID) {
		fmt.Println("invalid model ID")
		return
	}
	info, err := checker.QueryStatus(modelID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(info.Status)
	if info.ImageURL != "" {
		fmt.Println(info.ImageURL)
	}
	if info.ShowKind != cmdlib.ShowUnknown {
		fmt.Printf("show: %s\n", info.ShowKind)
	}
	if info.Viewers != nil {
		fmt.Printf("viewers: %d\n", *info.Viewers)
	}
	if info.Subject != "" {
		fmt.Printf("subject: %s\n", info.Subject)
	}
}
