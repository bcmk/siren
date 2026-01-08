package main

import (
	"context"
	"text/template"

	"github.com/bcmk/siren/internal/botconfig"
	"github.com/bcmk/siren/internal/db"
	"github.com/bcmk/siren/lib/cmdlib"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var testConfig = botconfig.Config{
	CheckGID:           true,
	MaxChannels:        3,
	AdminID:            1,
	HeavyUserRemainder: 1,
	ErrorDenominator:   10,
	StatusConfirmationSeconds: botconfig.StatusConfirmationSeconds{
		Offline: 5,
	},
}

var testTranslations = cmdlib.Translations{
	Help:                   &cmdlib.Translation{Key: "help", Str: "Help", Parse: cmdlib.ParseRaw},
	Online:                 &cmdlib.Translation{Key: "online", Str: "Online %s", Parse: cmdlib.ParseRaw},
	Offline:                &cmdlib.Translation{Key: "offline", Str: "Offline %s", Parse: cmdlib.ParseRaw},
	SyntaxAdd:              &cmdlib.Translation{Key: "syntax_add", Str: "SyntaxAdd", Parse: cmdlib.ParseRaw},
	SyntaxRemove:           &cmdlib.Translation{Key: "syntax_remove", Str: "SyntaxRemove", Parse: cmdlib.ParseRaw},
	SyntaxFeedback:         &cmdlib.Translation{Key: "syntax_feedback", Str: "SyntaxFeedback", Parse: cmdlib.ParseRaw},
	InvalidSymbols:         &cmdlib.Translation{Key: "invalid_symbols", Str: "InvalidSymbols", Parse: cmdlib.ParseRaw},
	AlreadyAdded:           &cmdlib.Translation{Key: "already_added", Str: "AlreadyAdded %s", Parse: cmdlib.ParseRaw},
	AddError:               &cmdlib.Translation{Key: "add_error", Str: "AddError %s", Parse: cmdlib.ParseRaw},
	ChannelAdded:           &cmdlib.Translation{Key: "channel_added", Str: "ChannelAdded %s", Parse: cmdlib.ParseRaw},
	ChannelNotInList:       &cmdlib.Translation{Key: "channel_not_in_list", Str: "ChannelNotInList %s", Parse: cmdlib.ParseRaw},
	ChannelRemoved:         &cmdlib.Translation{Key: "channel_removed", Str: "ChannelRemoved %s", Parse: cmdlib.ParseRaw},
	Feedback:               &cmdlib.Translation{Key: "feedback", Str: "Feedback", Parse: cmdlib.ParseRaw},
	Social:                 &cmdlib.Translation{Key: "social", Str: "Social", Parse: cmdlib.ParseRaw},
	UnknownCommand:         &cmdlib.Translation{Key: "unknown_command", Str: "UnknownCommand", Parse: cmdlib.ParseRaw},
	Languages:              &cmdlib.Translation{Key: "languages", Str: "Languages", Parse: cmdlib.ParseRaw},
	Version:                &cmdlib.Translation{Key: "version", Str: "Version %s", Parse: cmdlib.ParseRaw},
	ProfileRemoved:         &cmdlib.Translation{Key: "profile_removed", Str: "ProfileRemoved %s", Parse: cmdlib.ParseRaw},
	WeekRetrieving:         &cmdlib.Translation{Key: "week_retrieving", Str: "WeekRetrieving", Parse: cmdlib.ParseRaw},
	CheckingChannel:        &cmdlib.Translation{Key: "checking_channel", Str: "CheckingChannel", Parse: cmdlib.ParseRaw},
	NotEnoughSubscriptions: &cmdlib.Translation{Key: "not_enough_subscriptions", Str: "NotEnoughSubscriptions", Parse: cmdlib.ParseRaw},
	SubscriptionUsage:      &cmdlib.Translation{Key: "subscription_usage", Str: "SubscriptionUsage", Parse: cmdlib.ParseRaw},
	SubscriptionUsageAd:    &cmdlib.Translation{Key: "subscription_usage_ad", Str: "SubscriptionUsageAd", Parse: cmdlib.ParseRaw},
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

	tpl := template.New("")
	template.Must(tpl.New("checking_channel").Parse("CheckingChannel"))
	template.Must(tpl.New("channel_added").Parse("ChannelAdded"))
	template.Must(tpl.New("online").Parse("Online"))
	template.Must(tpl.New("offline").Parse("Offline"))
	template.Must(tpl.New("subscription_usage").Parse("SubscriptionUsage"))
	template.Must(tpl.New("subscription_usage_ad").Parse("SubscriptionUsageAd"))
	template.Must(tpl.New("add_error").Parse("AddError"))

	w := &testWorker{
		worker: worker{
			bots:                   nil,
			db:                     db.NewDatabase(connStr, true),
			cfg:                    &testConfig,
			clients:                nil,
			tr:                     map[string]*cmdlib.Translations{"test": &testTranslations},
			tpl:                    map[string]*template.Template{"test": tpl},
			lowPriorityMsg:         make(chan outgoingPacket, 10000),
			highPriorityMsg:        make(chan outgoingPacket, 10000),
			unsuccessfulRequests:   make([]bool, testConfig.ErrorDenominator),
			channelIDPreprocessing: cmdlib.CanonicalChannelID,
			channelIDRegexp:        cmdlib.CommonChannelIDRegexp,
		},
	}
	w.terminate = func() {
		checkErr(w.db.Close())
		checkErr(pgContainer.Terminate(ctx))
	}
	return w
}

func (w *testWorker) chatsForChannel(channelID string) (chats []int64, endpoints []string) {
	var chatID int64
	var endpoint string
	w.db.MustQuery(
		`select chat_id, endpoint from signals where channel_id = $1 order by chat_id`,
		db.QueryParams{channelID},
		db.ScanTo{&chatID, &endpoint},
		func() {
			chats = append(chats, chatID)
			endpoints = append(endpoints, endpoint)
		})
	return
}
