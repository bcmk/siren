package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/bcmk/siren"
)

func main() {
	modelID := os.Args[1]
	client := &http.Client{CheckRedirect: siren.NoRedirect}
	fmt.Println(siren.CheckModelBongaCams(client, modelID, false))
}
