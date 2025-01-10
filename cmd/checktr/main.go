// This program checks translations
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bcmk/siren/lib/cmdlib"
	"gopkg.in/yaml.v3"
)

var printTranslations = flag.Bool("p", false, "print translations")
var printKeys = flag.Bool("k", false, "print keys")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	_, raw := cmdlib.LoadEndpointTranslations(flag.Args())
	if *printTranslations {
		bytes, err := yaml.Marshal(raw)
		cmdlib.CheckErr(err)
		fmt.Println(string(bytes))
	} else if *printKeys {
		for _, t := range raw {
			fmt.Println(t.Key)
		}
	} else {
		fmt.Println("OK")
	}
}
