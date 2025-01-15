// This program migrates the database to the latest version
package main

import (
	"flag"

	"github.com/bcmk/siren/internal/botconfig"
	"github.com/bcmk/siren/internal/db"
	"github.com/bcmk/siren/lib/cmdlib"
)

var linf = cmdlib.Linf

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		panic("usage: migrator <config>")
	}
	cfg := botconfig.ReadConfig(args[0])

	db := db.NewDatabase(cfg.DBPath, cfg.CheckGID)

	linf("creating database if needed...")
	for _, prelude := range cfg.SQLPrelude {
		db.MustExec(prelude)
	}
	db.MustExec(`create table if not exists schema_version (version integer);`)
	db.ApplyMigrations()
}
