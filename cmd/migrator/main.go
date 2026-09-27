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
	botCfgPath := pflag.String("bot-config", "", "read the database from this bot config instead of migrator.json")
	workMem := pflag.String("work-mem", "", "sort and index-build memory for a prebuild migration, e.g. 128MB")
	pflag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s [options] <bot>\n"+
				"       %s [options] --bot-config <path>\n\n"+
				"The first form reads the bot's database and owning role from migrator.json in\n"+
				"%s.\n"+
				"Override the path with --config.\n"+
				"The second reads the database from a bot config and needs no role.\n\n",
			os.Args[0],
			os.Args[0],
			strings.Join(cmdlib.ConfigDirs(), " or "),
		)
		pflag.PrintDefaults()
	}
	pflag.Parse()

	var database db.Database
	if *botCfgPath != "" {
		if pflag.NArg() != 0 || *cfgPath != "" {
			fmt.Fprintln(os.Stderr, "--bot-config takes neither <bot> nor --config")
			pflag.Usage()
			os.Exit(2)
		}
		// The bot's own login owns its tables, so it needs no role.
		dsn, err := readBotDSN(*botCfgPath)
		cmdlib.CheckErr(err)
		database = db.NewDatabase(dsn, false, 0)
	} else {
		if pflag.NArg() != 1 {
			pflag.Usage()
			os.Exit(2)
		}
		cfg, err := readConfig(*cfgPath)
		cmdlib.CheckErr(err)
		bot, err := cfg.bot(pflag.Arg(0))
		cmdlib.CheckErr(err)
		if *workMem == "" {
			*workMem = cfg.WorkMem
		}
		database = db.NewDatabase(bot.DSN, false, 0)
		database.SetRole(bot.Role)
	}

	if !*prebuild {
		database.ApplyMigrations()
		return
	}
	database.Throttle()
	if *workMem != "" {
		database.SetWorkMem(*workMem)
	}
	database.ApplyNextPrebuildMigrations()
}
