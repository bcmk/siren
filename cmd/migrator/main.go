// This program migrates the database to the latest version
package main

import (
	"flag"

	"github.com/bcmk/siren/internal/db"
	"github.com/bcmk/siren/lib/cmdlib"
)

var linf = cmdlib.Linf

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		panic("usage: migrator <dsn>")
	}

	db := db.NewDatabase(args[0], false)
	linf("creating database if needed...")
	db.MustExec(`create table if not exists schema_version (version integer);`)
	db.ApplyMigrations()
}
