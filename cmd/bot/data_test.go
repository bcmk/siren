package main

import (
	"database/sql"

	"github.com/bcmk/siren/lib"
	tg "github.com/bcmk/telegram-bot-api"
)

var testConfig = config{
	MaxModels:               3,
	AdminID:                 1,
	NotFoundThreshold:       2,
	BlockThreshold:          10,
	OfflineThresholdSeconds: 5,
}

var testTranslations = translations{
	Help:           &translation{Str: "Help", Parse: parseRaw},
	Online:         &translation{Str: "Online %s", Parse: parseRaw},
	Offline:        &translation{Str: "Offline %s", Parse: parseRaw},
	SyntaxAdd:      &translation{Str: "SyntaxAdd", Parse: parseRaw},
	SyntaxRemove:   &translation{Str: "SyntaxRemove", Parse: parseRaw},
	SyntaxFeedback: &translation{Str: "SyntaxFeedback", Parse: parseRaw},
	InvalidSymbols: &translation{Str: "InvalidSymbols", Parse: parseRaw},
	AlreadyAdded:   &translation{Str: "AlreadyAdded %s", Parse: parseRaw},
	YourMaxModels:  &translation{Str: "YourMaxModels %d", Parse: parseRaw},
	AddError:       &translation{Str: "AddError %s", Parse: parseRaw},
	ModelAdded:     &translation{Str: "ModelAdded %s", Parse: parseRaw},
	ModelNotInList: &translation{Str: "ModelNotInList %s", Parse: parseRaw},
	ModelRemoved:   &translation{Str: "ModelRemoved %s", Parse: parseRaw},
	Feedback:       &translation{Str: "Feedback", Parse: parseRaw},
	Social:         &translation{Str: "Social", Parse: parseRaw},
	UnknownCommand: &translation{Str: "UnknownCommand", Parse: parseRaw},
	Slash:          &translation{Str: "Slash", Parse: parseRaw},
	Languages:      &translation{Str: "Languages", Parse: parseRaw},
	Version:        &translation{Str: "Version %s", Parse: parseRaw},
	ProfileRemoved: &translation{Str: "ProfileRemoved %s", Parse: parseRaw},
	NoModels:       &translation{Str: "NoModels", Parse: parseRaw},
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
	db, err := sql.Open("sqlite3", ":memory:")
	checkErr(err)
	w := &testWorker{
		worker: worker{
			bots:    nil,
			db:      db,
			cfg:     &testConfig,
			clients: nil,
			tr:      map[string]translations{"test": testTranslations},
		},
	}
	w.checkModel = w.testCheckModel
	w.senders = map[string]func(msg tg.Chattable) (tg.Message, error){"ep1": w.testSend, "ep2": w.testSend}
	return w
}
