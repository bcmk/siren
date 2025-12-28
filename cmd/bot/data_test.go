package main

import (
	"context"

	"github.com/bcmk/siren/internal/botconfig"
	"github.com/bcmk/siren/internal/db"
	"github.com/bcmk/siren/lib/cmdlib"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var testConfig = botconfig.Config{
	CheckGID:  true,
	MaxModels: 3,
	AdminID:   1,
	StatusConfirmationSeconds: botconfig.StatusConfirmationSeconds{
		Offline: 5,
	},
}

var testTranslations = cmdlib.Translations{
	Help:           &cmdlib.Translation{Str: "Help", Parse: cmdlib.ParseRaw},
	Online:         &cmdlib.Translation{Str: "Online %s", Parse: cmdlib.ParseRaw},
	Offline:        &cmdlib.Translation{Str: "Offline %s", Parse: cmdlib.ParseRaw},
	SyntaxAdd:      &cmdlib.Translation{Str: "SyntaxAdd", Parse: cmdlib.ParseRaw},
	SyntaxRemove:   &cmdlib.Translation{Str: "SyntaxRemove", Parse: cmdlib.ParseRaw},
	SyntaxFeedback: &cmdlib.Translation{Str: "SyntaxFeedback", Parse: cmdlib.ParseRaw},
	InvalidSymbols: &cmdlib.Translation{Str: "InvalidSymbols", Parse: cmdlib.ParseRaw},
	AlreadyAdded:   &cmdlib.Translation{Str: "AlreadyAdded %s", Parse: cmdlib.ParseRaw},
	AddError:       &cmdlib.Translation{Str: "AddError %s", Parse: cmdlib.ParseRaw},
	ModelAdded:     &cmdlib.Translation{Str: "ModelAdded %s", Parse: cmdlib.ParseRaw},
	ModelNotInList: &cmdlib.Translation{Str: "ModelNotInList %s", Parse: cmdlib.ParseRaw},
	ModelRemoved:   &cmdlib.Translation{Str: "ModelRemoved %s", Parse: cmdlib.ParseRaw},
	Feedback:       &cmdlib.Translation{Str: "Feedback", Parse: cmdlib.ParseRaw},
	Social:         &cmdlib.Translation{Str: "Social", Parse: cmdlib.ParseRaw},
	UnknownCommand: &cmdlib.Translation{Str: "UnknownCommand", Parse: cmdlib.ParseRaw},
	Languages:      &cmdlib.Translation{Str: "Languages", Parse: cmdlib.ParseRaw},
	Version:        &cmdlib.Translation{Str: "Version %s", Parse: cmdlib.ParseRaw},
	ProfileRemoved: &cmdlib.Translation{Str: "ProfileRemoved %s", Parse: cmdlib.ParseRaw},
	WeekRetrieving: &cmdlib.Translation{Str: "WeekRetrieving", Parse: cmdlib.ParseRaw},
}

type testWorker struct {
	worker
	terminate func()
}

func newTestWorker() *testWorker {
	ctx := context.Background()
	pgContainer, err := postgres.Run(
		ctx,
		"postgres:18",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	checkErr(err)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	checkErr(err)

	w := &testWorker{
		worker: worker{
			bots:            nil,
			db:              db.NewDatabase(connStr, true),
			cfg:             &testConfig,
			clients:         nil,
			tr:              map[string]*cmdlib.Translations{"test": &testTranslations},
			lowPriorityMsg:  make(chan outgoingPacket, 10000),
			highPriorityMsg: make(chan outgoingPacket, 10000),
		},
	}
	w.terminate = func() {
		checkErr(w.db.Close())
		checkErr(pgContainer.Terminate(ctx))
	}
	return w
}

func (w *testWorker) chatsForModel(modelID string) (chats []int64, endpoints []string) {
	var chatID int64
	var endpoint string
	w.db.MustQuery(
		`select chat_id, endpoint from signals where model_id = $1 order by chat_id`,
		db.QueryParams{modelID},
		db.ScanTo{&chatID, &endpoint},
		func() {
			chats = append(chats, chatID)
			endpoints = append(endpoints, endpoint)
		})
	return
}
