package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bcmk/siren/lib"
	"gopkg.in/yaml.v3"
)

var print = flag.Bool("p", false, "print translations")
var printKeys = flag.Bool("k", false, "print keys")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	_, raw := lib.LoadEndpointTranslations(flag.Args())
	if *print {
		bytes, err := yaml.Marshal(raw)
		lib.CheckErr(err)
		fmt.Println(string(bytes))
	} else if *printKeys {
		for _, t := range raw {
			fmt.Println(t.Key)
		}
	} else {
		fmt.Println("OK")
	}
}
