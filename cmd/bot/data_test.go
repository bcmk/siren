package main

import (
	"database/sql"

	"github.com/bcmk/siren/lib"
)

var testConfig = config{
	CheckGID:  true,
	MaxModels: 3,
	AdminID:   1,
	StatusConfirmationSeconds: statusConfirmationSeconds{
		Offline:  5,
		NotFound: 5,
	},
	KeepStatusesForDays: 1,
	MaxCleanSeconds:     1000000,
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
}

func newTestWorker() *testWorker {
	db, err := sql.Open("sqlite3", "file:memdb1?mode=memory&cache=shared")
	checkErr(err)
	w := &testWorker{
		worker: worker{
			bots:            nil,
			db:              db,
			cfg:             &testConfig,
			clients:         nil,
			tr:              map[string]*lib.Translations{"test": &testTranslations},
			durations:       map[string]queryDurationsData{},
			lowPriorityMsg:  make(chan outgoingPacket, 10000),
			highPriorityMsg: make(chan outgoingPacket, 10000),
			mainGID:         gid(),
		},
	}
	return w
}

func (w *testWorker) chatsForModel(modelID string) (chats []int64, endpoints []string) {
	var chatID int64
	var endpoint string
	w.mustQuery(
		`select chat_id, endpoint from signals where model_id=? order by chat_id`,
		queryParams{modelID},
		scanTo{&chatID, &endpoint},
		func() {
			chats = append(chats, chatID)
			endpoints = append(endpoints, endpoint)
		})
	return
}
