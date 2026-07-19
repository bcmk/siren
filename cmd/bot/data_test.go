package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"

	"github.com/bcmk/siren/v3/internal/botconfig"
	"github.com/bcmk/siren/v3/internal/checkers"
	"github.com/bcmk/siren/v3/internal/db"
	"github.com/bcmk/siren/v3/lib/cmdlib"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// nextOutgoing pops the message the sender would send next.
// The test worker sets commonCooling, so enqueue parks messages in the queue;
// tests read them there rather than from a channel.
func (w *testWorker) nextOutgoing() *queuedMessage {
	return w.sendQueue.pop()
}

var testConfig = botconfig.Config{
	CheckGID:             true,
	MaxSubs:              3,
	AdminID:              1,
	HeavyUserRemainder:   1,
	ReferralBonus:        1,
	FollowerBonus:        1,
	OfflineNotifications: true,
	StatusConfirmationSeconds: botconfig.StatusConfirmationSeconds{
		Offline: 5,
	},
}

var testTranslations = cmdlib.Translations{
	Start:                  &cmdlib.Translation{Key: "start", Str: "Start", Parse: cmdlib.ParseRaw},
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
	ReferralApplied:        &cmdlib.Translation{Key: "referral_applied", Str: "ReferralApplied", Parse: cmdlib.ParseRaw},
	InvalidReferralLink:    &cmdlib.Translation{Key: "invalid_referral_link", Str: "InvalidReferralLink", Parse: cmdlib.ParseRaw},
	FollowerExists:         &cmdlib.Translation{Key: "follower_exists", Str: "FollowerExists", Parse: cmdlib.ParseRaw},
	OwnReferralLinkHit:     &cmdlib.Translation{Key: "own_referral_link_hit", Str: "OwnReferralLinkHit", Parse: cmdlib.ParseRaw},
}

type testWorker struct {
	worker
	terminate func()
}

// templateDBName is the database migrations are applied to once per run.
// Each test clones it, so a test pays for a file copy
// rather than a container start plus 60-odd migrations.
const templateDBName = "test"

var (
	// pgOnce starts the container on first use, so a run with no database test
	// — go test -run TestCommandParser — pays nothing for it.
	pgOnce      sync.Once
	pgContainer *postgres.PostgresContainer

	// baseConnStr points at templateDBName on the shared container.
	// connStrFor rewrites its path to reach the other databases.
	baseConnStr string

	// adminConn issues create database against the maintenance database.
	// pgx connections are not goroutine safe, so adminMu guards it,
	// which also keeps concurrent clones off the same template.
	adminConn *pgx.Conn
	adminMu   sync.Mutex

	dbCounter atomic.Int64
)

func TestMain(m *testing.M) {
	// Set once here: parallel tests must not race on this global.
	cmdlib.Verbosity = cmdlib.SilentVerbosity
	code := m.Run()
	// Reached only after every test has returned, so the once is settled.
	if pgContainer != nil {
		ctx := context.Background()
		_ = adminConn.Close(ctx)
		_ = pgContainer.Terminate(ctx)
	}
	os.Exit(code)
}

// startPostgres boots the shared container and migrates the template.
func startPostgres() {
	ctx := context.Background()

	var err error
	pgContainer, err = postgres.Run(
		ctx,
		"postgres:18",
		postgres.WithDatabase(templateDBName),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	checkErr(err)

	baseConnStr, err = pgContainer.ConnectionString(ctx, "sslmode=disable")
	checkErr(err)

	// Migrate the template, then disconnect:
	// create database rejects a template that still has clients.
	d := db.NewDatabase(baseConnStr, false, testConfig.MaxSubs)
	d.ApplyMigrations()
	checkErr(d.Close())

	adminConn, err = pgx.Connect(ctx, connStrFor("postgres"))
	checkErr(err)
}

// connStrFor returns baseConnStr pointed at another database on the container.
func connStrFor(dbName string) string {
	u, err := url.Parse(baseConnStr)
	checkErr(err)
	u.Path = "/" + dbName
	return u.String()
}

// newTestWorker clones the migrated template into a database of its own,
// so tests stay isolated and can run in parallel.
func newTestWorker() *testWorker {
	pgOnce.Do(startPostgres)

	ctx := context.Background()
	// Generated, so it needs no quoting: identifiers cannot be query args.
	name := fmt.Sprintf("test_%d", dbCounter.Add(1))

	adminMu.Lock()
	_, err := adminConn.Exec(ctx, fmt.Sprintf("create database %s template %s", name, templateDBName))
	adminMu.Unlock()
	checkErr(err)

	connStr := connStrFor(name)

	tpl := template.New("")
	template.Must(tpl.New("start").Parse("Start"))
	template.Must(tpl.New("checking_streamer").Parse("CheckingStreamer"))
	template.Must(tpl.New("streamer_added").Parse("StreamerAdded"))
	template.Must(tpl.New("online").Parse("Online"))
	template.Must(tpl.New("offline").Parse("Offline"))
	template.Must(tpl.New("subscription_usage").Parse("SubscriptionUsage"))
	template.Must(tpl.New("subscription_usage_ad").Parse("SubscriptionUsageAd"))
	template.Must(tpl.New("add_error").Parse("AddError"))
	template.Must(tpl.New("referral_applied").Parse("ReferralApplied"))
	template.Must(tpl.New("invalid_referral_link").Parse("InvalidReferralLink"))
	template.Must(tpl.New("follower_exists").Parse("FollowerExists"))
	template.Must(tpl.New("own_referral_link_hit").Parse("OwnReferralLinkHit"))

	w := &testWorker{
		worker: worker{
			bots:          nil,
			db:            db.NewDatabase(connStr, true, testConfig.MaxSubs),
			cfg:           &testConfig,
			client:        nil,
			tr:            map[string]*cmdlib.Translations{"test": &testTranslations},
			tpl:           map[string]*template.Template{"test": tpl},
			sendQueue:     newSendQueue(),
			sendResults:   make(chan msgSendResult, sendChanCap),
			cooledUsers:   make(chan db.UserID, sendChanCap),
			shutdownCh:    make(chan struct{}),
			commonCooling: true,
			checker:       &checkers.RandomChecker{BaseChecker: checkers.NewBaseChecker(&checkers.TestCheckerConfig{})},
		},
	}
	// No drop database: the container's teardown takes every clone with it.
	w.terminate = func() { checkErr(w.db.Close()) }
	return w
}

func confirmedStatusesForChat(d *db.Database, endpoint string, chatID int64) (statuses []db.Streamer) {
	var iter db.Streamer
	d.MustQuery(`
		select s.nickname, s.confirmed_status
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		join users u on u.id = sub.user_id
		where u.chat_id = $1 and sub.endpoint = $2
		order by s.nickname`,
		db.QueryParams{chatID, endpoint},
		db.ScanTo{&iter.Nickname, &iter.ConfirmedStatus},
		func() { statuses = append(statuses, iter) })
	return
}

func queryLastStatusChanges(d *db.Database) map[string]db.StatusChange {
	statusChanges := map[string]db.StatusChange{}
	var statusChange db.StatusChange
	d.MustQuery(`
		select distinct on (sc.streamer_id)
			s.nickname, sc.status, sc.timestamp
		from status_changes sc
		join streamers s on s.id = sc.streamer_id
		order by sc.streamer_id, sc.timestamp desc`,
		nil,
		db.ScanTo{&statusChange.Nickname, &statusChange.Status, &statusChange.Timestamp},
		func() { statusChanges[statusChange.Nickname] = statusChange })
	return statusChanges
}

func (w *testWorker) chatsForStreamer(nickname string) (chats []int64, endpoints []string) {
	var chatID int64
	var endpoint string
	w.db.MustQuery(`
		select u.chat_id, sub.endpoint
		from subscriptions sub
		join streamers s on s.id = sub.streamer_id
		join users u on u.id = sub.user_id
		where s.nickname = $1
		order by u.chat_id`,
		db.QueryParams{nickname},
		db.ScanTo{&chatID, &endpoint},
		func() {
			chats = append(chats, chatID)
			endpoints = append(endpoints, endpoint)
		})
	return
}

func insertTestStreamer(d *db.Database, s db.Streamer) int {
	d.MustExec("insert into nicknames (nickname) values ($1)", s.Nickname)
	return d.MustInt(`
		insert into streamers (
			nickname,
			confirmed_status,
			unconfirmed_status,
			unconfirmed_timestamp,
			prev_unconfirmed_status,
			prev_unconfirmed_timestamp)
		values ($1, $2, $3, $4, $5, $6)
		returning id`,
		s.Nickname,
		s.ConfirmedStatus,
		s.UnconfirmedStatus,
		s.UnconfirmedTimestamp,
		s.PrevUnconfirmedStatus,
		s.PrevUnconfirmedTimestamp)
}

func insertSubscription(d *db.Database, endpoint string, chatID int64, nickname string) {
	d.AddUser(chatID, 0, 0, "")
	d.MustExec(`
		insert into subscriptions (endpoint, user_id, streamer_id)
		select $1, u.id, s.id
		from users u, streamers s
		where u.chat_id = $2 and s.nickname = $3`,
		endpoint, chatID, nickname)
}

func insertPendingSubscription(d *db.Database, endpoint string, chatID int64, nickname string, checking bool) {
	d.AddUser(chatID, 0, 0, "")
	d.MustExec(`
		insert into pending_subscriptions (endpoint, user_id, nickname, checking)
		select $1, u.id, $3, $4
		from users u
		where u.chat_id = $2`,
		endpoint, chatID, nickname, checking)
}
