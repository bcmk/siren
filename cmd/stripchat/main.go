package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/bcmk/siren/lib"
)

var verbose = flag.Bool("v", false, "verbose output")

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
	if !lib.ModelIDRegexp.MatchString(modelID) {
		fmt.Println("invalid model ID")
		return
	}
	client := &http.Client{CheckRedirect: lib.NoRedirect}
	fmt.Println(lib.CheckModelStripchat(client, modelID, *verbose))
}
