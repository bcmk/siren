package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/bcmk/siren/lib"
)

func main() {
	modelID := os.Args[1]
	client := &http.Client{CheckRedirect: lib.NoRedirect}
	fmt.Println(lib.CheckModelStripchat(client, modelID, false))
}
