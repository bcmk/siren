package main

import (
	"database/sql"

	"github.com/bcmk/siren/lib"
	tg "github.com/bcmk/telegram-bot-api"
)

var testConfig = config{
	MaxModels: 3,
	AdminID:   1,
	StatusConfirmationSeconds: statusConfirmationSeconds{
		Offline:  5,
		NotFound: 5,
	},
}

var testTranslations = lib.Translations{
	Help:           &lib.Translation{Str: "Help", Parse: lib.ParseRaw},
	Online:         &lib.Translation{Str: "Online %s", Parse: lib.ParseRaw},
	Offline:        &lib.Translation{Str: "Offline %s", Parse: lib.ParseRaw},
	SyntaxAdd:      &lib.Translation{Str: "SyntaxAdd", Parse: lib.ParseRaw},
	SyntaxRemove:   &lib.Translation{Str: "SyntaxRemove", Parse: lib.ParseRaw},
	SyntaxFeedback: &lib.Translation{Str: "SyntaxFeedback", Parse: lib.ParseRaw},
	InvalidSymbols: &lib.Translation{Str: "InvalidSymbols", Parse: lib.ParseRaw},
	AlreadyAdded:   &lib.Translation{Str: "AlreadyAdded %s", Parse: lib.ParseRaw},
	YourMaxModels:  &lib.Translation{Str: "YourMaxModels %d", Parse: lib.ParseRaw},
	AddError:       &lib.Translation{Str: "AddError %s", Parse: lib.ParseRaw},
	ModelAdded:     &lib.Translation{Str: "ModelAdded %s", Parse: lib.ParseRaw},
	ModelNotInList: &lib.Translation{Str: "ModelNotInList %s", Parse: lib.ParseRaw},
	ModelRemoved:   &lib.Translation{Str: "ModelRemoved %s", Parse: lib.ParseRaw},
	Feedback:       &lib.Translation{Str: "Feedback", Parse: lib.ParseRaw},
	Social:         &lib.Translation{Str: "Social", Parse: lib.ParseRaw},
	UnknownCommand: &lib.Translation{Str: "UnknownCommand", Parse: lib.ParseRaw},
	Languages:      &lib.Translation{Str: "Languages", Parse: lib.ParseRaw},
	Version:        &lib.Translation{Str: "Version %s", Parse: lib.ParseRaw},
	ProfileRemoved: &lib.Translation{Str: "ProfileRemoved %s", Parse: lib.ParseRaw},
}

type testWorker struct {
	worker
	status    lib.StatusKind
	message   tg.Message
	sendError error
}

func (w *testWorker) testCheckModel(*lib.Client, string, [][2]string, bool) lib.StatusKind {
	return w.status
}

func (w *testWorker) testSend(tg.Chattable) (tg.Message, error) {
	return w.message, w.sendError
}

func newTestWorker() *testWorker {
	db, err := sql.Open("sqlite3", "file:memdb1?mode=memory&cache=shared")
	checkErr(err)
	w := &testWorker{
		worker: worker{
			bots:    nil,
			db:      db,
			cfg:     &testConfig,
			clients: nil,
			tr:      map[string]*lib.Translations{"test": &testTranslations},
		},
	}
	w.checkModel = w.testCheckModel
	w.senders = map[string]func(msg tg.Chattable) (tg.Message, error){"ep1": w.testSend, "ep2": w.testSend}
	return w
}
