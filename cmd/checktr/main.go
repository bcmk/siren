package main

import (
	"fmt"
	"os"

	"github.com/bcmk/siren/lib"
)

func main() {
	lib.LoadEndpointTranslations(os.Args[1:])
	fmt.Println("OK")
}
