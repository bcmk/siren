package main

import (
	"context"
	"text/template"

	"github.com/bcmk/siren/v2/internal/botconfig"
	"github.com/bcmk/siren/v2/internal/db"
	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var testConfig = botconfig.Config{
	CheckGID:             true,
	MaxSubs:              3,
	AdminID:              1,
	HeavyUserRemainder:   1,
	OfflineNotifications: true,
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
	StreamerAdded:          &cmdlib.Translation{Key: "streamer_added", Str: "StreamerAdded %s", Parse: cmdlib.ParseRaw},
	StreamerNotInList:      &cmdlib.Translation{Key: "streamer_not_in_list", Str: "StreamerNotInList %s", Parse: cmdlib.ParseRaw},
	StreamerRemoved:        &cmdlib.Translation{Key: "streamer_removed", Str: "StreamerRemoved %s", Parse: cmdlib.ParseRaw},
	Feedback:               &cmdlib.Translation{Key: "feedback", Str: "Feedback", Parse: cmdlib.ParseRaw},
	Social:                 &cmdlib.Translation{Key: "social", Str: "Social", Parse: cmdlib.ParseRaw},
	UnknownCommand:         &cmdlib.Translation{Key: "unknown_command", Str: "UnknownCommand", Parse: cmdlib.ParseRaw},
	Languages:              &cmdlib.Translation{Key: "languages", Str: "Languages", Parse: cmdlib.ParseRaw},
	Version:                &cmdlib.Translation{Key: "version", Str: "Version %s", Parse: cmdlib.ParseRaw},
	ProfileRemoved:         &cmdlib.Translation{Key: "profile_removed", Str: "ProfileRemoved %s", Parse: cmdlib.ParseRaw},
	WeekRetrieving:         &cmdlib.Translation{Key: "week_retrieving", Str: "WeekRetrieving", Parse: cmdlib.ParseRaw},
	CheckingStreamer:       &cmdlib.Translation{Key: "checking_streamer", Str: "CheckingStreamer", Parse: cmdlib.ParseRaw},
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
	template.Must(tpl.New("checking_streamer").Parse("CheckingStreamer"))
	template.Must(tpl.New("streamer_added").Parse("StreamerAdded"))
	template.Must(tpl.New("online").Parse("Online"))
	template.Must(tpl.New("offline").Parse("Offline"))
	template.Must(tpl.New("subscription_usage").Parse("SubscriptionUsage"))
	template.Must(tpl.New("subscription_usage_ad").Parse("SubscriptionUsageAd"))
	template.Must(tpl.New("add_error").Parse("AddError"))

	w := &testWorker{
		worker: worker{
			bots:                  nil,
			db:                    db.NewDatabase(connStr, true),
			cfg:                   &testConfig,
			clients:               nil,
			tr:                    map[string]*cmdlib.Translations{"test": &testTranslations},
			tpl:                   map[string]*template.Template{"test": tpl},
			outgoingMsgCh:         make(chan outgoingPacket, maxHeapLen),
			nicknamePreprocessing: cmdlib.CanonicalNicknamePreprocessing,
			nicknameRegexp:        cmdlib.CommonNicknameRegexp,
		},
	}
	w.terminate = func() {
		checkErr(w.db.Close())
		checkErr(pgContainer.Terminate(ctx))
	}
	return w
}

func confirmedStatusesForChat(d *db.Database, endpoint string, chatID int64) (statuses []db.Streamer) {
	var iter db.Streamer
	d.MustQuery(`
		select s.nickname, s.confirmed_status
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		where sub.chat_id = $1 and sub.endpoint = $2
		order by s.nickname`,
		db.QueryParams{chatID, endpoint},
		db.ScanTo{&iter.Nickname, &iter.ConfirmedStatus},
		func() { statuses = append(statuses, iter) })
	return
}

func queryLastStatusChanges(d *db.Database) map[string]db.StatusChange {
	statusChanges := map[string]db.StatusChange{}
	var statusChange db.StatusChange
	d.MustQuery(
		`
			select distinct on (sc.streamer_id)
				s.nickname, sc.status, sc.timestamp
			from status_changes sc
			join streamers s on s.id = sc.streamer_id
			order by sc.streamer_id, sc.timestamp desc
		`,
		nil,
		db.ScanTo{&statusChange.Nickname, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.Nickname] = statusChange })
	return statusChanges
}

func (w *testWorker) chatsForStreamer(nickname string) (chats []int64, endpoints []string) {
	var chatID int64
	var endpoint string
	w.db.MustQuery(`
		select sub.chat_id, sub.endpoint
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		where s.nickname = $1
		order by sub.chat_id`,
		db.QueryParams{nickname},
		db.ScanTo{&chatID, &endpoint},
		func() {
			chats = append(chats, chatID)
			endpoints = append(endpoints, endpoint)
		})
	return
}

// insertSubscription creates a streamer (if needed) and inserts
// a confirmed subscription for tests
func insertSubscription(d *db.Database, endpoint string, chatID int64, nickname string) {
	d.MustExec(
		"insert into streamers (nickname) values ($1) on conflict(nickname) do nothing",
		nickname)
	d.MustExec(`
		insert into subscriptions (endpoint, chat_id, streamer_id)
		values ($1, $2, (select id from streamers where nickname = $3))`,
		endpoint, chatID, nickname)
}
