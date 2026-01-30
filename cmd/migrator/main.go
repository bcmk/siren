// This program migrates the database to the latest version
package main

import (
	"flag"

	"github.com/bcmk/siren/internal/db"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		panic("usage: migrator <dsn>")
	}

	db := db.NewDatabase(args[0], false)
	db.ApplyMigrations()
}
