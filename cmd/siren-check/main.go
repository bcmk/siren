// This program checks if a specific streamer is online
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bcmk/siren/v6/internal/checkers"
	"github.com/bcmk/siren/v6/lib/cmdlib"
)

var verbose = flag.Bool("v", false, "verbose output")
var checkerCfgPath = flag.String("checker-config", "", "path to <site>-checker.json (overrides default search)")

func main() {
	os.Exit(run())
}

// run checks one streamer and returns the exit status
func run() int {
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s [options] <site> <model ID>\n\n"+
				"sites: %s\n\n"+
				"Per-site checker settings (HTTP timeout, secrets, endpoint URLs)\n"+
				"are read from <site>-checker.json in\n"+
				"%s.\n"+
				"Override the path with -checker-config.\n\n",
			os.Args[0],
			strings.Join(checkers.CLISites(), ", "),
			strings.Join(cmdlib.ConfigDirs(), " or "),
		)
		flag.PrintDefaults()
	}
	flag.Parse()
	cmdlib.SetVerbosity(*verbose)
	if flag.NArg() != 2 {
		flag.Usage()
		return 2
	}
	site := flag.Arg(0)
	modelID := flag.Arg(1)

	checker, err := checkers.New(site)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	caps := checker.Capabilities()
	if !caps.SupportsCLI {
		fmt.Fprintf(os.Stderr, "%s is not supported in CLI tools\n", site)
		return 1
	}
	if !caps.SupportsQueryStreamer {
		fmt.Fprintf(os.Stderr, "single check is not supported for %s\n", site)
		return 1
	}
	if err := checker.Init(*checkerCfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	modelID = checker.NicknamePreprocessing(modelID)
	if !checker.NicknameRegexp().MatchString(modelID) {
		fmt.Fprintln(os.Stderr, "invalid model ID")
		return 1
	}
	info, err := checker.QueryStreamer(modelID, checkers.ExtraTimes)
	// The status is valid regardless of the error
	fmt.Println(info.Status)
	if info.ImageURL != "" {
		fmt.Println(info.ImageURL)
	}
	if info.ShowKind != cmdlib.ShowUnknown {
		line := showKindText(info.ShowKind)
		if info.ShowKindSince != nil {
			line += " since " + info.ShowKindSince.Local().Format(time.DateTime+" MST")
		}
		fmt.Println(line)
	}
	if info.Viewers != nil {
		fmt.Printf("viewers: %d\n", *info.Viewers)
	}
	if info.Subject != "" {
		fmt.Printf("subject: %s\n", info.Subject)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if !caps.SupportsQueryStreamerTimes {
		return 0
	}
	switch info.Status {
	case cmdlib.StatusOnline:
		if info.WentOnlineAt != nil {
			fmt.Printf("online since %s\n", info.WentOnlineAt.Local().Format(time.DateTime+" MST"))
		}
	case cmdlib.StatusOffline:
		// An away room is still open, so it has no end time yet
		if info.WentOfflineAt == nil && info.WentOnlineAt == nil && info.ShowKind != cmdlib.ShowAway {
			fmt.Println("last seen online: no data")
		}
		if info.WentOfflineAt != nil {
			fmt.Printf("last seen online: %s\n", info.WentOfflineAt.Local().Format(time.DateTime+" MST"))
		}
		if info.WentOnlineAt != nil {
			fmt.Printf("last went online: %s\n", info.WentOnlineAt.Local().Format(time.DateTime+" MST"))
		}
	}
	return 0
}

// showKindText words the show kind as the bot does, e.g. "in a private show" or "away"
func showKindText(kind cmdlib.ShowKind) string {
	if kind == cmdlib.ShowAway {
		return "away"
	}
	return fmt.Sprintf("in a %s show", kind)
}
