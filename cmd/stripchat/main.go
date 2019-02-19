package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/bcmk/siren/lib"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("usage: %s <model ID>\n", os.Args[0])
		return
	}
	modelID := os.Args[1]
	if !lib.ModelIDRegexp.MatchString(modelID) {
		fmt.Println("invalid model ID")
		return
	}
	client := &http.Client{CheckRedirect: lib.NoRedirect}
	fmt.Println(lib.CheckModelStripchat(client, modelID, false))
}
