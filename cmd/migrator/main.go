// This program migrates a bot's database to the latest version
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"github.com/bcmk/siren/v5/internal/db"
	"github.com/bcmk/siren/v5/lib/cmdlib"
)

func main() {
	prebuild := pflag.Bool("prebuild", false, "apply the next prebuild migrations instead, with the bot running")
	cfgPath := pflag.String("config", "", "path to migrator.json (overrides the default search)")
	workMem := pflag.String("work-mem", "", "sort and index-build memory for a prebuild migration, e.g. 128MB")
	pflag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s [options] <bot>\n\n"+
				"Each bot's database and owning role are read from migrator.json in\n"+
				"%s.\n"+
				"Override the path with --config.\n\n",
			os.Args[0],
			strings.Join(cmdlib.ConfigDirs(), " or "),
		)
		pflag.PrintDefaults()
	}
	pflag.Parse()
	if pflag.NArg() != 1 {
		pflag.Usage()
		os.Exit(2)
	}

	cfg, err := readConfig(*cfgPath)
	cmdlib.CheckErr(err)
	bot, err := cfg.bot(pflag.Arg(0))
	cmdlib.CheckErr(err)
	if *workMem != "" {
		cfg.WorkMem = *workMem
	}

	database := db.NewDatabase(bot.DSN, false, 0)
	database.SetRole(bot.Role)
	if !*prebuild {
		database.ApplyMigrations()
		return
	}
	database.Throttle()
	if cfg.WorkMem != "" {
		database.SetWorkMem(cfg.WorkMem)
	}
	database.ApplyNextPrebuildMigrations()
}
