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
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Printf("usage: %s <model ID>\n", os.Args[0])
		return
	}
	modelID := flag.Arg(0)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		fmt.Println("invalid model ID")
		return
	}
	client := &http.Client{CheckRedirect: lib.NoRedirect}
	fmt.Println(lib.CheckModelChaturbate(client, modelID, *verbose))
}
