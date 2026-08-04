// This is the main executable package
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	htmltemplate "html/template"
	"image"
	"io"
	"maps"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	texttemplate "text/template"
	"time"

	// The alpine image ships no zone database, so the binary carries one.
	_ "time/tzdata"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/bcmk/siren/v4/internal/botconfig"
	"github.com/bcmk/siren/v4/internal/checkers"
	"github.com/bcmk/siren/v4/internal/db"
	"github.com/bcmk/siren/v4/lib/cmdlib"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/spf13/pflag"
)

var (
	checkErr = cmdlib.CheckErr
	lerr     = cmdlib.Lerr
	linf     = cmdlib.Linf
	ldbg     = cmdlib.Ldbg
)

type tplData = map[string]interface{}

type timeDiff struct {
	Days        int
	Hours       int
	Minutes     int
	Seconds     int
	Nanoseconds int
}

// streamerListEntry is the per-row payload of the list and week_never_online templates,
// so a field rename must reach both, or the loser fails at render.
type streamerListEntry struct {
	Link     string
	TimeDiff *timeDiff
}

type worker struct {
	db                         db.Database
	fuzzySearchDB              db.Database
	client                     *http.Client
	bots                       map[string]*bot.Bot
	cfg                        *botconfig.Config
	tr                         map[string]*cmdlib.Translations
	tpl                        map[string]*texttemplate.Template
	legacyAffiliateTpl         *texttemplate.Template
	trAds                      map[string]map[string]*cmdlib.Translation
	tplAds                     map[string]*texttemplate.Template
	checker                    checkers.Checker
	imageDownloadLogs          chan imageDownloadLog
	unconfirmedOnlineStreamers map[string]cmdlib.StreamerInfo
	botNames                   map[string]string
	sendQueue                  sendQueue
	commonCooling              bool
	sendSeq                    uint64
	sendResults                chan msgSendResult
	cooledUsers                chan db.UserID
	ownerUserID                db.UserID
	deliverWG                  sync.WaitGroup
	existenceListResults       chan *cmdlib.ExistenceListResults
	checkerResults             chan cmdlib.CheckerResults
	sendingNotifications       chan []db.Notification
	imagedNotifications        chan notificationBatch
	ourIDs                     []int64
	searchHTML                 *htmltemplate.Template
	timezoneHTML               *htmltemplate.Template
	// zoneNames maps a lowercased IANA name to the zone the binary loaded for it,
	// whose own name is the spelling to store.
	// Written once at startup and only read after, so a concurrent reader needs no lock.
	zoneNames              map[string]*time.Location
	searchRequests         chan searchRequest
	webAppAddRequests      chan webAppAddRequest
	webAppTimezoneRequests chan webAppTimezoneRequest
	incomingPackets        chan incomingPacket
	maintenance            atomic.Bool
	shuttingDown           atomic.Bool
	// chatMember is the admin gate's lookup, a field so tests can fake the answer.
	chatMember func(endpoint string, chatID, userID int64) (*models.ChatMember, error)
	shutdownCh chan struct{}
}

type searchRequest struct {
	endpoint string
	chatID   int64
	term     string
	resultCh chan searchResult
}

// searchResult answers a searchRequest.
// allowed is false when the daemon finds the chat unvetted,
// so a refusal reads the same whichever layer catches it.
type searchResult struct {
	streamers []string
	allowed   bool
}

type webAppAddRequest struct {
	endpoint string
	chatID   int64
	nickname string
	// admittedCh reports whether the request was admitted,
	// not whether it subscribed:
	// an admitted add answers in the chat itself,
	// having subscribed, parked a pending subscription, or explained a refusal.
	// Only a chat outside the whitelist is dropped in silence,
	// and that alone must not read as success.
	admittedCh chan bool
}

// webAppTimezoneRequest carries a zone picked in the timezone web app.
// The zone is canonical: the HTTP handler resolves what the page submits,
// so the main loop stores a name it can load back.
type webAppTimezoneRequest struct {
	endpoint string
	chatID   int64
	zone     string
	// admittedCh reports whether the chat passed the whitelist,
	// the one refusal the page cannot be told apart from a save.
	admittedCh chan bool
}

type incomingPacket struct {
	message  *models.Update
	endpoint string
}

type appliedKind int

const (
	invalidReferral appliedKind = iota
	followerExists
	referralApplied
)

const (
	messageSent                = 200
	messageBadRequest          = 400
	messageBlocked             = 403
	messageTooManyRequests     = 429
	messageUnknownError        = -1
	messageUnknownNetworkError = -2
	messageTimeout             = -3
	messageMigrate             = -4
	messageChatNotFound        = -5
	messageSkipped             = -6
	messageNoPhotoRights       = -7
	messageNoTextRights        = -8
	messageTopicClosed         = -9
)

type msgSendResult struct {
	priority  db.Priority
	timestamp int
	result    int
	endpoint  string
	chatID    int64
	userID    db.UserID
	// migrateToChatID is meaningful only for messageMigrate, which always names a target.
	migrateToChatID int64
	latency         int
	tag             sendTag
	notificationID  int
	// resend is the original queued message,
	// handed back whole for the main loop to re-queue,
	// so no field is lost in a copy on the way.
	resend *queuedMessage
}

type sendDisposition int

const (
	dispFinalize sendDisposition = iota // truly done: delete the row
	dispResend                          // fallback, postponed, or migrated message: re-queue
	dispRearm                           // same-id migrate: re-arm the row
	dispLeave                           // same-id reply migrate or any maintenance migrate: skip
)

// disposition classifies a delivery's result.
func (r msgSendResult) disposition() sendDisposition {
	switch {
	case r.resend != nil:
		return dispResend
	case r.result != messageMigrate:
		return dispFinalize
	case r.notificationID != 0:
		return dispRearm
	default:
		return dispLeave
	}
}

type waitingUser struct {
	chatID   int64
	endpoint string
}

type imageDownloadLog struct {
	success    bool
	durationMs int
}

func newWorker(cfg *botconfig.Config, checker checkers.Checker) *worker {
	client := cmdlib.HTTPClientWithTimeout(cfg.ImageDownloadTimeout())

	incomingPackets := make(chan incomingPacket, incomingBufferSize*len(cfg.Endpoints))
	telegramClient := cmdlib.HTTPClientWithTimeout(cfg.TelegramTimeout())
	bots := make(map[string]*bot.Bot)
	for n, p := range cfg.Endpoints {
		endpointName := n
		handler := func(_ context.Context, _ *bot.Bot, update *models.Update) {
			incomingPackets <- incomingPacket{message: update, endpoint: endpointName}
		}
		b, err := bot.New(
			string(p.BotToken),
			bot.WithHTTPClient(0, telegramClient),
			bot.WithDefaultHandler(handler),
			bot.WithUpdatesChannelCap(incomingBufferSize))
		checkErr(err)
		bots[n] = b
	}
	tr, tpl := cmdlib.LoadAllTranslations(trsByEndpoint(cfg))
	trAds, tplAds := cmdlib.LoadAllAds(trsAdsByEndpoint(cfg))
	// A standalone template: affiliate_link gets no translation func or named template.
	var legacyAffiliateTpl *texttemplate.Template
	if cfg.AffiliateLink != "" {
		legacyAffiliateTpl = texttemplate.Must(texttemplate.New("affiliate_link").Parse(cfg.AffiliateLink))
	}
	w := &worker{
		bots:                       bots,
		db:                         db.NewDatabase(string(cfg.DBConnectionString), cfg.CheckGID, cfg.MaxSubs),
		fuzzySearchDB:              db.NewDatabase(string(cfg.DBConnectionString), false, cfg.MaxSubs),
		cfg:                        cfg,
		client:                     client,
		tr:                         tr,
		tpl:                        tpl,
		legacyAffiliateTpl:         legacyAffiliateTpl,
		trAds:                      trAds,
		tplAds:                     tplAds,
		imageDownloadLogs:          make(chan imageDownloadLog),
		unconfirmedOnlineStreamers: map[string]cmdlib.StreamerInfo{},
		botNames:                   map[string]string{},
		sendQueue:                  newSendQueue(),
		sendResults:                make(chan msgSendResult, sendChanCap),
		cooledUsers:                make(chan db.UserID, sendChanCap),
		shutdownCh:                 make(chan struct{}),
		existenceListResults:       make(chan *cmdlib.ExistenceListResults),
		checkerResults:             make(chan cmdlib.CheckerResults),
		sendingNotifications:       make(chan []db.Notification, 1000),
		imagedNotifications:        make(chan notificationBatch),
		ourIDs:                     getOurIDs(cfg),
		searchRequests:             make(chan searchRequest),
		webAppAddRequests:          make(chan webAppAddRequest),
		webAppTimezoneRequests:     make(chan webAppTimezoneRequest),
		incomingPackets:            incomingPackets,
	}
	w.chatMember = w.getChatMember
	// The bot starts in maintenance: the database is not created yet.
	w.maintenance.Store(true)
	for endpoint, a := range tr {
		for _, b := range a.ToMap() {
			w.loadImageForTranslation(endpoint, b)
		}
	}
	for endpoint, a := range trAds {
		for _, b := range a {
			w.loadImageForTranslation(endpoint, b)
		}
	}

	w.checker = checker

	searchHTMLBytes, err := os.ReadFile("res/webapp/search.html")
	checkErr(err)
	w.searchHTML, err = htmltemplate.New("search").Parse(string(searchHTMLBytes))
	checkErr(err)

	timezoneHTMLBytes, err := os.ReadFile("res/webapp/timezone.html")
	checkErr(err)
	w.timezoneHTML, err = htmltemplate.New("timezone").Parse(string(timezoneHTMLBytes))
	checkErr(err)

	return w
}

func (w *worker) loadImageForTranslation(endpoint string, tr *cmdlib.Translation) {
	if tr.Image != "" {
		p := path.Join(w.cfg.Endpoints[endpoint].Images, tr.Image)
		imageBytes, err := os.ReadFile(p)
		tr.ImageBytes = imageBytes
		checkErr(err)
	}
}

func trsByEndpoint(cfg *botconfig.Config) map[string][]string {
	result := make(map[string][]string)
	for k, v := range cfg.Endpoints {
		result[k] = v.Translation
	}
	return result
}

func trsAdsByEndpoint(cfg *botconfig.Config) map[string][]string {
	result := map[string][]string{}
	for k, v := range cfg.Endpoints {
		result[k] = v.Ads
	}
	return result
}

func (w *worker) setWebhook() {
	ctx := context.Background()
	for n, p := range w.cfg.Endpoints {
		linf("setting webhook for endpoint %s...", n)
		params := &bot.SetWebhookParams{URL: path.Join(p.WebhookDomain, string(p.ListenPath))}
		_, err := w.bots[n].SetWebhook(ctx, params)
		checkErr(err)
		info, err := w.bots[n].GetWebhookInfo(ctx)
		checkErr(err)
		if info.LastErrorDate != 0 {
			linf("last webhook error time: %v", time.Unix(int64(info.LastErrorDate), 0))
		}
		if info.LastErrorMessage != "" {
			linf("last webhook error message: %s", info.LastErrorMessage)
		}
		linf("OK")
	}
}

func (w *worker) removeWebhook(ctx context.Context) {
	for n := range w.cfg.Endpoints {
		linf("removing webhook for endpoint %s...", n)
		// nil, not &DeleteWebhookParams{}: an empty struct makes the library
		// send a part-less multipart body that Telegram 400s with no body.
		if _, err := w.bots[n].DeleteWebhook(ctx, nil); err != nil {
			lerr("failed to remove webhook for endpoint %s: %v", n, err)
			continue
		}
		linf("OK")
	}
}

func (w *worker) initBotNames() {
	ctx := context.Background()
	for n := range w.cfg.Endpoints {
		user, err := w.bots[n].GetMe(ctx)
		checkErr(err)
		linf("bot name for endpoint %s: %s", n, user.Username)
		w.botNames[n] = user.Username
	}
}

func (w *worker) menuCommandEnabled(command string) bool {
	switch command {
	case "buy_subs":
		return w.cfg.BuySubsEnabled()
	case "week":
		return w.cfg.EnableWeek
	}
	return true
}

func (w *worker) setCommands() {
	ctx := context.Background()
	for n := range w.cfg.Endpoints {
		text := templateToString(w.tpl[n], w.tr[n].RawCommands.Key, nil)
		lines := strings.Split(text, "\n")
		var commands []models.BotCommand
		for _, l := range lines {
			pair := strings.SplitN(l, "-", 2)
			if len(pair) != 2 {
				checkErr(fmt.Errorf("unexpected command pair %q", l))
			}
			pair[0], pair[1] = strings.TrimSpace(pair[0]), strings.TrimSpace(pair[1])
			if !w.menuCommandEnabled(pair[0]) {
				continue
			}
			commands = append(commands, models.BotCommand{Command: pair[0], Description: pair[1]})
			ldbg("command %s - %s", pair[0], pair[1])
		}
		linf("setting commands for endpoint %s...", n)
		_, err := w.bots[n].SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: commands})
		checkErr(err)
		if w.customAffiliateLinkEnabled() {
			// Scopes do not merge, so admins get the defaults plus /affiliate.
			adminCommands := append(commands[:len(commands):len(commands)],
				models.BotCommand{Command: "affiliate", Description: w.tr[n].AffiliateCommand.Str})
			_, err = w.bots[n].SetMyCommands(ctx, &bot.SetMyCommandsParams{
				Commands: adminCommands,
				Scope:    &models.BotCommandScopeAllChatAdministrators{},
			})
			checkErr(err)
		} else {
			// Telegram keeps a scope until it is deleted, so drop one an earlier run left.
			_, err = w.bots[n].DeleteMyCommands(ctx, &bot.DeleteMyCommandsParams{
				Scope: &models.BotCommandScopeAllChatAdministrators{},
			})
			checkErr(err)
		}
		linf("OK")
	}
}

func (w *worker) setDefaultAdminRights() {
	ctx := context.Background()
	for n := range w.cfg.Endpoints {
		linf("setting default admin rights for channels for endpoint %s...", n)
		_, err := w.bots[n].SetMyDefaultAdministratorRights(ctx, &bot.SetMyDefaultAdministratorRightsParams{
			Rights:      &models.ChatAdministratorRights{CanPostMessages: true},
			ForChannels: true,
		})
		checkErr(err)
		linf("OK")
	}
}

func (w *worker) sendText(
	priority db.Priority,
	endpoint string,
	userID db.UserID,
	notify bool,
	disablePreview bool,
	parse cmdlib.ParseKind,
	text string,
	tag sendTag,
) {
	msg := textMessage(text, notify, disablePreview, parse)
	w.enqueueMessage(priority, endpoint, msg, tag, userID, 0)
}

// sendMaintenance queues a maintenance-window notice
// addressed by a literal chat id.
// Its userID is 0,
// so the scheduler keeps that chat id rather than resolving one.
// As a MaintenancePacket its send result skips all database bookkeeping,
// so it may run before the database exists.
func (w *worker) sendMaintenance(endpoint string, chatID int64, notify bool, text string) {
	msg := textMessage(text, notify, true, cmdlib.ParseRaw)
	msg.setChatID(chatID)
	w.enqueueMessage(db.PriorityHigh, endpoint, msg, unprompted(db.MaintenancePacket), 0, 0)
}

// sendMessageInternal sends one message and classifies the outcome.
// retryAfter is Telegram's requested 429 pause in seconds, 0 otherwise.
func (w *worker) sendMessageInternal(
	endpoint string,
	msg sendable,
) (result int, migrateTo int64, retryAfter int) {
	chatID := msg.chatID()
	if !w.cfg.ChatWhitelisted(chatID) {
		return messageSkipped, 0, 0
	}
	ctx := context.Background()
	if _, err := msg.send(ctx, w.bots[endpoint]); err != nil {
		var migrateErr *bot.MigrateError
		if errors.As(err, &migrateErr) {
			ldbg("cannot send a message, group migration")
			// The library builds a MigrateError only around a set target, never a zero.
			return messageMigrate, int64(migrateErr.MigrateToChatID), 0
		}
		var tooManyErr *bot.TooManyRequestsError
		if errors.As(err, &tooManyErr) {
			ldbg("cannot send a message, too many requests: retry_after = %d", tooManyErr.RetryAfter)
			return messageTooManyRequests, 0, tooManyErr.RetryAfter
		}
		if errors.Is(err, bot.ErrorForbidden) {
			ldbg("cannot send a message, bot blocked")
			return messageBlocked, 0, 0
		}
		if errors.Is(err, bot.ErrorBadRequest) {
			if strings.Contains(err.Error(), "chat not found") {
				ldbg("cannot send a message, chat not found")
				return messageChatNotFound, 0, 0
			}
			if strings.Contains(err.Error(), "not enough rights to send photos") {
				ldbg("cannot send a message, no photo rights")
				return messageNoPhotoRights, 0, 0
			}
			if strings.Contains(err.Error(), "not enough rights to send text messages") {
				ldbg("cannot send a message, no text rights")
				return messageNoTextRights, 0, 0
			}
			if strings.Contains(err.Error(), "TOPIC_CLOSED") {
				ldbg("cannot send a message, topic closed")
				return messageTopicClosed, 0, 0
			}
			lerr("cannot send a message, bad request, error: %v", err)
			return messageBadRequest, 0, 0
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			if netErr.Timeout() {
				ldbg("cannot send a message, timeout")
				return messageTimeout, 0, 0
			}
			lerr("cannot send a message, unknown network error")
			return messageUnknownNetworkError, 0, 0
		}
		lerr("unexpected error type while sending a message to %d, %v", chatID, err)
		return messageUnknownError, 0, 0
	}
	return messageSent, 0, 0
}

func templateToString(t *texttemplate.Template, key string, data tplData) string {
	buf := &bytes.Buffer{}
	err := t.ExecuteTemplate(buf, key, data)
	checkErr(err)
	return buf.String()
}

func parseMode(parse cmdlib.ParseKind) models.ParseMode {
	switch parse {
	case cmdlib.ParseHTML:
		return models.ParseModeHTML
	case cmdlib.ParseMarkdown:
		return models.ParseModeMarkdown
	}
	return ""
}

func textMessage(text string, notify, disablePreview bool, parse cmdlib.ParseKind) *messageParams {
	params := &bot.SendMessageParams{
		Text:                text,
		DisableNotification: !notify,
		ParseMode:           parseMode(parse),
	}
	if disablePreview {
		params.LinkPreviewOptions = &models.LinkPreviewOptions{IsDisabled: bot.True()}
	}
	return &messageParams{SendMessageParams: params}
}

// enqueueTr queues a translation to render at dispatch,
// tagged with the notification_queue row to clear once sent (0 for replies).
// The message carries no chat id; trySend resolves it from userID,
// and the chat kind decides what a command mention carries.
func (w *worker) enqueueTr(
	priority db.Priority,
	endpoint string,
	userID db.UserID,
	notify bool,
	translation *cmdlib.Translation,
	data tplData,
	image []byte,
	tag sendTag,
	notificationID int,
) {
	params := &renderParams{templates: w.tpl[endpoint], key: translation.Key, data: data}
	msg := params.asDeferredSendable(translation, notify, image)
	w.enqueueMessage(priority, endpoint, msg, tag, userID, notificationID)
}

func (w *worker) sendTr(
	priority db.Priority,
	endpoint string,
	userID db.UserID,
	notify bool,
	translation *cmdlib.Translation,
	data tplData,
	tag sendTag,
) {
	// Replies leave the chat id to trySend;
	// a single reply per command makes the per-dispatch lookup negligible.
	w.enqueueTr(priority, endpoint, userID, notify, translation, data, nil, tag, 0)
}

func (w *worker) sendAdsTr(
	priority db.Priority,
	endpoint string,
	userID db.UserID,
	notify bool,
	translation *cmdlib.Translation,
	command string,
) {
	tag := adTag(command)
	params := &renderParams{templates: w.tplAds[endpoint], key: translation.Key}
	msg := params.asDeferredSendable(translation, notify, translation.ImageBytes)
	w.enqueueMessage(priority, endpoint, msg, tag, userID, 0)
}

func (w *worker) createDatabase() {
	linf("ensuring database created...")
	for _, prelude := range w.cfg.SQLPrelude {
		w.db.MustExec(prelude)
		// fuzzySearchDaemon also owns this connection.
		// It touches the connection only to serve a search request,
		// and no request arrives until registerWebApp, which runs after this.
		// fuzzySearchDB has no GID check to catch a slip in that ordering.
		w.fuzzySearchDB.MustExec(prelude)
	}
	w.db.ApplyMigrations()
	w.db.ResetQueryStats()
}

// minZoneNames is the floor a working zone table clears with room to spare,
// the smallest packaging shipping some four hundred names.
// A server whose zone directory is missing returns none, refusing every zone a chat names.
const minZoneNames = 100

// initTimezones loads the zone spellings the server holds, which parseTimezone resolves against.
// It runs before the web app is served, whose goroutines read the map without a lock.
func (w *worker) initTimezones() {
	w.setZoneNames(w.db.TimezoneNames())
}

// setZoneNames is the one way the map is filled, so no path stores a table without the floor.
// It stops a bot whose server has no working zone table,
// which would otherwise run on and refuse every zone a chat names.
//
// The server's names are loaded here, once, and only those that load are kept:
// the two carry their own copies of the zone database and drift as either is upgraded,
// and a name the page offered but the save refused would be the drift landing on a chat.
func (w *worker) setZoneNames(names map[string]string) {
	zones := make(map[string]*time.Location, len(names))
	for lower, canonical := range names {
		loc, err := time.LoadLocation(canonical)
		if err != nil {
			lerr("the server holds a zone this binary cannot load: timezone = %s, %v", canonical, err)
			continue
		}
		zones[lower] = loc
	}
	if len(zones) < minZoneNames {
		checkErr(fmt.Errorf("the server offers %d loadable timezone names, fewer than %d: "+
			"its zone table is broken", len(zones), minZoneNames))
	}
	w.zoneNames = zones
	linf("timezone names loaded: %d of the %d offered", len(zones), len(names))
}

func (w *worker) initCache() {
	start := time.Now()
	w.unconfirmedOnlineStreamers = map[string]cmdlib.StreamerInfo{}
	for nickname := range w.db.QueryLastOnlineStreamers() {
		w.unconfirmedOnlineStreamers[nickname] = cmdlib.StreamerInfo{}
	}
	elapsed := time.Since(start)
	linf("cache initialized with %d online streamers in %d ms", len(w.unconfirmedOnlineStreamers), elapsed.Milliseconds())
}

func (w *worker) notifyOfAddResults(priority db.Priority, notifications []db.Notification) {
	for _, n := range notifications {
		if w.tr[n.Endpoint] == nil {
			// An orphaned endpoint (removed from config)
			// can still hold a pending subscription,
			// which nothing purges by endpoint;
			// drop the result rather than panic the main goroutine,
			// as notifyOfStatus does.
			lerr("dropping add result for unknown endpoint %s", n.Endpoint)
			continue
		}
		data := tplData{"streamer": n.Nickname}
		if n.Status&(cmdlib.StatusOnline|cmdlib.StatusOffline|cmdlib.StatusDenied) != 0 {
			w.sendTr(priority, n.Endpoint, n.UserID, false, w.tr[n.Endpoint].StreamerAdded, data, notificationTag(n))
		} else {
			w.sendTr(priority, n.Endpoint, n.UserID, false, w.tr[n.Endpoint].AddError, data, notificationTag(n))
		}
	}
}

func (w *worker) downloadImages(notifications []db.Notification) map[string][]byte {
	images := map[string][]byte{}
	for _, n := range notifications {
		if n.ImageURL != "" {
			images[n.ImageURL] = nil
		}
	}
	for url := range images {
		images[url] = w.downloadImage(url)
	}
	return images
}

// notificationBatch pairs notifications with their fetched images.
type notificationBatch struct {
	notifications []db.Notification
	images        map[string][]byte
}

// enqueueNotifications queues a fetched batch of status notifications.
// Main goroutine only.
func (w *worker) enqueueNotifications(batch notificationBatch) {
	// NewNotifications orders by id.
	// On an idle scheduler the first push dispatches at once,
	// before its batch siblings reach the heap;
	// order by priority so that first pick is the best of the batch.
	sort.SliceStable(batch.notifications, func(i, j int) bool {
		return batch.notifications[i].Priority < batch.notifications[j].Priority
	})
	for _, n := range batch.notifications {
		w.notifyOfStatus(n.Priority, n, batch.images[n.ImageURL], n.Social)
	}
}

func (w *worker) trAdsSlice(endpoint string) []*cmdlib.Translation {
	var res []*cmdlib.Translation
	for _, v := range w.trAds[endpoint] {
		if v.NeedsCustomAffiliateLink && !w.customAffiliateLinkEnabled() {
			continue
		}
		res = append(res, v)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Key < res[j].Key })
	return res
}

func (w *worker) ad(priority db.Priority, endpoint string, userID db.UserID, command string) {
	trAds := w.trAdsSlice(endpoint)
	if len(trAds) == 0 {
		return
	}
	totalWeight := 0
	for _, a := range trAds {
		totalWeight += adWeight(a)
	}
	r := rand.Intn(totalWeight)
	for _, a := range trAds {
		r -= adWeight(a)
		if r < 0 {
			w.sendAdsTr(priority, endpoint, userID, false, a, command)
			return
		}
	}
}

func adWeight(tr *cmdlib.Translation) int {
	if tr.Weight <= 0 {
		return 1
	}
	return tr.Weight
}

// finalizeNotification clears a notification's row.
// It counts toward the user's report total
// only if the send reached a final result,
// not if the row was dropped before any send
// (no endpoint, unhandled status).
func (w *worker) finalizeNotification(notificationID int, userID db.UserID, hasFinalResult bool) {
	if notificationID == 0 {
		return
	}
	w.db.DeleteNotification(notificationID)
	if hasFinalResult {
		w.db.IncrementReports(userID)
	}
}

func (w *worker) notifyOfStatus(priority db.Priority, n db.Notification, image []byte, social bool) {
	if w.tr[n.Endpoint] == nil {
		// Orphaned endpoint (removed from config); drop the stranded row.
		w.finalizeNotification(n.ID, 0, false)
		return
	}
	ldbg("notifying of status of the streamer %s", n.Nickname)
	var timeDiff *timeDiff
	if n.TimeDiff != nil {
		temp := calcTimeDiff(*n.TimeDiff)
		timeDiff = &temp
	}
	data := tplData{
		"streamer":      n.Nickname,
		"streamer_link": w.streamerLink(n.Nickname, w.gatedAffiliate(n.AffiliateParams)),
		"time_diff":     timeDiff,
		"viewers":       n.Viewers,
		"show_kind":     n.ShowKind,
		"subject":       html.EscapeString(n.Subject),
	}
	notify := !n.SilentMessages
	switch n.Status {
	case cmdlib.StatusOnline:
		w.enqueueTr(priority, n.Endpoint, n.UserID, notify, w.tr[n.Endpoint].Online, data, image, notificationTag(n), n.ID)
	case cmdlib.StatusOffline:
		w.enqueueTr(priority, n.Endpoint, n.UserID, false, w.tr[n.Endpoint].Offline, data, nil, notificationTag(n), n.ID)
	case cmdlib.StatusDenied:
		w.enqueueTr(priority, n.Endpoint, n.UserID, false, w.tr[n.Endpoint].Denied, data, nil, notificationTag(n), n.ID)
	default:
		// No message for this status; clear the queue row so it doesn't strand.
		w.finalizeNotification(n.ID, 0, false)
	}
	if n.FieldsHint {
		// A requeued picture repeats its hint, as it repeats itself.
		w.enqueueTr(
			db.PriorityLow, n.Endpoint, n.UserID, false,
			w.tr[n.Endpoint].FieldsCustomizationHint, nil, nil,
			replyNth(n.Command, n.ReplySeq+1), 0)
	}
	if social && w.cfg.AdChancePercent > 0 && rand.Intn(100) < w.cfg.AdChancePercent {
		// Empty today: only a status notification is ever social,
		// and no command asks for one.
		// Carried rather than hardcoded, so an ad and the reply beside it
		// still agree should a notification ever have both.
		w.ad(priority, n.Endpoint, n.UserID, n.Command)
	}
}

func (w *worker) mustUserByID(userID db.UserID) (user db.User) {
	user, found := w.db.UserByID(userID)
	if !found {
		checkErr(fmt.Errorf("user not found: id %d", userID))
	}
	return
}

// customAffiliateLinkEnabled reports whether chats may set their own affiliate link.
// It needs affiliate_base, since only a Go-built link carries the params.
func (w *worker) customAffiliateLinkEnabled() bool {
	return w.cfg.EnableCustomAffiliateLink &&
		w.cfg.AffiliateBase != "" &&
		w.checker.Capabilities().SupportsCustomAffiliateLink
}

// affiliateBase is affiliate_base without its trailing slash, so every link spells it one way.
func (w *worker) affiliateBase() string {
	return strings.TrimRight(w.cfg.AffiliateBase, "/")
}

// siteHomeLink is the site's homepage link, affiliate_base over website_link.
func (w *worker) siteHomeLink() string {
	if w.cfg.AffiliateBase != "" {
		return w.affiliateBase()
	}
	return w.cfg.WebsiteLink
}

// streamerLinker returns a link builder that encodes the affiliate query once.
func (w *worker) streamerLinker(affiliate map[string]string) func(string) string {
	if w.cfg.AffiliateBase != "" {
		prefix := w.affiliateBase() + "/"
		suffix := ""
		if len(affiliate) > 0 {
			suffix = "?" + affiliateQuery(affiliate)
		}
		return func(streamer string) string {
			return "<a href='" + prefix + streamer + suffix + "'>" + streamer + "</a>"
		}
	}
	if w.legacyAffiliateTpl == nil {
		return func(streamer string) string { return streamer }
	}
	return func(streamer string) string {
		var b bytes.Buffer
		checkErr(w.legacyAffiliateTpl.Execute(&b, streamer))
		return b.String()
	}
}

// streamerLink renders one streamer's affiliate link as an HTML anchor.
func (w *worker) streamerLink(streamer string, affiliate map[string]string) string {
	return w.streamerLinker(affiliate)(streamer)
}

// affiliateQuery encodes affiliate params as an href-escaped URL query.
func affiliateQuery(affiliate map[string]string) string {
	values := url.Values{}
	for name, value := range affiliate {
		values.Set(name, value)
	}
	return html.EscapeString(values.Encode())
}

// gatedAffiliate returns a chat's own affiliate params, or nil when off.
func (w *worker) gatedAffiliate(custom map[string]string) map[string]string {
	if !w.customAffiliateLinkEnabled() {
		return nil
	}
	return custom
}

// gatedAffiliateForUser loads a recipient chat's affiliate params.
// Only a group or channel can hold them, so a private chat skips the lookup.
func (w *worker) gatedAffiliateForUser(m receivedMessage) map[string]string {
	if !isGroupOrChannel(m.chatID) || !w.customAffiliateLinkEnabled() {
		return nil
	}
	return w.mustUserByID(m.userID).AffiliateParams
}

// gatedAffiliateForChat is gatedAffiliateForUser for a caller that already holds the user.
func (w *worker) gatedAffiliateForChat(chatID int64, user db.User) map[string]string {
	if !isGroupOrChannel(chatID) || !w.customAffiliateLinkEnabled() {
		return nil
	}
	return user.AffiliateParams
}

func (w *worker) showWeek(m receivedMessage, nickname string) {
	if nickname != "" {
		nickname = w.checker.NicknamePreprocessing(nickname)
		if !w.checker.NicknameRegexp().MatchString(nickname) {
			w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].InvalidSymbols, tplData{"streamer": nickname})
			return
		}
		user := w.mustUserByID(m.userID)
		affiliate := w.gatedAffiliateForChat(m.chatID, user)
		loc, zone := w.chatLocation(user)
		hours, weekday := w.week(nickname, loc)
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].Week, tplData{
			"hours":         hours,
			"weekday":       int(weekday),
			"timezone":      zone,
			"streamer_link": w.streamerLink(nickname, affiliate),
		})
		return
	}
	streamers := w.db.StreamersForUser(m.endpoint, m.userID)
	if len(streamers) == 0 {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].ZeroSubscriptions, nil)
		return
	}
	user := w.mustUserByID(m.userID)
	link := w.streamerLinker(w.gatedAffiliateForChat(m.chatID, user))
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].WeekRetrieving, nil)
	m = m.next()
	ids := make([]int, len(streamers))
	for i, s := range streamers {
		ids[i] = s.ID
	}
	now := time.Now()
	loc, zone := w.chatLocation(user)
	hoursMap, weekday := w.weekForStreamers(ids, now, loc)
	statuses := w.db.UnconfirmedStatusesForUser(m.endpoint, m.userID)
	statusMap := make(map[string]db.Streamer, len(statuses))
	for _, s := range statuses {
		statusMap[s.Nickname] = s
	}
	var weeks []tplData
	var neverOnline []streamerListEntry
	nowUnix := int(now.Unix())
	for _, s := range streamers {
		hours := hoursMap[s.ID]
		if !slices.Contains(hours, true) {
			var td *timeDiff
			if st, ok := statusMap[s.Nickname]; ok {
				td = w.streamerTimeDiff(st, nowUnix)
			}
			neverOnline = append(neverOnline, streamerListEntry{
				Link:     link(s.Nickname),
				TimeDiff: td,
			})
			continue
		}
		weeks = append(weeks, tplData{
			"hours":         hours,
			"weekday":       int(weekday),
			"timezone":      zone,
			"streamer_link": link(s.Nickname),
		})
	}
	for chunk := range slices.Chunk(weeks, 10) {
		w.replyTr(m, db.PriorityLow, false, w.tr[m.endpoint].WeekChunk, tplData{"rows": chunk})
		m = m.next()
	}
	for chunk := range slices.Chunk(neverOnline, 50) {
		w.replyTr(m, db.PriorityLow, false, w.tr[m.endpoint].WeekNeverOnline, tplData{"streamers": chunk})
		m = m.next()
	}
}

func (w *worker) addStreamer(m receivedMessage, nickname string, referral bool) *int {
	if nickname == "" {
		tr := w.tr[m.endpoint].SyntaxAdd
		params := &renderParams{templates: w.tpl[m.endpoint], key: tr.Key}
		msg := params.asDeferredText(true, tr.DisablePreview, tr.Parse)
		// A private chat, the only place a web app button works.
		if !w.checker.Capabilities().UsesFixedListOnline() && !isGroupOrChannel(m.chatID) {
			msg.ReplyMarkup = &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{{
					{
						Text:   w.tr[m.endpoint].SearchButton.Str,
						WebApp: &models.WebAppInfo{URL: w.webAppURL(m.endpoint)},
					},
				}},
			}
		}
		w.replyMessage(m, db.PriorityHigh, msg)
		return nil
	}
	nickname = w.checker.NicknamePreprocessing(nickname)
	if !w.checker.NicknameRegexp().MatchString(nickname) {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].InvalidSymbols, tplData{"streamer": nickname})
		return nil
	}

	if w.db.SubscribedOrPending(m.endpoint, m.userID, nickname) {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AlreadyAdded, tplData{"streamer": nickname})
		return nil
	}
	subscriptionsNumber := w.db.SubscribedOrPendingCount(m.endpoint, m.userID)
	user := w.mustUserByID(m.userID)
	if subscriptionsNumber >= user.MaxSubs {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].NotEnoughSubscriptions, nil)
		w.subscriptionUsage(m.next(), true)
		return nil
	}
	streamer := w.db.MaybeStreamer(nickname)
	if streamer == nil {
		caps := w.checker.Capabilities()
		if !caps.SupportsQueryStatus && !caps.SupportsQueryFixedListStatuses {
			w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AddError, tplData{"streamer": nickname})
			return nil
		}
		// The confirmation lands later, so it takes the next number.
		w.db.AddPendingSubscription(m.userID, nickname, m.endpoint, referral, m.command, m.replySeq+1)
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].CheckingStreamer, nil)
		return nil
	}
	confirmedStatus := streamer.ConfirmedStatus
	if confirmedStatus != cmdlib.StatusOnline {
		confirmedStatus = cmdlib.StatusOffline
	}
	w.db.AddSubscription(m.userID, streamer.ID, m.endpoint)
	subscriptionsNumber++
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].StreamerAdded, tplData{"streamer": nickname})
	m = m.next()
	nots := []db.Notification{{
		Endpoint:   m.endpoint,
		UserID:     m.userID,
		StreamerID: &streamer.ID,
		Nickname:   nickname,
		Status:     confirmedStatus,
		TimeDiff:   w.streamerDuration(*streamer, m.timestamp),
		Social:     false,
		Priority:   db.PriorityHigh,
		Kind:       db.ReplyPacket,
		Command:    m.command,
		ReplySeq:   m.replySeq}}
	if subscriptionsNumber >= user.MaxSubs-w.cfg.HeavyUserRemainder {
		w.subscriptionUsage(m.next(), true)
	}
	w.db.StoreNotifications(nots)
	return &streamer.ID
}

func (w *worker) subscriptionUsage(m receivedMessage, ad bool) {
	subscriptionsNumber := w.db.SubscribedOrPendingCount(m.endpoint, m.userID)
	user := w.mustUserByID(m.userID)
	tr := w.tr[m.endpoint].SubscriptionUsage
	if ad {
		tr = w.tr[m.endpoint].SubscriptionUsageAd
	}
	w.replyTr(m, db.PriorityHigh, false, tr, tplData{
		"subscriptions_used":  subscriptionsNumber,
		"total_subscriptions": user.MaxSubs,
	})
}

func (w *worker) wantMore(m receivedMessage) {
	w.replyTr(m, db.PriorityHigh, false,
		w.tr[m.endpoint].WantMore, w.referralData(m.endpoint, m.userID))
}

// preCheckoutCommand names an admitted pre-checkout query.
const preCheckoutCommand = "pre_checkout"

// buyCallbackCommand names a tap on the buy menu, the funnel's first step.
const buyCallbackCommand = "buy_callback"

// invoiceCommand names the invoice event as a received message.
// Only a sent invoice is recorded, so the log counts invoices, not attempts.
const invoiceCommand = "invoice"

// paymentCommand names the credited-payment event,
// both as a received message and as what its replies answer.
const paymentCommand = "successful_payment"

// productSubs is the only product sold today.
// handlePreCheckoutQuery and handleSuccessfulPayment guard on it,
// so adding another product means extending those guards.
const productSubs = "subs"

func (w *worker) findSubsTier(count int) (botconfig.SubsTier, bool) {
	for _, t := range w.cfg.SubsTiers {
		if t.Count == count {
			return t, true
		}
	}
	return botconfig.SubsTier{}, false
}

func (w *worker) buySubs(m receivedMessage) {
	if len(w.cfg.SubsTiers) == 0 {
		return
	}
	tr := w.tr[m.endpoint].BuySubs
	params := &renderParams{templates: w.tpl[m.endpoint], key: tr.Key}
	buttonTpl := w.tr[m.endpoint].BuySubsPackageButton
	base := w.cfg.SubsTiers[0]
	buttons := make([][]models.InlineKeyboardButton, 0, len(w.cfg.SubsTiers))
	for _, t := range w.cfg.SubsTiers {
		discount := (t.Count*base.Cost - t.Cost*base.Count) * 100 / (t.Count * base.Cost)
		label := templateToString(w.tpl[m.endpoint], buttonTpl.Key, tplData{
			"count":    t.Count,
			"stars":    t.Cost,
			"discount": discount,
		})
		buttons = append(buttons, []models.InlineKeyboardButton{{
			Text:         label,
			CallbackData: fmt.Sprintf("buy:stars:%d", t.Count),
		}})
	}
	msg := params.asDeferredText(true, tr.DisablePreview, tr.Parse)
	msg.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: buttons}
	w.replyMessage(m, db.PriorityHigh, msg)
}

func (w *worker) sendSubsInvoice(m receivedMessage, tier botconfig.SubsTier) {
	// Derived, not passed: a chat id that disagreed with m.userID would send
	// the invoice to one user and attribute its payload and rows to another.
	chatID, ok := w.db.ChatIDForUser(m.userID)
	if !ok {
		lerr("cannot send invoice: no chat for user %d", m.userID)
		return
	}
	stars := tier.Cost
	// Invoice fields reach no chat, so they must name no command: the unbound writer fails here.
	invoiceTpl := w.tpl[m.endpoint]
	title := templateToString(
		invoiceTpl, w.tr[m.endpoint].BuySubsInvoiceTitle.Key, tplData{"count": tier.Count})
	description := templateToString(
		invoiceTpl, w.tr[m.endpoint].BuySubsInvoiceDescription.Key, tplData{"count": tier.Count})
	label := templateToString(
		invoiceTpl, w.tr[m.endpoint].BuySubsInvoiceLabel.Key, tplData{"count": tier.Count})
	payload := fmt.Sprintf("stars:%s:%d:%d", productSubs, chatID, tier.Count)
	ctx := context.Background()
	_, err := w.bots[m.endpoint].SendInvoice(ctx, &bot.SendInvoiceParams{
		ChatID:      chatID,
		Title:       title,
		Description: description,
		Payload:     payload,
		Currency:    "XTR",
		Prices:      []models.LabeledPrice{{Label: label, Amount: stars}},
	})
	if err != nil {
		lerr("cannot send invoice to %d: %v", chatID, err)
		// The invoice row is never written, but the tap that asked for it was,
		// so the reply answers that.
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].BuyInvoiceFailed, nil)
		return
	}
	// A second event, not the tap m carries, so logReceived is not the call:
	// it would name m.command, and the stamp is fresh, the invoice going out
	// later than the tap that asked for it.
	w.db.LogReceivedMessage(int(time.Now().Unix()), m.endpoint, m.userID, invoiceCommand)
}

// parseStarsPayload parses a "stars:<product>:<chat_id>:<count>" payload.
func parseStarsPayload(payload string) (product string, chatID int64, count int, ok bool) {
	parts := strings.Split(payload, ":")
	if len(parts) != 4 || parts[0] != "stars" || parts[1] == "" {
		return "", 0, 0, false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n < 1 {
		return "", 0, 0, false
	}
	return parts[1], id, n, true
}

func (w *worker) handleBuyCallback(endpoint string, chatID int64, data string) {
	if !w.cfg.BuySubsEnabled() {
		return
	}
	m, _ := w.newReceivedMessage(int(time.Now().Unix()), endpoint, chatID, "", "")
	parts := strings.SplitN(data, ":", 3)
	if len(parts) != 3 || parts[1] != "stars" {
		// A payload we cannot read is no funnel entry,
		// as handlePreCheckoutQuery skips a query it cannot parse,
		// so the menu it re-shows names nothing either.
		w.buySubs(m)
		return
	}
	// A menu tap is a command-like event, so it is recorded by name,
	// keeping the tap, invoice and payment steps joinable as one funnel.
	// A stale tier still counts: the user meant to buy and could not.
	m.command = buyCallbackCommand
	w.logReceived(m)
	w.handleStarsBuyCallback(m, parts[2])
}

func (w *worker) handleStarsBuyCallback(m receivedMessage, arg string) {
	count, err := strconv.Atoi(arg)
	tier, found := w.findSubsTier(count)
	if err != nil || !found {
		// Stale callback (tier removed or button data malformed): re-show menu.
		w.buySubs(m)
		return
	}
	w.sendSubsInvoice(m, tier)
}

func (w *worker) handlePreCheckoutQuery(endpoint string, q *models.PreCheckoutQuery) {
	ctx := context.Background()
	product, chatID, count, valid := parseStarsPayload(q.InvoicePayload)
	tier, found := w.findSubsTier(count)
	// PreCheckoutQuery has no chat, so whitelist-gate on the payload's chat id.
	allowed := valid && w.cfg.BuySubsEnabled() && w.cfg.ChatWhitelisted(chatID)
	if allowed {
		now := int(time.Now().Unix())
		w.db.LogReceivedMessage(now, endpoint, w.db.EnsureUser(chatID), preCheckoutCommand)
	}
	ok := allowed &&
		q.Currency == "XTR" &&
		product == productSubs &&
		found &&
		q.TotalAmount == tier.Cost
	params := &bot.AnswerPreCheckoutQueryParams{
		PreCheckoutQueryID: q.ID,
		OK:                 ok,
	}
	if !ok {
		params.ErrorMessage = templateToString(w.tpl[endpoint], w.tr[endpoint].BuyInvoiceExpired.Key, nil)
	}
	if _, err := w.bots[endpoint].AnswerPreCheckoutQuery(ctx, params); err != nil {
		lerr("cannot answer pre-checkout query %s: %v", q.ID, err)
	}
}

func (w *worker) handleSuccessfulPayment(endpoint string, chatID int64, p *models.SuccessfulPayment, now int) {
	product, payloadChatID, count, ok := parseStarsPayload(p.InvoicePayload)
	if !ok || payloadChatID != chatID {
		lerr("malformed successful payment payload %q for chat %d", p.InvoicePayload, chatID)
		return
	}
	if product != productSubs {
		// Only subscriptions are credited today; reject rather than miscredit.
		lerr("unsupported product %q in successful payment for chat %d", product, chatID)
		return
	}
	// Tiers changed since pre_checkout; credit anyway and log for reconciliation.
	if tier, found := w.findSubsTier(count); !found || p.TotalAmount != tier.Cost {
		lerr(
			"successful_payment tier/amount mismatch, crediting anyway: chat = %d, charge = %s, count = %d, paid = %d",
			chatID, p.TelegramPaymentChargeID, count, p.TotalAmount)
	}
	// Log the charge first: a failed credit rolls back the star_payments row.
	linf(
		"crediting successful_payment: chat = %d, charge = %s, product = %s, count = %d, stars = %d",
		chatID, p.TelegramPaymentChargeID, product, count, p.TotalAmount)
	// GrantStarPaymentSubs resolves the user inside the charge tx
	// and returns the id, so a rejected payload above or a duplicate charge
	// leaves no stray user.
	added, maxSubs, userID := w.db.GrantStarPaymentSubs(
		chatID,
		endpoint,
		p.TelegramPaymentChargeID,
		p.TotalAmount,
		product,
		count,
		p.InvoicePayload,
		now)
	if !added {
		lerr("duplicate successful_payment for chat %d, charge %s", chatID, p.TelegramPaymentChargeID)
		// A duplicate returns before the received row is written,
		// so this reply names nothing the received log could match.
		w.sendTr(db.PriorityHigh, endpoint, userID, false,
			w.tr[endpoint].BuyAlreadyCredited, nil, reply(""))
		return
	}
	// Only a credited charge reaches here.
	// A duplicate redelivery or unsupported product returned above,
	// so the log counts credited payments, not every receipt.
	w.db.LogReceivedMessage(now, endpoint, userID, paymentCommand)
	w.sendTr(db.PriorityHigh, endpoint, userID, false,
		w.tr[endpoint].SubsPurchased,
		tplData{
			"added":               count,
			"total_subscriptions": maxSubs,
		},
		reply(paymentCommand))
}

func (w *worker) settings(m receivedMessage) {
	subscriptionsNumber := w.db.SubscribedOrPendingCount(m.endpoint, m.userID)
	user := w.mustUserByID(m.userID)
	_, zone := w.chatLocation(user)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].Settings, tplData{
		"subscriptions_used":              subscriptionsNumber,
		"total_subscriptions":             user.MaxSubs,
		"show_images":                     user.ShowImages,
		"offline_notifications_supported": w.cfg.OfflineNotifications,
		"offline_notifications":           user.OfflineNotifications,
		"subject_supported":               w.checker.Capabilities().SupportsSubject,
		"show_subject":                    user.ShowSubject,
		"silent_messages":                 user.SilentMessages,
		"in_group":                        isGroup(user),
		"member_subscriptions":            user.MemberSubscriptions,
		"timezone":                        zone,
		"timezone_set":                    user.Timezone != nil,
		"can_manage_affiliate":            w.customAffiliateLinkEnabled() && isGroupOrChannel(user.ChatID),
		"affiliate_params":                w.gatedAffiliate(user.AffiliateParams),
	})
}

func (w *worker) enableImages(m receivedMessage, showImages bool) {
	w.db.SetShowImages(m.userID, showImages)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].OK, nil)
}

func (w *worker) enableOfflineNotifications(m receivedMessage, offlineNotifications bool) {
	w.db.SetOfflineNotifications(m.userID, offlineNotifications)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].OK, nil)
}

func (w *worker) enableSubject(m receivedMessage, showSubject bool) {
	w.db.SetShowSubject(m.userID, showSubject)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].OK, nil)
}

func (w *worker) enableSilentMessages(m receivedMessage, silentMessages bool) {
	w.db.SetSilentMessages(m.userID, silentMessages)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].OK, nil)
}

// enableMemberSubscriptions opens the chat's streamer list to its members, or closes it again.
// A private chat and a channel have no posting members, so they are told where the setting works.
func (w *worker) enableMemberSubscriptions(m receivedMessage, memberSubscriptions bool) {
	if !isGroup(w.mustUserByID(m.userID)) {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].GroupsOnly, nil)
		return
	}
	w.db.SetMemberSubscriptions(m.userID, memberSubscriptions)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].OK, nil)
}

// utcZone is what a chat that set no timezone reads and prints as.
const utcZone = "UTC"

// parseTimezone resolves a typed IANA zone name and returns the spelling to store.
// Only the map decides, setZoneNames having loaded every zone in it once at startup,
// so what the page is offered and what a save accepts cannot come apart.
func (w *worker) parseTimezone(name string) (*time.Location, string, bool) {
	loc, known := w.zoneNames[strings.ToLower(strings.TrimSpace(name))]
	if !known {
		return nil, "", false
	}
	return loc, loc.String(), true
}

// zoneOrDefault resolves a name the way a chat's own zone is resolved:
// the offered list first, then the database this binary carries,
// which may still hold a name the server has stopped offering.
// known is false when neither holds it and the default answers.
func (w *worker) zoneOrDefault(name string) (*time.Location, string, bool) {
	if loc, zone, ok := w.parseTimezone(name); ok {
		return loc, zone, true
	}
	// LoadLocation answers an empty name with UTC and Local with the host's own,
	// neither of which is a chat's zone.
	if name != "" && !strings.EqualFold(name, "local") {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, name, true
		}
	}
	return time.UTC, utcZone, false
}

// chatLocation resolves a chat's timezone, UTC when it set none.
// A name it cannot resolve is reported and fallen back from, and left in the column.
// Main goroutine only, as every caller is a command handler.
func (w *worker) chatLocation(user db.User) (*time.Location, string) {
	if user.Timezone == nil {
		return time.UTC, utcZone
	}
	loc, zone, known := w.zoneOrDefault(*user.Timezone)
	if !known {
		// Reported, never repaired: which names resolve turns on the tzdata this binary carries,
		// so a rollback to one that predates a zone would have a listing destroy a chat's own choice.
		lerr("stored timezone will not load, falling back: @uid = %d, timezone = %s",
			user.UserID, *user.Timezone)
	}
	return loc, zone
}

// replyTimezone answers with the chat's zone.
// help carries the ways to change it, the instructions and the picker alike,
// and belongs to the bare command, which asks what the zone is:
// one that has just set or cleared it is answered with the zone alone.
func (w *worker) replyTimezone(m receivedMessage, zone string, help bool) {
	tr := w.tr[m.endpoint].Timezone
	params := &renderParams{
		templates: w.tpl[m.endpoint],
		key:       tr.Key,
		data:      tplData{"timezone": zone, "help": help},
	}
	// Silent, as a replyTr answer is. The bare /add reply, which carries a picker button too,
	// notifies instead: whichever is right, the two should move together.
	msg := params.asDeferredText(false, tr.DisablePreview, tr.Parse)
	// A private chat, the only place a web app button works.
	if help && !isGroupOrChannel(m.chatID) {
		msg.ReplyMarkup = &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{{
				{
					Text:   w.tr[m.endpoint].TimezoneButton.Str,
					WebApp: &models.WebAppInfo{URL: w.timezoneAppURL(m.endpoint, zone)},
				},
			}},
		}
	}
	w.replyMessage(m, db.PriorityHigh, msg)
}

// setTimezone shows the chat's zone or sets it from a typed IANA name.
func (w *worker) setTimezone(m receivedMessage, arg string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		_, zone := w.chatLocation(w.mustUserByID(m.userID))
		w.replyTimezone(m, zone, true)
		return
	}
	_, zone, ok := w.parseTimezone(arg)
	if !ok {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].TimezoneInvalid, nil)
		return
	}
	w.db.SetTimezone(m.userID, zone)
	w.replyTimezone(m, zone, false)
}

// resetTimezone clears the chat's zone, leaving the grid on the default.
// Typing the default's name does the same, but nothing in the chat says so,
// where a command sits in the settings listing beside the one that set it.
func (w *worker) resetTimezone(m receivedMessage) {
	w.db.SetTimezone(m.userID, "")
	w.replyTimezone(m, utcZone, false)
}

// modelPayloadPrefix prefixes a deep-link payload naming a model to subscribe to.
// The admin gate names it too, so the gate and the parser share one grammar.
const modelPayloadPrefix = "m-"

// setAffiliate handles /affiliate: show or set the chat's affiliate link.
func (w *worker) setAffiliate(m receivedMessage, arg string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		user := w.mustUserByID(m.userID)
		if len(user.AffiliateParams) == 0 {
			w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AffiliateInstructions, nil)
			return
		}
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AffiliateStatus,
			w.affiliateStatusData(user.AffiliateParams))
		return
	}
	params, ok := w.checker.ParseAffiliateParams(arg)
	if !ok {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AffiliateInvalid, nil)
		return
	}
	w.db.SetAffiliateParams(m.userID, params)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AffiliateStatus,
		w.affiliateStatusData(params))
}

// resetAffiliate drops a chat's custom affiliate link.
func (w *worker) resetAffiliate(m receivedMessage) {
	if len(w.mustUserByID(m.userID).AffiliateParams) == 0 {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AffiliateNothingToReset, nil)
		return
	}
	w.db.SetAffiliateParams(m.userID, nil)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].OK, nil)
}

// affiliateStatusData builds the affiliate status template data.
func (w *worker) affiliateStatusData(params map[string]string) tplData {
	return tplData{
		"affiliate_id":    w.checker.AffiliateID(params),
		"affiliate_base":  w.affiliateBase(),
		"affiliate_query": affiliateQuery(params),
	}
}

// chatMemberTimeout caps the admin lookup, which blocks the update loop.
const chatMemberTimeout = 2 * time.Second

// getChatMember asks Telegram, and is what worker.chatMember holds outside tests.
func (w *worker) getChatMember(endpoint string, chatID, userID int64) (*models.ChatMember, error) {
	ctx, cancel := context.WithTimeout(context.Background(), chatMemberTimeout)
	defer cancel()
	return w.bots[endpoint].GetChatMember(ctx, &bot.GetChatMemberParams{
		ChatID: chatID,
		UserID: userID,
	})
}

// sender is who sent an update, resolved once so the dispatch layer needs no raw update.
// The chat it acts in stays on receivedMessage, which pairs it with the user.
type sender struct {
	channelPost bool
	senderChat  *models.Chat
	from        *models.User
}

// senderOf resolves an update's sender.
func senderOf(u *models.Update) sender {
	s := sender{channelPost: u.ChannelPost != nil}
	if u.Message != nil {
		s.senderChat = u.Message.SenderChat
		s.from = u.Message.From
	}
	return s
}

// senderIsGroupAdmin reports whether the sender is a group admin, failing closed.
func (w *worker) senderIsGroupAdmin(endpoint string, chatID int64, snd sender) bool {
	if snd.channelPost {
		return true // a channel post already requires admin post rights
	}
	if snd.senderChat != nil && snd.senderChat.ID == chatID {
		return true // an anonymous admin posts as the chat itself
	}
	if snd.senderChat != nil {
		// Any channel owner can post under the channel's identity, which hides the human,
		// so a channel identity cannot pass an admin gate, admin behind it or not.
		return false
	}
	if snd.from == nil {
		return false
	}
	if w.shuttingDown.Load() {
		// The shutdown drain replays buffered updates on a fixed budget,
		// which a member lookup would burn for replies that can no longer send.
		return false
	}
	member, err := w.chatMember(endpoint, chatID, snd.from.ID)
	if err != nil {
		lerr("cannot get chat member for the admin gate: chat = %d, @uid = %d, %v", chatID, snd.from.ID, err)
		return false
	}
	if member == nil {
		return false // a null result decodes without error
	}
	switch member.Type {
	case models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator:
		return true
	}
	return false
}

// isGroupOrChannel reports whether a chat id belongs to a group or a channel.
// Telegram gives those negative ids and private chats positive ones.
func isGroupOrChannel(chatID int64) bool {
	return chatID < 0
}

// isGroup reports whether a chat has members who can post.
// A channel takes posts from its admins alone, and an id cannot tell one from the other,
// so the chat type decides, and a missing one reads as a group.
func isGroup(user db.User) bool {
	if !isGroupOrChannel(user.ChatID) {
		return false
	}
	return user.ChatType == nil || *user.ChatType != string(models.ChatTypeChannel)
}

// botMentionIfNeeded is the bot's @name in a group or channel, nothing in a private chat.
func (w *worker) botMentionIfNeeded(endpoint string, chatID int64) string {
	if isGroupOrChannel(chatID) {
		return w.botMention(endpoint)
	}
	return ""
}

// botMention is the bot's @name: appended to a command the bot prints,
// stripped from a command it receives. Both sides must spell it the same.
func (w *worker) botMention(endpoint string) string {
	return "@" + w.botNames[endpoint]
}

func (w *worker) removeStreamer(m receivedMessage, nickname string) {
	if nickname == "" {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].SyntaxRemove, nil)
		return
	}
	nickname = w.checker.NicknamePreprocessing(nickname)
	if !w.checker.NicknameRegexp().MatchString(nickname) {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].InvalidSymbols, tplData{"streamer": nickname})
		return
	}
	if !w.db.SubscribedOrPending(m.endpoint, m.userID, nickname) {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].StreamerNotInList, tplData{"streamer": nickname})
		return
	}
	w.db.RemoveSubscription(m.userID, nickname, m.endpoint)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].StreamerRemoved, tplData{"streamer": nickname})
}

func (w *worker) sureRemoveAll(m receivedMessage) {
	w.db.RemoveAllSubscriptions(m.userID, m.endpoint)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AllStreamersRemoved, nil)
}

// calcTimeDiff calculates time difference ignoring summer time and leap seconds
func calcTimeDiff(dur int) timeDiff {
	d := (time.Duration(dur) * time.Second).Nanoseconds()
	var diff timeDiff
	day := int64(time.Hour * 24)
	diff.Days = int(d / day)
	d -= int64(diff.Days) * day
	diff.Hours = int(d / int64(time.Hour))
	d -= int64(diff.Hours) * int64(time.Hour)
	diff.Minutes = int(d / int64(time.Minute))
	d -= int64(diff.Minutes) * int64(time.Minute)
	diff.Seconds = int(d / int64(time.Second))
	d -= int64(diff.Seconds) * int64(time.Second)
	diff.Nanoseconds = int(d)
	return diff
}

func chunkStreamers(xs []db.Streamer, chunkSize int) [][]db.Streamer {
	if len(xs) == 0 {
		return nil
	}
	divided := make([][]db.Streamer, (len(xs)+chunkSize-1)/chunkSize)
	prev := 0
	i := 0
	till := len(xs) - chunkSize
	for prev < till {
		next := prev + chunkSize
		divided[i] = xs[prev:next]
		prev = next
		i++
	}
	divided[i] = xs[prev:]
	return divided
}

func listStreamersSortWeight(s cmdlib.StatusKind) int {
	switch s {
	case cmdlib.StatusOnline:
		return 0
	default:
		return 1
	}
}

func (w *worker) listStreamers(m receivedMessage) {
	statuses := w.db.UnconfirmedStatusesForUser(m.endpoint, m.userID)
	if len(statuses) == 0 {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].ZeroSubscriptions, nil)
		return
	}
	sort.SliceStable(statuses, func(i, j int) bool {
		return listStreamersSortWeight(statuses[i].UnconfirmedStatus) < listStreamersSortWeight(statuses[j].UnconfirmedStatus)
	})
	link := w.streamerLinker(w.gatedAffiliateForUser(m))
	chunks := chunkStreamers(statuses, 50)
	for _, chunk := range chunks {
		var online, offline []streamerListEntry
		for _, s := range chunk {
			entry := streamerListEntry{
				Link:     link(s.Nickname),
				TimeDiff: w.streamerTimeDiff(s, m.timestamp),
			}
			switch s.UnconfirmedStatus {
			case cmdlib.StatusOnline:
				online = append(online, entry)
			default:
				offline = append(offline, entry)
			}
		}
		tplData := tplData{"online": online, "offline": offline}
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].List, tplData)
		m = m.next()
	}
}

// streamerDuration calculates time since last status change for display.
// Returns duration since going offline (if offline)
// or since going online (if online).
func (w *worker) streamerDuration(c db.Streamer, now int) *int {
	if c.UnconfirmedStatus == cmdlib.StatusOnline || c.UnconfirmedStatus == cmdlib.StatusOffline {
		if c.UnconfirmedTimestamp != 0 && c.PrevUnconfirmedStatus != cmdlib.StatusUnknown {
			dur := now - c.UnconfirmedTimestamp
			return &dur
		}
	}
	return nil
}

func (w *worker) streamerTimeDiff(c db.Streamer, now int) *timeDiff {
	dur := w.streamerDuration(c, now)
	if dur != nil {
		td := calcTimeDiff(*dur)
		return &td
	}
	return nil
}

func (w *worker) downloadImage(url string) []byte {
	start := time.Now()
	imageBytes, err := w.downloadImageInternal(url)
	elapsed := time.Since(start)
	if err != nil {
		linf("cannot download image, %v", err)
	}
	w.imageDownloadLogs <- imageDownloadLog{success: err == nil, durationMs: int(elapsed.Milliseconds())}
	return imageBytes
}

func (w *worker) downloadImageInternal(url string) ([]byte, error) {
	resp, err := w.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("cannot query the image %s, %v", url, err)
	}
	defer cmdlib.CloseBody(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("cannot download the image %s, status code %v", url, resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cannot read the image %s, %v", url, err)
	}
	data := buf.Bytes()
	_, _, err = image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cannot decode the image %s, %v", url, err)
	}
	return data, nil
}

func (w *worker) listOnlineStreamers(m receivedMessage) {
	statuses := w.db.UnconfirmedStatusesForUser(m.endpoint, m.userID)
	var online []db.Streamer
	for _, s := range statuses {
		if s.UnconfirmedStatus == cmdlib.StatusOnline {
			online = append(online, s)
		}
	}
	if len(online) == 0 {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].NoOnlineStreamers, nil)
		return
	}
	// A group or channel, where a pile of pictures is worth capping.
	if len(online) > w.cfg.MaxSubscriptionsForPics && isGroupOrChannel(m.chatID) {
		data := tplData{"max_subs": w.cfg.MaxSubscriptionsForPics}
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].TooManySubscriptionsForPics, data)
		return
	}
	user := w.mustUserByID(m.userID)
	var nots []db.Notification
	for _, s := range online {
		info := w.unconfirmedOnlineStreamers[s.Nickname]
		not := db.Notification{
			Priority:   db.PriorityHigh,
			Endpoint:   m.endpoint,
			UserID:     m.userID,
			StreamerID: &s.ID,
			Nickname:   s.Nickname,
			Status:     cmdlib.StatusOnline,
			ImageURL:   info.ImageURL,
			Viewers:    info.Viewers,
			ShowKind:   info.ShowKind,
			TimeDiff:   w.streamerDuration(s, m.timestamp),
			Kind:       db.ReplyPacket,
			Command:    m.command,
			ReplySeq:   m.replySeq,
		}
		if user.ShowSubject {
			not.Subject = info.Subject
		}
		nots = append(nots, not)
		m = m.next()
	}
	// Carried by the last picture, so the hint is queued behind them all
	// rather than sent now and delivered a tick ahead of them.
	nots[len(nots)-1].FieldsHint = true
	w.db.StoreNotifications(nots)
}

func (w *worker) week(nickname string, loc *time.Location) ([]bool, time.Weekday) {
	streamer := w.db.MaybeStreamer(nickname)
	if streamer == nil {
		return nil, 0
	}
	result, weekday := w.weekForStreamers([]int{streamer.ID}, time.Now(), loc)
	return result[streamer.ID], weekday
}

func onlineCells(
	changesMap map[int][]db.StatusChange,
	from int,
	to int,
	cellSeconds int,
) map[int][]bool {
	result := make(map[int][]bool)
	for streamerID, changes := range changesMap {
		cells := make([]bool, (to-from+cellSeconds-1)/cellSeconds)
		for i, c := range changes[:len(changes)-1] {
			if c.Status == cmdlib.StatusOnline {
				begin := (c.Timestamp - from) / cellSeconds
				if begin < 0 {
					begin = 0
				}
				end := (changes[i+1].Timestamp - from + cellSeconds - 1) / cellSeconds
				for j := begin; j < end; j++ {
					cells[j] = true
				}
			}
		}
		result[streamerID] = cells
	}
	return result
}

// weekHours is what the grid holds: the seven rows of 24 cells the week template prints.
const weekHours = 7 * 24

/*
weekWindow is the span the grid covers, from the start of the day six days back through now,
and the weekday its first row carries.

Every step keeps a zone's shifts out of the arithmetic they would corrupt:

	the date is walked in UTC, where no shift can move it;
	the clock is placed in loc after, and a day whose midnight a shift skips starts at the shift;
	the weekday is read off the date rather than off an instant a shift may have normalized;
	a fall-back stretching the week past weekHours drops its oldest hour, never opening an eighth row.

A row is 24 cells whatever the day held, so a shift inside the window moves the later rows
off their labels by however much it moved the clock.
Where that is not a whole hour, as in Australia/Lord_Howe, Pacific/Chatham and Iran,
the cells stop lining up with the clock at all and each straddles two labelled hours.
Snapping the start would not mend it, the shift falling in the middle of the window.
*/
func weekWindow(now time.Time, loc *time.Location) (from, to int, weekday time.Weekday) {
	year, month, day := now.In(loc).Date()
	first := time.Date(year, month, day, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -6)
	year, month, day = first.Date()
	start := time.Date(year, month, day, 0, 0, 0, 0, loc)
	for hour := 1; hour <= 3 && start.Day() != day; hour++ {
		start = time.Date(year, month, day, hour, 0, 0, 0, loc)
	}
	from, to = int(start.Unix()), int(now.Unix())
	// Whole hours, so the cells the grid prints stay aligned to the clock they are labelled with.
	if cells := (to - from + 3599) / 3600; cells > weekHours {
		from += (cells - weekHours) * 3600
	}
	return from, to, first.Weekday()
}

// weekForStreamers builds the hourly grid of the last seven days in loc.
func (w *worker) weekForStreamers(
	streamerIDs []int,
	now time.Time,
	loc *time.Location,
) (map[int][]bool, time.Weekday) {
	from, to, weekday := weekWindow(now, loc)
	changesMap := w.db.ChangesFromToForStreamers(streamerIDs, from, to)
	return onlineCells(changesMap, from, to, 3600), weekday
}

func (w *worker) feedback(m receivedMessage, text string) {
	if text == "" {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].SyntaxFeedback, nil)
		return
	}
	w.db.AddFeedback(m.endpoint, m.userID, text, m.timestamp)
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].Feedback, nil)
	user := w.mustUserByID(m.userID)
	if !user.Blacklist {
		finalText := fmt.Sprintf("Feedback from %d: %s", user.ChatID, text)
		// The owner issued no command, so this copy answers none,
		// the same shape direct and broadcast use for a third party.
		w.sendText(
			db.PriorityHigh, m.endpoint, w.ownerUserID, true, true, cmdlib.ParseRaw, finalText,
			unprompted(db.MessagePacket))
	}
}

func (w *worker) performanceStat(endpoint string, arguments string) {
	parts := strings.Fields(arguments)
	sortBy := "total"
	if len(parts) > 0 && (parts[0] == "avg" || parts[0] == "total") {
		sortBy = parts[0]
		parts = parts[1:]
	}
	n := int64(10)
	if len(parts) > 1 {
		w.replyToOwner(endpoint, "usage: /performance [avg|total] [count]")
		return
	}
	if len(parts) == 1 {
		var err error
		n, err = strconv.ParseInt(parts[0], 10, 32)
		if err != nil {
			w.replyToOwner(endpoint, "cannot parse the count")
			return
		}
		if n <= 0 {
			w.replyToOwner(endpoint, "the count must be positive")
			return
		}
	}
	durations := w.db.Durations
	var queries []string
	for x := range durations {
		queries = append(queries, x)
	}
	if sortBy == "avg" {
		sort.SliceStable(queries, func(i, j int) bool {
			return durations[queries[i]].Avg > durations[queries[j]].Avg
		})
	} else {
		sort.SliceStable(queries, func(i, j int) bool {
			return durations[queries[i]].Total() > durations[queries[j]].Total()
		})
	}
	for i, x := range queries {
		if n == 0 {
			return
		}
		lines := []string{
			fmt.Sprintf("<b>Desc</b>: %s", html.EscapeString(x)),
			fmt.Sprintf("<b>Total</b>: %d", int(durations[x].Avg*float64(durations[x].Count)*1000.)),
			fmt.Sprintf("<b>Avg</b>: %d", int(durations[x].Avg*1000.)),
			fmt.Sprintf("<b>Count</b>: %d", durations[x].Count),
		}
		entry := strings.Join(lines, "\n")
		w.replyToOwnerNth(endpoint, i, cmdlib.ParseHTML, entry)
		n--
	}
}

// broadcast sends text to every private subscriber.
// BroadcastUsers returns user ids directly.
// The loop no longer resolves chat id -> user id here
// only for trySend to resolve it back to chat id at dispatch.
// That dispatch-time ChatIDForUser is deliberate — single source of truth,
// migration-safe — and stays; we drop only the redundant enqueue-time lookup.
func (w *worker) broadcast(endpoint string, text string) {
	if text == "" {
		return
	}
	ldbg("broadcasting")
	users := w.db.BroadcastUsers(endpoint)
	for _, userID := range users {
		w.sendText(db.PriorityLow, endpoint, userID, true, false, cmdlib.ParseRaw, text, unprompted(db.MessagePacket))
	}
	// The ack queues at the broadcast's own priority, behind its last message.
	// Same-priority dispatch is FIFO only while every recipient stays healthy;
	// a stalled or cooling chat sinks behind the ack and is still pending when it sends.
	// So "OK" means the queue ran past the broadcast, not that every copy landed.
	// replyToOwner would jump the queue and say even less.
	w.sendText(db.PriorityLow, endpoint, w.ownerUserID, false, true, cmdlib.ParseRaw, "OK", reply(""))
}

func (w *worker) direct(endpoint string, arguments string) {
	parts := strings.SplitN(arguments, " ", 2)
	if len(parts) < 2 {
		w.replyToOwner(endpoint, "usage: /direct chatID text")
		return
	}
	whom, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		w.replyToOwner(endpoint, "first argument is invalid")
		return
	}
	text := parts[1]
	if text == "" {
		return
	}
	// User, not EnsureUser: a bad owner arg must not materialize a stray row.
	// A private chat that can be messaged has a row anyway (it started the bot);
	// the only gap is a group the bot is in that has never sent a command.
	user, found := w.db.User(whom)
	if !found {
		w.replyToOwner(endpoint, "no such user")
		return
	}
	w.sendText(db.PriorityHigh, endpoint, user.UserID, true, false, cmdlib.ParseRaw, text, unprompted(db.MessagePacket))
	w.replyToOwner(endpoint, "OK")
}

func (w *worker) blacklist(endpoint string, arguments string) {
	whom, err := strconv.ParseInt(arguments, 10, 64)
	if err != nil {
		w.replyToOwner(endpoint, "first argument is invalid")
		return
	}
	// EnsureUser creates the row when the chat is unknown,
	// so blacklisting an unseen chat materializes a (blacklisted) row.
	// Deliberate: pre-blacklisting before the chat ever starts the bot works.
	w.db.BlacklistUser(w.db.EnsureUser(whom))
	w.replyToOwner(endpoint, "OK")
}

// seq numbers the first chunk, so a listing that follows another reply
// continues its numbering.
func (w *worker) sendPolledList(endpoint string, now int, seq int) {
	polled := w.db.PolledStreamersWithStatus()
	if len(polled) == 0 {
		w.replyToOwnerNth(endpoint, seq, cmdlib.ParseRaw, "no polled streamers")
		return
	}
	// An owner listing goes to a private chat, which cannot set affiliate params.
	link := w.streamerLinker(nil)
	chunks := chunkStreamers(polled, 50)
	for i, chunk := range chunks {
		var online, offline []streamerListEntry
		for _, s := range chunk {
			entry := streamerListEntry{
				Link:     link(s.Nickname),
				TimeDiff: w.streamerTimeDiff(s, now),
			}
			switch s.UnconfirmedStatus {
			case cmdlib.StatusOnline:
				online = append(online, entry)
			default:
				offline = append(offline, entry)
			}
		}
		w.sendTr(
			db.PriorityHigh, endpoint, w.ownerUserID, false, w.tr[endpoint].List,
			tplData{"online": online, "offline": offline}, replyNth("", seq+i))
	}
}

func (w *worker) poll(endpoint string, arguments string) {
	caps := w.checker.Capabilities()
	if caps.UsesFixedListOnline() || !caps.SupportsQueryStatus {
		w.replyToOwner(endpoint, "checker does not support per-streamer polling")
		return
	}
	parts := strings.Fields(arguments)
	if len(parts) != 2 {
		w.replyToOwner(endpoint, "expecting <streamer> <on|off>")
		if len(parts) == 0 {
			w.sendPolledList(endpoint, int(time.Now().Unix()), 1)
		}
		return
	}
	nickname := w.checker.NicknamePreprocessing(parts[0])
	if !w.checker.NicknameRegexp().MatchString(nickname) {
		w.replyToOwner(endpoint, "invalid nickname")
		return
	}
	var on bool
	switch parts[1] {
	case "on":
		on = true
	case "off":
		on = false
	default:
		w.replyToOwner(endpoint, "second argument must be on or off")
		return
	}
	if !w.db.SetPoll(nickname, on) {
		w.replyToOwner(endpoint, "no such streamer")
		return
	}
	w.replyToOwner(endpoint, "OK")
}

// webAppBase is where an endpoint's pages are served from.
// checkConfig holds webhook_domain to a bare host, which is what makes this a plain join:
// a scheme written into the config would land a second one here.
func (w *worker) webAppBase(endpoint string) string {
	return "https://" + w.cfg.Endpoints[endpoint].WebhookDomain
}

func (w *worker) webAppURL(endpoint string) string {
	return w.webAppBase(endpoint) + "/apps/add?endpoint=" + endpoint
}

// timezoneAppURL carries the chat's zone to the page, which has no way to ask for it.
// It is a display hint: the submit decides what the chat stores.
func (w *worker) timezoneAppURL(endpoint, current string) string {
	return w.webAppBase(endpoint) +
		"/apps/timezone?endpoint=" + endpoint + "&current=" + url.QueryEscape(current)
}

func (w *worker) parseInitData(initData string, botToken string) (url.Values, bool) {
	values, err := url.ParseQuery(initData)
	if err != nil {
		lerr("search auth: cannot parse init data: %v", err)
		return nil, false
	}
	hash := values.Get("hash")
	if hash == "" {
		lerr("search auth: empty hash, initData length: %d", len(initData))
		return nil, false
	}
	values.Del("hash")
	authDateStr := values.Get("auth_date")
	authDate, err := strconv.ParseInt(authDateStr, 10, 64)
	if err != nil {
		lerr("search auth: cannot parse auth_date: %v", err)
		return nil, false
	}
	if time.Now().Unix()-authDate > 3600 {
		lerr("search auth: auth_date too old: %d", authDate)
		return nil, false
	}
	var keys []string
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+values.Get(k))
	}
	dataCheckString := strings.Join(parts, "\n")
	secretKey := hmac.New(sha256.New, []byte("WebAppData"))
	secretKey.Write([]byte(botToken))
	h := hmac.New(sha256.New, secretKey.Sum(nil))
	h.Write([]byte(dataCheckString))
	hashBytes, err := hex.DecodeString(hash)
	if err != nil || !hmac.Equal(h.Sum(nil), hashBytes) {
		lerr("search auth: hash mismatch, keys: %v", keys)
		return nil, false
	}
	return values, true
}

func (w *worker) handleWebApp(rw http.ResponseWriter, r *http.Request) {
	endpoint := r.URL.Query().Get("endpoint")
	if _, ok := w.cfg.Endpoints[endpoint]; !ok {
		http.Error(rw, "bad endpoint", http.StatusBadRequest)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	tr := w.tr[endpoint]
	data := struct {
		Header      string
		Placeholder string
		NoResults   string
		Failed      string
		FailedToAdd string
	}{
		Header:      tr.SearchHeader.Str,
		Placeholder: tr.SearchPlaceholder.Str,
		NoResults:   tr.SearchNoResults.Str,
		Failed:      tr.SearchFailed.Str,
		FailedToAdd: tr.SearchFailedToAdd.Str,
	}
	err := w.searchHTML.Execute(rw, data)
	if err != nil {
		lerr("cannot write web app response, %v", err)
	}
}

func (w *worker) handleTimezoneApp(rw http.ResponseWriter, r *http.Request) {
	endpoint := r.URL.Query().Get("endpoint")
	if _, ok := w.cfg.Endpoints[endpoint]; !ok {
		http.Error(rw, "bad endpoint", http.StatusBadRequest)
		return
	}
	// The page is served before any init data is read, so the query names the zone, not the chat.
	// A zone this binary cannot load reads as the default.
	_, current, _ := w.zoneOrDefault(r.URL.Query().Get("current"))
	// The page offers what the bot accepts, rather than what the browser happens to know:
	// two lists would let a row be tapped and then refused by the save it was offered for.
	zones := make([]string, 0, len(w.zoneNames))
	for _, loc := range w.zoneNames {
		zones = append(zones, loc.String())
	}
	sort.Strings(zones)
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	tr := w.tr[endpoint]
	data := struct {
		Header      string
		Current     string
		CurrentZone string
		Detected    string
		Placeholder string
		NoResults   string
		Failed      string
		Zones       []string
	}{
		Header:      tr.TimezoneAppHeader.Str,
		Current:     tr.TimezoneAppCurrent.Str,
		CurrentZone: current,
		Detected:    tr.TimezoneAppDetected.Str,
		Placeholder: tr.TimezoneAppPlaceholder.Str,
		NoResults:   tr.TimezoneAppNoResults.Str,
		Failed:      tr.TimezoneAppFailed.Str,
		Zones:       zones,
	}
	err := w.timezoneHTML.Execute(rw, data)
	if err != nil {
		lerr("cannot write timezone app response, %v", err)
	}
}

// webAppUserID reads the caller's id from signed web app init data.
// Both endpoints of the web app read it the same way,
// so neither can drift into a different idea of who is identifiable.
func webAppUserID(values url.Values) (int64, bool) {
	var user struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal([]byte(values.Get("user")), &user) != nil || user.ID == 0 {
		return 0, false
	}
	return user.ID, true
}

// searchCaller identifies the caller of a web app search,
// answering with an HTTP status when it may not search at all.
// An unidentifiable caller is rejected as handleWebAppAdd rejects one,
// and a chat outside the whitelist is refused like every inbound path.
// Both are decided before the search term, so the endpoint answers one way.
func (w *worker) searchCaller(values url.Values) (chatID int64, status int) {
	userID, ok := webAppUserID(values)
	if !ok {
		return 0, http.StatusBadRequest
	}
	if !w.cfg.ChatWhitelisted(userID) {
		return userID, http.StatusForbidden
	}
	return userID, 0
}

func (w *worker) handleSearch(rw http.ResponseWriter, r *http.Request) {
	endpoint := r.URL.Query().Get("endpoint")
	if _, ok := w.cfg.Endpoints[endpoint]; !ok {
		lerr("search: bad endpoint %q", endpoint)
		http.Error(rw, "bad endpoint", http.StatusBadRequest)
		return
	}
	initData := r.Header.Get("X-Init-Data")
	values, ok := w.parseInitData(initData, string(w.cfg.Endpoints[endpoint].BotToken))
	if !ok {
		lerr("search: invalid init data for endpoint %s", endpoint)
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
		return
	}
	chatID, status := w.searchCaller(values)
	if status != 0 {
		// Debug, not info: a typeahead endpoint refuses once per keystroke.
		ldbg("search refused: chat = %d, status = %d", chatID, status)
		http.Error(rw, http.StatusText(status), status)
		return
	}
	term := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("term")))
	var results []string
	if len(term) >= 1 && len(term) <= 32 {
		req := searchRequest{
			endpoint: endpoint,
			chatID:   chatID,
			term:     term,
			resultCh: make(chan searchResult, 1),
		}
		w.searchRequests <- req
		res := <-req.resultCh
		if !res.allowed {
			// The daemon caught what searchCaller should have;
			// answer as that layer would rather than serve an empty list.
			http.Error(rw, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		results = res.streamers
	}
	if results == nil {
		results = []string{}
	}
	rw.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(rw).Encode(results)
	if err != nil {
		lerr("cannot write search response, %v", err)
	}
}

func (w *worker) serveEndpoints() {
	go func() {
		err := http.ListenAndServe(w.cfg.ListenAddress, nil)
		checkErr(err)
	}()
}

// webAppAddCommand names an add made from the search web app's submit button.
// It is distinct from /add so the two are not conflated in the log.
const webAppAddCommand = "web_app_add"

// performWebAppAdd carries out an add submitted from the search web app.
// Main goroutine only.
// It vets the chat on the whitelist, as every inbound path does,
// before EnsureUser can materialize a row for it.
// It has no maintenance divert, unlike a Telegram update:
// registerWebApp runs only once the database and caches are ready.
// The event is logged here, not in handleWebAppAdd,
// which runs on an HTTP goroutine where w.db is off limits.
func (w *worker) performWebAppAdd(req webAppAddRequest) {
	if !w.admitChat("web app add", req.chatID) {
		req.admittedCh <- false
		return
	}
	m, _ := w.newReceivedMessage(int(time.Now().Unix()), req.endpoint, req.chatID, "", webAppAddCommand)
	w.logReceived(m)
	// The streamer id is discarded:
	// it is nil for a refusal and for a parked pending subscription alike,
	// so it cannot tell admitted from performed.
	// Either way addStreamer has answered in the chat.
	_ = w.addStreamer(m, req.nickname, false)
	req.admittedCh <- true
}

// submitWebAppAdd hands a request to the main loop and waits for its verdict.
// alive is false once the loop has stopped selecting that arm:
// the send would otherwise park this goroutine for good,
// where the Telegram route gets a 503 from rejectForRedeliveryWhileMigrating.
// Waiting on the verdict needs no such escape:
// admittedCh is buffered, and performWebAppAdd answers on every path.
func (w *worker) submitWebAppAdd(req webAppAddRequest) (admitted, alive bool) {
	select {
	case w.webAppAddRequests <- req:
	case <-w.shutdownCh:
		return false, false
	}
	return <-req.admittedCh, true
}

func (w *worker) handleWebAppAdd(rw http.ResponseWriter, r *http.Request) {
	endpoint := r.URL.Query().Get("endpoint")
	if _, ok := w.cfg.Endpoints[endpoint]; !ok {
		lerr("web app add: bad endpoint %q", endpoint)
		http.Error(rw, "bad endpoint", http.StatusBadRequest)
		return
	}
	// The add is not a read, as the timezone save is not:
	// a shared cache keyed on the URL alone must not answer one chat's with another's.
	if r.Method != http.MethodPost {
		rw.Header().Set("Allow", http.MethodPost)
		http.Error(rw, "use POST", http.StatusMethodNotAllowed)
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	initData := r.Header.Get("X-Init-Data")
	values, ok := w.parseInitData(initData, string(w.cfg.Endpoints[endpoint].BotToken))
	if !ok {
		lerr("web app add: invalid init data for endpoint %s", endpoint)
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
		return
	}
	nickname := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("streamer")))
	if nickname == "" {
		http.Error(rw, "empty streamer", http.StatusBadRequest)
		return
	}
	userID, ok := webAppUserID(values)
	if !ok {
		lerr("web app add: missing user id")
		http.Error(rw, "missing user id", http.StatusBadRequest)
		return
	}
	req := webAppAddRequest{
		endpoint:   endpoint,
		chatID:     userID,
		nickname:   nickname,
		admittedCh: make(chan bool, 1),
	}
	admitted, alive := w.submitWebAppAdd(req)
	if !alive {
		http.Error(rw, "shutting down", http.StatusServiceUnavailable)
		return
	}
	if !admitted {
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}
	rw.WriteHeader(http.StatusOK)
}

// webAppTimezoneCommand names a timezone set from the web app.
// It is distinct from /timezone so the picker and the typed form are not conflated in the log.
const webAppTimezoneCommand = "web_app_timezone"

// performWebAppTimezone stores a zone picked in the web app.
// Main goroutine only, as performWebAppAdd is, and it answers in the chat,
// so the app can close on the save and leave the confirmation behind it.
func (w *worker) performWebAppTimezone(req webAppTimezoneRequest) {
	if !w.admitChat("web app timezone", req.chatID) {
		req.admittedCh <- false
		return
	}
	m, _ := w.newReceivedMessage(int(time.Now().Unix()), req.endpoint, req.chatID, "", webAppTimezoneCommand)
	w.logReceived(m)
	w.db.SetTimezone(m.userID, req.zone)
	w.replyTimezone(m, req.zone, false)
	req.admittedCh <- true
}

// submitWebAppTimezone hands a request to the main loop and waits for its verdict,
// as submitWebAppAdd does, and for the same reasons.
func (w *worker) submitWebAppTimezone(req webAppTimezoneRequest) (admitted, alive bool) {
	select {
	case w.webAppTimezoneRequests <- req:
	case <-w.shutdownCh:
		return false, false
	}
	return <-req.admittedCh, true
}

func (w *worker) handleWebAppTimezone(rw http.ResponseWriter, r *http.Request) {
	endpoint := r.URL.Query().Get("endpoint")
	if _, ok := w.cfg.Endpoints[endpoint]; !ok {
		lerr("web app timezone: bad endpoint %q", endpoint)
		http.Error(rw, "bad endpoint", http.StatusBadRequest)
		return
	}
	// The save is not a read: a shared cache must not answer one chat's with another's.
	if r.Method != http.MethodPost {
		rw.Header().Set("Allow", http.MethodPost)
		http.Error(rw, "use POST", http.StatusMethodNotAllowed)
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	initData := r.Header.Get("X-Init-Data")
	values, ok := w.parseInitData(initData, string(w.cfg.Endpoints[endpoint].BotToken))
	if !ok {
		lerr("web app timezone: invalid init data for endpoint %s", endpoint)
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
		return
	}
	// The page proposes, the bot decides: whatever the browser holds,
	// only a zone this binary can load back may be stored.
	proposed := r.URL.Query().Get("timezone")
	_, zone, ok := w.parseTimezone(proposed)
	if !ok {
		// The page offers what this refuses, so a refusal here is the two sets having parted.
		lerr("web app timezone: refused zone %q for endpoint %s", proposed, endpoint)
		http.Error(rw, "bad timezone", http.StatusBadRequest)
		return
	}
	userID, ok := webAppUserID(values)
	if !ok {
		lerr("web app timezone: missing user id")
		http.Error(rw, "missing user id", http.StatusBadRequest)
		return
	}
	req := webAppTimezoneRequest{
		endpoint:   endpoint,
		chatID:     userID,
		zone:       zone,
		admittedCh: make(chan bool, 1),
	}
	admitted, alive := w.submitWebAppTimezone(req)
	if !alive {
		http.Error(rw, "shutting down", http.StatusServiceUnavailable)
		return
	}
	if !admitted {
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}
	rw.WriteHeader(http.StatusOK)
}

func (w *worker) handleUnmatched(rw http.ResponseWriter, r *http.Request) {
	linf("unhandled request: %s %s", r.Method, r.URL)
	http.NotFound(rw, r)
}

func (w *worker) registerWebApp() {
	http.HandleFunc("/", w.handleUnmatched)
	http.HandleFunc("/apps/add", w.handleWebApp)
	http.HandleFunc("/apps/add/api/search", w.handleSearch)
	http.HandleFunc("/apps/add/api/submit", w.handleWebAppAdd)
	http.HandleFunc("/apps/timezone", w.handleTimezoneApp)
	http.HandleFunc("/apps/timezone/api/submit", w.handleWebAppTimezone)
}

func (w *worker) logConfig() {
	// Two separate log lines on purpose —
	// easier to grep "bot config" or "checker config" out of the journal.
	cfgString, err := json.MarshalIndent(w.cfg, "", "    ")
	checkErr(err)
	linf("bot config: " + string(cfgString))
	checkerCfgString, err := json.MarshalIndent(w.checker.Config(), "", "    ")
	checkErr(err)
	linf("checker config: " + string(checkerCfgString))
	for k, v := range w.trAds {
		linf("ads for %s: %d", k, len(v))
	}
}

// ownerCommands maps each owner command to its handler,
// so a handler cannot exist without its dispatch key.
// The names stay off knownCommands, held so by TestOwnerCommandsStayOffKnownCommands.
var ownerCommands = map[string]func(w *worker, endpoint, arguments string){
	"performance":  (*worker).performanceStat,
	"broadcast":    (*worker).broadcast,
	"direct":       (*worker).direct,
	"blacklist":    (*worker).blacklist,
	"poll":         (*worker).poll,
	"set_max_subs": (*worker).setMaxSubs,
}

// processOwnerMessage handles the owner's own commands.
// Its replies carry the empty command,
// as does the received log for any name off knownCommands.
func (w *worker) processOwnerMessage(endpoint string, command, arguments string) bool {
	handler, ok := ownerCommands[command]
	if !ok {
		return false
	}
	handler(w, endpoint, arguments)
	return true
}

// setMaxSubs raises or lowers a chat's subscription limit.
func (w *worker) setMaxSubs(endpoint string, arguments string) {
	parts := strings.Fields(arguments)
	if len(parts) != 2 {
		w.replyToOwner(endpoint, "expecting two arguments")
		return
	}
	who, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		w.replyToOwner(endpoint, "first argument is invalid")
		return
	}
	maxSubs, err := strconv.Atoi(parts[1])
	if err != nil {
		w.replyToOwner(endpoint, "second argument is invalid")
		return
	}
	if maxSubs < 0 {
		w.replyToOwner(endpoint, "the limit must be non-negative")
		return
	}
	// User, not EnsureUser: a bad owner arg must not materialize a stray row (see direct).
	user, found := w.db.User(who)
	if !found {
		w.replyToOwner(endpoint, "no such user")
		return
	}
	w.db.SetLimit(user.UserID, maxSubs)
	w.replyToOwner(endpoint, "OK")
}

// noinspection SpellCheckingInspection
const letterBytes = "abcdefghijklmnopqrstuvwxyz"

func randString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}

func (w *worker) newRandReferralID() (id string) {
	for {
		id = randString(5)
		if w.db.MustInt("select count(*) from referrals where referral_id = $1", id) == 0 {
			break
		}
	}
	return
}

func (w *worker) getChatMemberCount(endpoint string, chatID int64) *int {
	if !isGroupOrChannel(chatID) { // a private chat has no member count
		return nil
	}
	ctx := context.Background()
	count, err := w.bots[endpoint].GetChatMemberCount(ctx, &bot.GetChatMemberCountParams{ChatID: chatID})
	if err != nil {
		linf("cannot get chat member count, %v", err)
		return nil
	}
	return &count
}

// refreshMemberCount records the chat's member count.
// It skips the network round-trip during shutdown,
// whose drain runs on a fixed budget.
func (w *worker) refreshMemberCount(endpoint string, chatID int64, userID db.UserID) {
	if w.shuttingDown.Load() {
		return
	}
	if memberCount := w.getChatMemberCount(endpoint, chatID); memberCount != nil {
		w.db.UpdateMemberCount(userID, *memberCount)
	}
}

func (w *worker) refer(followerUserID db.UserID, referrer string, now int, followerCreated bool) (applied appliedKind) {
	referrerUserID := w.db.UserForReferralID(referrer)
	if referrerUserID == nil {
		return invalidReferral
	}
	if !followerCreated {
		return followerExists
	}
	w.db.SetLimit(followerUserID, w.cfg.MaxSubs+w.cfg.FollowerBonus)
	w.db.AddReferrerBonus(*referrerUserID, w.cfg.ReferralBonus)
	w.db.IncrementReferredUsers(*referrerUserID)
	w.db.AddReferralEvent(now, referrerUserID, followerUserID, nil)
	return referralApplied
}

func (w *worker) referralData(endpoint string, userID db.UserID) tplData {
	referralID := w.db.ReferralID(userID)
	if referralID == nil {
		temp := w.newRandReferralID()
		referralID = &temp
		w.db.AddReferral(userID, *referralID)
	}
	referralLink := fmt.Sprintf("https://t.me/%s?start=%s", w.botNames[endpoint], *referralID)
	subscriptionsNumber := w.db.SubscribedOrPendingCount(endpoint, userID)
	user := w.mustUserByID(userID)
	return tplData{
		"link":                referralLink,
		"referral_bonus":      w.cfg.ReferralBonus,
		"follower_bonus":      w.cfg.FollowerBonus,
		"subscriptions_used":  subscriptionsNumber,
		"total_subscriptions": user.MaxSubs,
		"buy_subs_enabled":    w.cfg.BuySubsEnabled(),
	}
}

func (w *worker) showReferral(m receivedMessage) {
	w.replyTr(m, db.PriorityHigh, false,
		w.tr[m.endpoint].ReferralLink, w.referralData(m.endpoint, m.userID))
}

func (w *worker) start(m receivedMessage, referrer string, created bool) {
	nickname := ""
	switch {
	case strings.HasPrefix(referrer, modelPayloadPrefix):
		nickname = referrer[len(modelPayloadPrefix):]
		nickname = w.checker.NicknamePreprocessing(nickname)
		referrer = ""
	case referrer != "":
		referralID := w.db.ReferralID(m.userID)
		if referralID != nil && *referralID == referrer {
			w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].OwnReferralLinkHit, nil)
			return
		}
	}
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].Start, tplData{
		"website_link": w.siteHomeLink(),
	})
	m = m.next()
	if referrer != "" && !isGroupOrChannel(m.chatID) {
		applied := w.refer(m.userID, referrer, m.timestamp, created)
		switch applied {
		case referralApplied:
			w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].ReferralApplied, nil)
		case invalidReferral:
			w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].InvalidReferralLink, nil)
		case followerExists:
			w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].FollowerExists, nil)
		}
	}
	if nickname != "" {
		if streamerID := w.addStreamer(m, nickname, true); streamerID != nil {
			w.db.AddReferralEvent(m.timestamp, nil, m.userID, streamerID)
		}
	}
}

func (w *worker) help(m receivedMessage) {
	w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].Help, tplData{
		"website_link": w.siteHomeLink(),
	})
}

// commandSpec declares what a command needs.
// Membership means the switch dispatches it and its use is logged,
// so a case the table omits answers "unknown command".
type commandSpec struct {
	// groupAdminOnly commands read or change the chat's own setup, which in a group is everyone's.
	groupAdminOnly bool

	// memberSubscriptions commands lose the admin gate where the chat has turned the setting on.
	memberSubscriptions bool
}

var knownCommands = map[string]commandSpec{
	"ad":                            {},
	"add":                           {groupAdminOnly: true, memberSubscriptions: true},
	"affiliate":                     {}, // admin-gated in commandGate while enabled
	"buy_subs":                      {},
	"disable_images":                {groupAdminOnly: true},
	"disable_member_subscriptions":  {groupAdminOnly: true},
	"disable_offline_notifications": {groupAdminOnly: true},
	"disable_silent_messages":       {groupAdminOnly: true},
	"disable_subject":               {groupAdminOnly: true},
	"enable_images":                 {groupAdminOnly: true},
	"enable_member_subscriptions":   {groupAdminOnly: true},
	"enable_offline_notifications":  {groupAdminOnly: true},
	"enable_silent_messages":        {groupAdminOnly: true},
	"enable_subject":                {groupAdminOnly: true},
	"faq":                           {},
	"feedback":                      {},
	"help":                          {},
	"list":                          {},
	"online":                        {},
	"pics":                          {},
	"referral":                      {},
	"remove":                        {groupAdminOnly: true, memberSubscriptions: true},
	"remove_all":                    {groupAdminOnly: true},
	"reset_affiliate":               {}, // admin-gated in commandGate while enabled
	"reset_timezone":                {groupAdminOnly: true},
	"settings":                      {groupAdminOnly: true},
	"social":                        {},
	"start":                         {}, // the m- deep-link form is gated in commandGate
	"stop":                          {groupAdminOnly: true},
	"sure_remove_all":               {groupAdminOnly: true},
	"timezone":                      {groupAdminOnly: true},
	"version":                       {},
	"want_more":                     {},
	"week":                          {},
}

// affiliateCommandAllowed answers the caller when the command does not apply,
// and reports whether to go on.
// A disabled feature stays hidden; a private chat gets told where it works.
func (w *worker) affiliateCommandAllowed(m receivedMessage) bool {
	switch {
	case !w.customAffiliateLinkEnabled():
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].UnknownCommand, nil)
	case !isGroupOrChannel(m.chatID):
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AffiliateGroupsOnly, nil)
	default:
		return true
	}
	return false
}

// commandGate resolves what a command asks of the chat it came from.
// A command the switch refuses is left ungated, so it answers the same to everyone.
func (w *worker) commandGate(command, arguments string) commandSpec {
	spec := knownCommands[command]
	switch command {
	case "start":
		// The deep link adds a subscription, so it is gated as /add is,
		// and a payload naming no model is left ungated, since start answers it with nothing.
		name, ok := strings.CutPrefix(arguments, modelPayloadPrefix)
		if !ok || w.checker.NicknamePreprocessing(name) == "" {
			return commandSpec{}
		}
		return knownCommands["add"]
	case "affiliate", "reset_affiliate":
		spec.groupAdminOnly = w.customAffiliateLinkEnabled()
	}
	return spec
}

// groupAdminOnly reports whether a command needs group-admin rights in the chat it came from.
// The chat's setup is read, so the caller establishes the chat is a group or a channel first.
func (w *worker) groupAdminOnly(m receivedMessage, command, arguments string) bool {
	spec := w.commandGate(command, arguments)
	if !spec.groupAdminOnly {
		return false
	}
	// Only an admin can turn the setting on, so opening a command to members is an admin's choice.
	return !spec.memberSubscriptions || !w.mustUserByID(m.userID).MemberSubscriptions
}

// processIncomingCommand dispatches on command, which the caller lowercases.
// Every reply is logged against m.command, which the caller has already gated:
// a name the received log skipped must not surface here either.
func (w *worker) processIncomingCommand(
	m receivedMessage,
	snd sender,
	command, arguments string,
	created bool,
) {
	linf("command received: chat = %d, command = %s, arguments = %s", m.chatID, command, arguments)
	w.db.ResetBlock(m.endpoint, m.userID)

	if m.chatID == w.cfg.OwnerID {
		if w.processOwnerMessage(m.endpoint, command, arguments) {
			return
		}
	}

	unknown := func() {
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].UnknownCommand, nil)
	}

	// The unknown-command answer for anything off the table;
	// the switch's default answers only table-switch drift.
	if _, known := knownCommands[command]; !known {
		unknown()
		return
	}

	if isGroupOrChannel(m.chatID) && w.groupAdminOnly(m, command, arguments) &&
		!w.senderIsGroupAdmin(m.endpoint, m.chatID, snd) {
		// A refusal the setting could have prevented names it, the refusal itself proving it off.
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].AdminsOnly, tplData{
			"member_subscriptions": w.commandGate(command, arguments).memberSubscriptions,
		})
		return
	}

	switch command {
	case "add":
		arguments = strings.ReplaceAll(arguments, "—", "--")
		_ = w.addStreamer(m, arguments, false)
	case "remove":
		arguments = strings.ReplaceAll(arguments, "—", "--")
		w.removeStreamer(m, arguments)
	case "list":
		w.listStreamers(m)
	case "pics", "online":
		w.listOnlineStreamers(m)
	case "start":
		w.start(m, arguments, created)
	case "help":
		w.help(m)
	case "ad":
		w.ad(db.PriorityHigh, m.endpoint, m.userID, m.command)
	case "faq":
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].FAQ, tplData{
			"max_subs":         w.cfg.MaxSubs,
			"buy_subs_enabled": w.cfg.BuySubsEnabled(),
		})
	case "feedback":
		w.feedback(m, arguments)
	case "social":
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].Social, nil)
	case "version":
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].Version,
			tplData{"version": cmdlib.Version})
	case "remove_all", "stop":
		w.replyTr(m, db.PriorityHigh, false, w.tr[m.endpoint].RemoveAll, nil)
	case "sure_remove_all":
		w.sureRemoveAll(m)
	case "want_more":
		w.wantMore(m)
	case "buy_subs":
		if !w.cfg.BuySubsEnabled() {
			unknown()
			return
		}
		w.buySubs(m)
	case "settings":
		w.settings(m)
	case "enable_images":
		w.enableImages(m, true)
	case "disable_images":
		w.enableImages(m, false)
	case "enable_offline_notifications":
		w.enableOfflineNotifications(m, true)
	case "disable_offline_notifications":
		w.enableOfflineNotifications(m, false)
	case "enable_subject":
		w.enableSubject(m, true)
	case "disable_subject":
		w.enableSubject(m, false)
	case "enable_silent_messages":
		w.enableSilentMessages(m, true)
	case "disable_silent_messages":
		w.enableSilentMessages(m, false)
	case "enable_member_subscriptions":
		w.enableMemberSubscriptions(m, true)
	case "disable_member_subscriptions":
		w.enableMemberSubscriptions(m, false)
	case "referral":
		w.showReferral(m)
	case "week":
		if !w.cfg.EnableWeek {
			unknown()
			return
		}
		w.showWeek(m, arguments)
	case "timezone":
		w.setTimezone(m, arguments)
	case "reset_timezone":
		w.resetTimezone(m)
	case "affiliate":
		if !w.affiliateCommandAllowed(m) {
			return
		}
		w.setAffiliate(m, arguments)
	case "reset_affiliate":
		if !w.affiliateCommandAllowed(m) {
			return
		}
		w.resetAffiliate(m)
	default:
		// Unreachable while every knownCommands key has a case; table-switch drift lands here.
		lerr("known command missing a handler: command = %s", command)
		unknown()
	}
}

func (w *worker) periodic() {
	w.pushOnlineRequest()
}

func (w *worker) pushOnlineRequest() {
	caps := w.checker.Capabilities()
	var request cmdlib.StatusRequest
	if caps.UsesFixedListOnline() {
		request = &cmdlib.FixedListOnlineRequest{
			ResultsCh: w.checkerResults,
			Streamers: w.db.SubscribedStreamers(),
		}
	} else {
		var poll []string
		if caps.SupportsQueryStatus {
			poll = w.db.StreamersToPoll()
		}
		request = &cmdlib.OnlineListRequest{
			ResultsCh: w.checkerResults,
			Poll:      poll,
		}
	}
	if err := w.checker.PushStatusRequest(request); err != nil {
		lerr("%v", err)
	}
}

func (w *worker) pushExistenceRequest(streamers map[string]bool) error {
	caps := w.checker.Capabilities()
	switch {
	case caps.SupportsQueryFixedListStatuses:
		err := w.checker.PushStatusRequest(&cmdlib.FixedListStatusRequest{
			ResultsCh: w.existenceListResults,
			Streamers: streamers,
		})
		if err != nil {
			lerr("%v", err)
		}
		return err
	case caps.SupportsQueryStatus:
		for name := range streamers {
			err := w.checker.PushStatusRequest(&cmdlib.SingleStatusRequest{
				ResultsCh: w.existenceListResults,
				Streamer:  name,
			})
			if err != nil {
				lerr("%v", err)
				return err
			}
		}
		return nil
	}
	lerr("pushExistenceRequest called with no status capability")
	return nil
}

type processingResult struct {
	unconfirmedChangesCount int
	unconfirmedOfflineCount int
	unconfirmedOnlineCount  int
	confirmedChangesCount   int
	notifications           []db.Notification
	elapsed                 time.Duration
	upsertTimings           db.UpsertUnconfirmedTimings
	confirmChangesMs        int
	storeNotificationsMs    int
}

func (w *worker) handleCheckerResults(result cmdlib.CheckerResults, now int) processingResult {
	start := time.Now()
	if result.Failed() {
		return processingResult{}
	}

	var updates []db.StatusChange

	switch r := result.(type) {
	case *cmdlib.OnlineListResults:
		if len(r.PollErrors) > 0 {
			w.db.IncrementPollErrors(r.PollErrors)
		}
		// Went offline: was online but not in result
		for nickname := range w.unconfirmedOnlineStreamers {
			if _, inResult := r.Streamers[nickname]; !inResult {
				updates = append(updates, db.StatusChange{
					Nickname: nickname,
					Status:   cmdlib.StatusOffline,
				})
			}
		}
		// Went online: in result but wasn't online
		for nickname := range r.Streamers {
			if _, wasOnline := w.unconfirmedOnlineStreamers[nickname]; !wasOnline {
				updates = append(updates, db.StatusChange{
					Nickname: nickname,
					Status:   cmdlib.StatusOnline,
				})
			}
		}
		w.unconfirmedOnlineStreamers = r.Streamers

	case *cmdlib.FixedListOnlineResults:
		// Went offline: was online but not in result (and was requested)
		var maybeFirstOffline []string
		for nickname := range w.unconfirmedOnlineStreamers {
			if _, inResult := r.Streamers[nickname]; !inResult && r.RequestedStreamers[nickname] {
				updates = append(updates, db.StatusChange{
					Nickname: nickname,
					Status:   cmdlib.StatusOffline,
				})
			}
		}

		// Collect streamers that might need first offline status:
		// requested, not in result, and never seen online
		for nickname := range r.RequestedStreamers {
			_, inResult := r.Streamers[nickname]
			_, inCache := w.unconfirmedOnlineStreamers[nickname]
			if !inResult && !inCache {
				maybeFirstOffline = append(maybeFirstOffline, nickname)
			}
		}

		// First offline: exists in DB but never online, set offline if not already
		if len(maybeFirstOffline) > 0 {
			dbStatuses := w.db.UnconfirmedStatusesForStreamers(maybeFirstOffline)
			for _, nickname := range maybeFirstOffline {
				if status, exists := dbStatuses[nickname]; exists && status.Status != cmdlib.StatusOffline {
					updates = append(updates, db.StatusChange{
						Nickname: nickname,
						Status:   cmdlib.StatusOffline,
					})
				}
			}
		}

		// Went online: in result but wasn't online
		for nickname := range r.Streamers {
			if _, inCache := w.unconfirmedOnlineStreamers[nickname]; !inCache {
				updates = append(updates, db.StatusChange{
					Nickname: nickname,
					Status:   cmdlib.StatusOnline,
				})
			}
		}

		w.unconfirmedOnlineStreamers = r.Streamers

		// Set known streamers not in request to unknown
		for nickname := range w.db.KnownStreamers() {
			if !r.RequestedStreamers[nickname] {
				delete(w.unconfirmedOnlineStreamers, nickname)
				updates = append(updates, db.StatusChange{
					Nickname: nickname,
					Status:   cmdlib.StatusUnknown,
				})
			}
		}
	}

	upsertTimings := w.db.UpsertUnconfirmedStatusChanges(updates, now)

	confirmChangesStart := time.Now()
	confirmedStatusChanges := w.db.ConfirmStatusChanges(
		now,
		w.cfg.StatusConfirmationSeconds.Online,
		w.cfg.StatusConfirmationSeconds.Offline,
	)
	confirmChangesMs := int(time.Since(confirmChangesStart).Milliseconds())

	storeNotificationsStart := time.Now()
	notifications := w.buildNotifications(confirmedStatusChanges)
	w.db.StoreNotifications(notifications)
	storeNotificationsMs := int(time.Since(storeNotificationsStart).Milliseconds())

	var offlineCount, onlineCount int
	for _, u := range updates {
		switch u.Status {
		case cmdlib.StatusOffline:
			offlineCount++
		case cmdlib.StatusOnline:
			onlineCount++
		}
	}

	return processingResult{
		unconfirmedChangesCount: len(updates),
		unconfirmedOfflineCount: offlineCount,
		unconfirmedOnlineCount:  onlineCount,
		confirmedChangesCount:   len(confirmedStatusChanges),
		notifications:           notifications,
		elapsed:                 time.Since(start),
		storeNotificationsMs:    storeNotificationsMs,
		upsertTimings:           upsertTimings,
		confirmChangesMs:        confirmChangesMs,
	}
}

func (w *worker) buildNotifications(
	confirmedStatusChanges []db.ConfirmedStatusChange,
) []db.Notification {
	streamerIDs := make([]int, len(confirmedStatusChanges))
	for i, c := range confirmedStatusChanges {
		streamerIDs[i] = c.StreamerID
	}

	var notifications []db.Notification
	usersForStreamers, endpointsForStreamers := w.db.UsersForStreamers(streamerIDs)
	for _, c := range confirmedStatusChanges {
		// Skip unknown -> offline transitions
		// They don't represent meaningful events to users
		if c.PrevStatus == cmdlib.StatusUnknown && c.Status == cmdlib.StatusOffline {
			continue
		}
		users := usersForStreamers[c.StreamerID]
		endpoints := endpointsForStreamers[c.StreamerID]
		info := w.unconfirmedOnlineStreamers[c.Nickname]
		for i, user := range users {
			if (w.cfg.OfflineNotifications && user.OfflineNotifications) || c.Status != cmdlib.StatusOffline {
				n := db.Notification{
					Endpoint:   endpoints[i],
					UserID:     user.UserID,
					StreamerID: &c.StreamerID,
					Nickname:   c.Nickname,
					Status:     c.Status,
					Social:     !isGroupOrChannel(user.ChatID),
					Sound:      c.Status == cmdlib.StatusOnline,
					Priority:   db.PriorityLow,
					Kind:       db.NotificationPacket}
				if user.ShowImages {
					n.ImageURL = info.ImageURL
				}
				if user.ShowSubject {
					n.Subject = info.Subject
				}
				notifications = append(notifications, n)
			}
		}
	}

	return notifications
}

// admitChat is the whitelist gate for every inbound path that can answer with a bool.
// Four sites gate on ChatWhitelisted directly: the pre-checkout answer folds it into
// its allowed conjunction, searchCaller speaks HTTP statuses,
// a chat migration admits either end of its pair,
// and sendMessageInternal gates outbound, skipping a send to a de-whitelisted chat.
// path names the rejecting caller, since several share this log line.
func (w *worker) admitChat(path string, chatID int64) bool {
	if !w.cfg.ChatWhitelisted(chatID) {
		linf("chat not in whitelist: path = %s, chat = %d", path, chatID)
		return false
	}
	return true
}

func getCommandAndArgs(update *models.Update, mention string, ourIDs []int64) (int64, string, string) {
	var text string
	var chatID int64
	var forceMention bool
	if update.Message != nil {
		if update.Message.IsAutomaticForward {
			// The linked channel's relay: with the bot in the channel,
			// the post was already handled there, and running the copy would execute one command twice.
			// A bot only in the group drops channel commands by this same test,
			// so to command the bot from a channel, add the bot to the channel.
			// A command someone meant is worth a visible line; mirrored chatter is not.
			if strings.HasPrefix(strings.TrimSpace(update.Message.Text), "/") {
				linf("ignoring a command in an auto-forwarded channel post: chat = %d", update.Message.Chat.ID)
			} else {
				ldbg("ignoring an auto-forwarded channel post: chat = %d", update.Message.Chat.ID)
			}
			return update.Message.Chat.ID, "", ""
		}
		text = update.Message.Text
		chatID = update.Message.Chat.ID
		for _, m := range update.Message.NewChatMembers {
			for _, ourID := range ourIDs {
				if m.ID == ourID {
					return chatID, "start", ""
				}
			}
		}
		// Chatter is not for the bot; in a group it answers commands only.
		if isGroupOrChannel(chatID) && !strings.HasPrefix(text, "/") {
			return chatID, "", ""
		}
	} else if update.ChannelPost != nil {
		text = update.ChannelPost.Text
		chatID = update.ChannelPost.Chat.ID
		forceMention = true
	}
	text = strings.TrimLeft(text, " /")
	if text == "" {
		return chatID, "", ""
	}
	parts := strings.SplitN(text, " ", 2)
	// Telegram matches a username case-insensitively but relays the text as typed,
	// so /settings@sirenbot must reach a bot registered as SirenBot.
	name, addressee, addressed := strings.Cut(parts[0], "@")
	switch {
	case addressed && strings.EqualFold("@"+addressee, mention):
		parts[0] = name
	case forceMention || (addressed && isGroupOrChannel(chatID)):
		// A command naming another bot is that bot's, and a group delivers it to every bot.
		return chatID, "", ""
	}
	if len(parts) < 2 {
		return chatID, parts[0], ""
	}
	return chatID, parts[0], strings.TrimSpace(parts[1])
}

// migrateCommand names a completed group-to-supergroup migration.
const migrateCommand = "migrate"

// migrateChatAndLog migrates the chat and, only when it actually migrated,
// logs one migrate event attributed to the destination user,
// so a redelivery or the pair's second message does not double-count.
func (w *worker) migrateChatAndLog(timestamp int, endpoint string, fromID, toID int64) {
	if w.migrateChat(fromID, toID) {
		w.db.LogReceivedMessage(timestamp, endpoint, w.db.EnsureUser(toID), migrateCommand)
	}
}

// migrationPair decodes a migration message's wire format, the one place that does:
// MigrateToChatID and MigrateFromChatID are 0 where Telegram omitted the field.
// ok reports a migration; both directions carry the same pair.
// The API reference promises no exclusivity between the two fields;
// its maintainer states a message cannot belong to both chats
// (tdlib/telegram-bot-api#170), and the first case trusts the fields alone regardless.
func migrationPair(m *models.Message) (fromID, toID int64, ok bool) {
	switch {
	// Both fields name their ends absolutely, so together they need no help from the chat,
	// which names an end only where one field arrives alone.
	case m.MigrateFromChatID != 0 && m.MigrateToChatID != 0:
		return m.MigrateFromChatID, m.MigrateToChatID, true
	case m.MigrateToChatID != 0:
		return m.Chat.ID, m.MigrateToChatID, true
	case m.MigrateFromChatID != 0:
		return m.MigrateFromChatID, m.Chat.ID, true
	}
	return 0, 0, false
}

// handleChatMigration migrates a chat after a group-to-supergroup upgrade.
// Both the migrate_to and migrate_from messages carry the same ID pair.
func (w *worker) handleChatMigration(endpoint string, now int, fromID, toID int64) {
	if !w.cfg.ChatWhitelisted(fromID) && !w.cfg.ChatWhitelisted(toID) {
		linf("chat migration not in whitelist: from = %d, to = %d", fromID, toID)
		return
	}
	w.migrateChatAndLog(now, endpoint, fromID, toID)
}

// migrateChat moves a chat's data, logs the outcome,
// and reports whether it migrated,
// so the caller writes one received_message_log event per migration.
// Only the main goroutine may touch the database, so it must run there.
func (w *worker) migrateChat(fromID, toID int64) bool {
	m := w.db.MigrateChat(fromID, toID)
	if m == nil {
		// Already applied (the pair's second message, or a redelivery): a no-op.
		ldbg("migrate chat %d -> %d already applied, skipping", fromID, toID)
		return false
	}
	if m.Renamed {
		linf("migrated chat %d -> %d: renamed", fromID, toID)
	} else {
		linf(
			"migrated chat %d -> %d: destination exists, moved feedback = %d, payments = %d, referrals = %d, tombstoned source",
			fromID, toID, m.Feedback, m.Payments, m.Referrals)
	}
	return true
}

func (w *worker) processTGUpdate(p incomingPacket) {
	now := int(time.Now().Unix())
	u := p.message
	if u.PreCheckoutQuery != nil {
		w.handlePreCheckoutQuery(p.endpoint, u.PreCheckoutQuery)
		return
	}
	if u.CallbackQuery != nil {
		q := u.CallbackQuery
		var chatID int64
		switch {
		case q.Message.Type == models.MaybeInaccessibleMessageTypeMessage && q.Message.Message != nil:
			chatID = q.Message.Message.Chat.ID
		case q.Message.Type == models.MaybeInaccessibleMessageTypeInaccessibleMessage && q.Message.InaccessibleMessage != nil:
			chatID = q.Message.InaccessibleMessage.Chat.ID
		default:
			return
		}
		if !w.admitChat("callback_query", chatID) {
			return
		}
		// Always answer to clear the client's loading spinner,
		// even for data we do not handle (e.g. a stale keyboard from an older build).
		ctx := context.Background()
		if _, err := w.bots[p.endpoint].AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID}); err != nil {
			lerr("cannot answer callback query %s: %v", q.ID, err)
		}
		if strings.HasPrefix(q.Data, "buy:") {
			w.handleBuyCallback(p.endpoint, chatID, q.Data)
		}
		return
	}
	if u.Message != nil && u.Message.SuccessfulPayment != nil {
		chatID := u.Message.Chat.ID
		if !w.admitChat("successful_payment", chatID) {
			return
		}
		w.handleSuccessfulPayment(p.endpoint, chatID, u.Message.SuccessfulPayment, now)
		return
	}
	if m := u.Message; m != nil {
		if fromID, toID, ok := migrationPair(m); ok {
			w.handleChatMigration(p.endpoint, now, fromID, toID)
			return
		}
	}
	mention := w.botMention(p.endpoint)
	chatID, command, args := getCommandAndArgs(u, mention, w.ourIDs)
	if command == "" {
		return
	}
	if !w.admitChat("message", chatID) {
		return
	}
	// Lowercase before the gate: knownCommands holds lowercase names,
	// so a capitalized command must not read as untracked.
	command = strings.ToLower(command)
	var loggedCommand string
	if _, ok := knownCommands[command]; ok {
		loggedCommand = command
	}
	var chatType string
	if u.Message != nil {
		chatType = string(u.Message.Chat.Type)
	} else if u.ChannelPost != nil {
		chatType = string(u.ChannelPost.Chat.Type)
	}
	m, created := w.newReceivedMessage(now, p.endpoint, chatID, chatType, loggedCommand)
	w.logReceived(m)
	w.processIncomingCommand(m, senderOf(u), command, args, created)
	w.refreshMemberCount(p.endpoint, chatID, m.userID)
}

func (w *worker) incoming() chan incomingPacket {
	// Background, not a cancellable context. The bots stop on Shutdown
	// (called by shutdownBots), draining buffered updates into incomingPackets.
	ctx := context.Background()
	for n, p := range w.cfg.Endpoints {
		linf("listening for a webhook for endpoint %s", n)
		http.Handle(string(p.ListenPath), w.rejectForRedeliveryWhileMigrating(w.bots[n].WebhookHandler()))
		go w.bots[n].StartWebhook(ctx)
	}
	return w.incomingPackets
}

// maxWebhookBody caps the body buffered in the redelivery gate.
// Updates are far smaller, so this only bounds an oversized public POST.
const maxWebhookBody = 1 << 20

// incomingBufferSize is incomingPackets' per-bot capacity,
// and the cap of one bot's Updates channel, pinned at bot.New.
// The channel holds it times the bot count,
// so a shutdown flush of every bot's buffer usually fits outright;
// drainIncoming consumes concurrently to cover a pre-existing backlog.
const incomingBufferSize = 1024

// rejectForRedeliveryWhileMigrating 503s updates that must not be dropped,
// so Telegram redelivers them: during the startup schema migration, and
// (via shuttingDown) during shutdown, when buffered updates would be lost.
// These are successful_payment and group-to-supergroup migration messages.
// The library 200s on enqueue, so a 503 is the only way to force redelivery.
// pre_checkout passes through (10s deadline).
func (w *worker) rejectForRedeliveryWhileMigrating(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if w.maintenance.Load() || w.shuttingDown.Load() {
			body, err := io.ReadAll(http.MaxBytesReader(rw, req.Body, maxWebhookBody))
			if err != nil {
				http.Error(rw, "cannot read body", http.StatusServiceUnavailable)
				return
			}
			req.Body = io.NopCloser(bytes.NewReader(body))
			update := &models.Update{}
			// Decode with the library's own type so classification matches it.
			// Fail closed: an unparsable update must not be 200'd and dropped.
			if err := json.Unmarshal(body, update); err != nil {
				http.Error(rw, "unparsable update", http.StatusServiceUnavailable)
				return
			}
			if m := update.Message; m != nil {
				if m.SuccessfulPayment != nil {
					linf(
						"rejecting successful_payment during migration for redelivery: chat = %d, charge = %s",
						m.Chat.ID, m.SuccessfulPayment.TelegramPaymentChargeID)
					http.Error(rw, "migrating", http.StatusServiceUnavailable)
					return
				}
				if _, _, ok := migrationPair(m); ok {
					linf("rejecting chat migration during startup for redelivery: chat = %d", m.Chat.ID)
					http.Error(rw, "migrating", http.StatusServiceUnavailable)
					return
				}
			}
		}
		inner.ServeHTTP(rw, req)
	})
}

func getOurIDs(c *botconfig.Config) []int64 {
	var ids []int64
	for _, e := range c.Endpoints {
		if idx := strings.Index(string(e.BotToken), ":"); idx != -1 {
			id, err := strconv.ParseInt(string(e.BotToken)[:idx], 10, 64)
			checkErr(err)
			ids = append(ids, id)
		} else {
			checkErr(errors.New("cannot get our ID"))
		}
	}
	return ids
}

func (w *worker) maintainDB() {
	w.db.MaintainBrinIndexes()
}

// maintenanceReply handles an update that arrives while migrations run:
// it rejects pre-checkouts, answers callbacks,
// and replies to commands with the maintenance notice,
// remembering who to notify once the bot is up.
// Replies go through the regular scheduler as MaintenancePacket,
// so their send results touch no database.
func (w *worker) maintenanceReply(u incomingPacket, waitingUsers map[waitingUser]bool) {
	// Reject pre-checkout during startup, the payment won't complete.
	if u.message.PreCheckoutQuery != nil {
		ctx := context.Background()
		_, err := w.bots[u.endpoint].AnswerPreCheckoutQuery(ctx, &bot.AnswerPreCheckoutQueryParams{
			PreCheckoutQueryID: u.message.PreCheckoutQuery.ID,
			OK:                 false,
			ErrorMessage:       w.cfg.Endpoints[u.endpoint].MaintenanceResponse,
		})
		if err != nil {
			lerr("cannot answer pre-checkout query during maintenance: %v", err)
		}
		return
	}
	// Answer inline-button taps with the maintenance notice,
	// otherwise the client spinner hangs until Telegram expires the callback.
	if u.message.CallbackQuery != nil {
		ctx := context.Background()
		_, err := w.bots[u.endpoint].AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: u.message.CallbackQuery.ID,
			Text:            w.cfg.Endpoints[u.endpoint].MaintenanceResponse,
		})
		if err != nil {
			lerr("cannot answer callback query during maintenance: %v", err)
		}
		return
	}
	// successful_payment is rejected at the webhook layer during maintenance,
	// so it never reaches here.
	mention := w.botMention(u.endpoint)
	chatID, command, args := getCommandAndArgs(u.message, mention, w.ourIDs)
	if command != "" {
		// Gate like processTGUpdate: otherwise finishStartup's EnsureUser
		// materializes a row for a non-whitelisted chat.
		if !w.admitChat("maintenance message", chatID) {
			return
		}
		waitingUsers[waitingUser{chatID: chatID, endpoint: u.endpoint}] = true
		w.sendMaintenance(u.endpoint, chatID, false, w.cfg.Endpoints[u.endpoint].MaintenanceResponse)
		linf("ignoring command %s %s", command, args)
	}
}

// shutdownWaitTimeout caps the whole stop sequence:
// the webhook removal, the library flush and drain,
// and the wait for the in-flight send.
const shutdownWaitTimeout = 10 * time.Second

// webhookRemovalTimeout caps the webhook removal inside shutdown,
// so a hung Telegram call cannot eat the drain's share of the budget.
const webhookRemovalTimeout = 2 * time.Second

// shutdown runs the whole stop sequence under one shutdownWaitTimeout budget,
// shared by every phase, so it fits the orchestrator's grace
// rather than each phase getting a fresh timeout.
// It rejects new work, flushes and drains the library buffers,
// and waits for the in-flight send.
func (w *worker) shutdown(incoming chan incomingPacket) {
	w.shuttingDown.Store(true)
	// Wake both watchers of shutdownCh: the in-flight global pace inside deliver,
	// so the drain never sleeps out its 1s,
	// and any cooldown-release callback parked on the cooledUsers send
	// once the drain stops reading it.
	// The long postpone waits sit in detached timers that shutdown never fires.
	close(w.shutdownCh)
	ctx, cancel := context.WithTimeout(context.Background(), shutdownWaitTimeout)
	defer cancel()
	webhookCtx, webhookCancel := context.WithTimeout(ctx, webhookRemovalTimeout)
	w.removeWebhook(webhookCtx)
	webhookCancel()
	// Flush and drain concurrently:
	// a full incoming channel would otherwise park the flush until the deadline,
	// with the drain only starting after.
	botsDone := make(chan struct{})
	go func() {
		w.shutdownBots(ctx)
		close(botsDone)
	}()
	w.drainIncoming(ctx, incoming, botsDone)
	w.waitForInflightSends(ctx)
	w.drainSendResults()
	w.logShutdownLoss(len(incoming))
}

// waitForInflightSends lets the single in-flight delivery finish its POST
// before exit, bounded by ctx (the shared shutdown deadline).
// Anything still queued is dropped;
// notifications re-send next start, command replies are lost.
func (w *worker) waitForInflightSends(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		w.deliverWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		linf("shutdown: in-flight send did not finish before the deadline")
		// A POST succeeding right at the deadline
		// still sleeps commonCooldown before its result lands; grant it that,
		// so drainSendResults finalizes a delivered notification
		// instead of re-arming it into a duplicate.
		time.Sleep(2 * commonCooldown)
	}
}

// handleSendResult runs the database bookkeeping for one send result:
// block counters, chat-data migration, and the send log.
// A maintenance send skips it all: it may run before the database exists.
// The member-count lookup is a network call and deliberately not here —
// completeSendResult runs it only after freeing the send slot.
func (w *worker) handleSendResult(r msgSendResult) {
	if r.tag.kind == db.MaintenancePacket {
		return
	}
	switch r.result {
	case messageBlocked:
		w.db.IncrementBlock(r.endpoint, r.userID)
	case messageSent:
		w.db.ResetBlock(r.endpoint, r.userID)
	case messageMigrate:
		// Move the chat's data so later sends go to the new id, not the old.
		// A same-id migrate degenerates inside MigrateChat; nothing to guard here.
		w.migrateChatAndLog(r.timestamp, r.endpoint, r.chatID, r.migrateToChatID)
	}
	w.db.LogSentMessage(
		r.timestamp, r.userID, r.result, r.endpoint, r.priority, r.latency, r.tag.kind, r.tag.command,
		r.tag.replySeq)
}

// resolveResultUser resolves a non-maintenance result's user to the live one,
// so bookkeeping after a mid-flight migrate lands on the live row,
// not a tombstone. A maintenance send carries no user.
func (w *worker) resolveResultUser(r *msgSendResult) {
	if r.tag.kind != db.MaintenancePacket {
		r.userID = w.db.LiveUserID(r.userID)
	}
}

// completeSendResult applies the result's database bookkeeping,
// finalizes the notification, frees the send slot,
// and only then refreshes the member count — in that order.
// The whole bookkeeping (handleSendResult) runs before the slot frees.
// Migrate requires it: a stalled main loop can let the user's cooling release
// overtake this result and dispatch the resend the moment onSendDone frees
// the slot, so the chat row must already point at the migrated chat's new id.
// The log and block-counter writes do not need that ordering,
// but stay here deliberately rather than split off to overlap the next POST:
// they are fast local writes that single-flight pacing (commonCooldown)
// already dwarfs, so recording the whole result before the next send
// beats a sub-millisecond overlap and keeps the ordering trivial.
// Only the member-count lookup, a network round-trip, is deferred past
// onSendDone so the next POST overlaps and hides it.
// Main goroutine only.
func (w *worker) completeSendResult(r msgSendResult) {
	w.resolveResultUser(&r)
	w.handleSendResult(r)
	switch r.disposition() {
	case dispFinalize:
		w.finalizeNotification(r.notificationID, r.userID, true)
	case dispResend:
		// Re-queue the original queued message:
		// its seq keeps the queue position against later same-user messages,
		// its requestedAt keeps the latency baseline,
		// and its userID keeps it parked behind the cooling user,
		// even when a merge migration has re-keyed the live user meanwhile
		// (dispatch resolves the chat id through the tombstone chain).
		w.enqueue(r.resend)
	case dispRearm:
		// A same-id migrate delivered nothing.
		// Re-arm the notification rather than finalizing it as sent;
		// the next fetch retries it fresh.
		w.db.RequeueNotification(r.notificationID)
	case dispLeave:
		// A same-id reply migrate or any maintenance migrate (notificationID 0):
		// no row to re-arm.
	}
	w.onSendDone()
	if r.result == messageSent && r.tag.kind != db.MaintenancePacket {
		w.refreshMemberCount(r.endpoint, r.chatID, r.userID)
	}
}

// drainSendResults handles sends that completed after the drain stopped
// pumping, so their results were never read;
// run their bookkeeping and finalize them here.
// Without finalizing, the rows stay sending=1
// and re-arm into duplicates on restart.
// Re-queued sends (fallback, postpone, migrate) are left to re-arm,
// their normal path.
// Unlike completeSendResult, it never frees the slot:
// no new send may start while exiting.
func (w *worker) drainSendResults() {
	for {
		select {
		case r := <-w.sendResults:
			w.resolveResultUser(&r)
			w.handleSendResult(r)
			// A resend or a targetless migrate re-arms next start;
			// finalize only a genuinely finished send.
			switch d := r.disposition(); {
			case d == dispFinalize:
				w.finalizeNotification(r.notificationID, r.userID, true)
			case d == dispResend && r.notificationID == 0:
				// A postponed reply cannot re-arm (no row) and is not queued
				// for logShutdownLoss to count, so note the drop here.
				ldbg("shutdown dropped a postponed reply")
			}
		default:
			return
		}
	}
}

// logShutdownLoss reports what exiting drops.
// Queued notifications re-send next start;
// other queued messages and the buffered updates are lost.
// The re-send count reads the queue only,
// so a notification drained as a re-arm
// (a postpone or fallback landing mid-shutdown) is not counted:
// an accepted undercount in a log line.
func (w *worker) logShutdownLoss(bufferedUpdates int) {
	var messages, notifications int
	for _, u := range w.sendQueue.byUser {
		for _, q := range u.items {
			if q.notificationID == 0 {
				messages++
			} else {
				notifications++
			}
		}
	}
	if messages+notifications+bufferedUpdates == 0 {
		return
	}
	linf("shutdown dropped %d messages and %d unprocessed updates (lost); %d notifications will re-send",
		messages, bufferedUpdates, notifications)
}

// drainIncoming processes updates the webhook 200'd but the loop hasn't,
// for their DB side effects (e.g. successful_payment credits).
// It consumes while shutdownBots flushes the library buffers into incoming,
// so a full channel cannot park the flush, then drains what is left.
// It keeps pumping send results and cooldowns meanwhile,
// so queued replies and notifications still flow while the budget lasts.
// Bounded by ctx, like every other shutdown phase.
func (w *worker) drainIncoming(ctx context.Context, incoming chan incomingPacket, botsDone <-chan struct{}) {
	n := 0
	deadline := func() {
		linf("shutdown: drain deadline hit after %d update(s)", n)
	}
	for {
		// After the flush no producers remain, so an empty channel is drained.
		if botsDone == nil && len(incoming) == 0 {
			if n > 0 {
				linf("shutdown: processed %d buffered update(s) before exit", n)
			}
			return
		}
		select {
		case u := <-incoming:
			w.processTGUpdate(u)
			n++
			if ctx.Err() != nil {
				deadline()
				return
			}
		case r := <-w.sendResults:
			w.completeSendResult(r)
		case userID := <-w.cooledUsers:
			w.onUserCooled(userID)
		case <-botsDone:
			botsDone = nil
		case <-ctx.Done():
			deadline()
			return
		}
	}
}

// shutdownBots stops every bot with Shutdown,
// draining its buffered updates (already 200'd)
// into incomingPackets for drainIncoming.
// Bots run concurrently under the shared ctx,
// so one stalled bot can't eat the whole budget and starve the rest.
func (w *worker) shutdownBots(ctx context.Context) {
	var wg sync.WaitGroup
	for n := range w.bots {
		wg.Go(func() {
			if err := w.bots[n].Shutdown(ctx); err != nil {
				linf("shutdown: bot %s drain: %v", n, err)
			}
		})
	}
	wg.Wait()
}

func (w *worker) sendReadyNotifications() { w.sendingNotifications <- w.db.NewNotifications() }

// notificationFetcher downloads notification images off the main goroutine,
// then hands the batch back for the main loop to enqueue.
func (w *worker) notificationFetcher() {
	for nots := range w.sendingNotifications {
		images := map[string][]byte{}
		if w.cfg.ShowImages {
			images = w.downloadImages(nots)
		}
		w.imagedNotifications <- notificationBatch{notifications: nots, images: images}
	}
}

func (w *worker) fuzzySearchDaemon() {
	for req := range w.searchRequests {
		w.handleSearchRequest(req)
	}
}

// searchCommand names a search made from the web app.
const searchCommand = "search"

// handleSearchRequest serves one search from the web app.
// It runs on the search daemon's goroutine, which owns fuzzySearchDB.
// searchCaller vets the chat before the request is queued;
// the guard repeats here because EnsureUser is the write it protects.
func (w *worker) handleSearchRequest(req searchRequest) {
	if !w.admitChat("search", req.chatID) {
		// searchCaller vets before queueing, so a rejection here is a broken invariant.
		lerr("search request not vetted: chat = %d", req.chatID)
		req.resultCh <- searchResult{}
		return
	}
	now := int(time.Now().Unix())
	// A search means the user found the bot on their own,
	// so create the row now;
	// a later referral /start then correctly earns no follower bonus.
	w.fuzzySearchDB.LogReceivedMessage(
		now, req.endpoint,
		w.fuzzySearchDB.EnsureUser(req.chatID), searchCommand)
	req.resultCh <- searchResult{streamers: w.fuzzySearchDB.SearchStreamers(req.term), allowed: true}
}

func (w *worker) queryUnconfirmedSubs() {
	caps := w.checker.Capabilities()
	if !caps.SupportsQueryStatus && !caps.SupportsQueryFixedListStatuses {
		return
	}
	unconfirmed := map[string]bool{}
	var nickname string
	w.db.MustQuery("select nickname from pending_subscriptions where not checking", nil, db.ScanTo{&nickname}, func() { unconfirmed[nickname] = true })
	if len(unconfirmed) > 0 {
		w.db.MarkUnconfirmedAsChecking()
		ldbg("queueing unconfirmed subscriptions check for %d streamers", len(unconfirmed))
		if w.pushExistenceRequest(unconfirmed) != nil {
			w.db.ResetCheckingToUnconfirmed()
		}
	}
}

func (w *worker) processSubsConfirmations(res *cmdlib.ExistenceListResults) {
	streamersNumber := len(res.Streamers)
	ldbg("processing subscription confirmations for %d streamers", streamersNumber)
	nicknames := make([]string, 0, len(res.Streamers))
	for n := range res.Streamers {
		nicknames = append(nicknames, n)
	}
	confirmationsInWork := map[string][]db.PendingSubscription{}
	var iter db.PendingSubscription
	w.db.MustQuery(
		`
			select ps.endpoint, ps.nickname, ps.user_id, ps.referral, coalesce(ps.command, ''), ps.reply_seq
			from pending_subscriptions ps
			where ps.checking and ps.nickname = any($1)
		`,
		db.QueryParams{nicknames},
		db.ScanTo{&iter.Endpoint, &iter.Nickname, &iter.UserID, &iter.Referral, &iter.Command, &iter.ReplySeq},
		func() { confirmationsInWork[iter.Nickname] = append(confirmationsInWork[iter.Nickname], iter) })
	var nots []db.Notification
	var confirmedNots []db.Notification
	if !res.Failed() {
		for nickname, info := range res.Streamers {
			for _, sub := range confirmationsInWork[nickname] {
				confirmed := info.Status&(cmdlib.StatusOnline|cmdlib.StatusOffline|cmdlib.StatusDenied) != 0
				var streamerID *int
				if confirmed {
					id := w.db.ConfirmSub(sub)
					streamerID = &id
					if sub.Referral {
						now := int(time.Now().Unix())
						w.db.AddReferralEvent(now, nil, sub.UserID, streamerID)
					}
				} else {
					w.db.DenySub(sub)
				}
				n := db.Notification{
					Endpoint:   sub.Endpoint,
					UserID:     sub.UserID,
					StreamerID: streamerID,
					Nickname:   nickname,
					Status:     info.Status,
					Social:     false,
					Priority:   db.PriorityHigh,
					Kind:       db.ReplyPacket,
					Command:    sub.Command,
					ReplySeq:   sub.ReplySeq,
				}
				nots = append(nots, n)
				if confirmed {
					// The status follows the add result, so it takes the next number.
					n.ReplySeq++
					confirmedNots = append(confirmedNots, n)
				}
			}
		}
	} else {
		lerr("confirmations query failed")
		w.db.ResetCheckingToUnconfirmed()
	}
	w.notifyOfAddResults(db.PriorityHigh, nots)
	w.db.StoreNotifications(confirmedNots)
}

// startupTimers holds the main loop's periodic timer channels.
type startupTimers struct {
	request            <-chan time.Time
	maintainDB         <-chan time.Time
	subsConfirm        <-chan time.Time
	notificationSender <-chan time.Time
}

// finishStartup completes the loop-owned initialization
// once the database is ready: the periodic timers, the checker daemon,
// signal handling, the queued we-are-up replies, and the payment gate.
func (w *worker) finishStartup(
	ctx context.Context,
	cancel context.CancelFunc,
	waitingUsers map[waitingUser]bool,
) startupTimers {
	timers := startupTimers{
		request:            time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second).C,
		subsConfirm:        time.NewTicker(time.Duration(w.cfg.SubsConfirmationPeriodSeconds) * time.Second).C,
		notificationSender: time.NewTicker(time.Duration(w.cfg.NotificationsReadyPeriodSeconds) * time.Second).C,
	}
	if w.cfg.MaintainDBPeriodSeconds != 0 {
		timers.maintainDB = time.NewTicker(time.Duration(w.cfg.MaintainDBPeriodSeconds) * time.Second).C
	}
	checkers.StartCheckerDaemon(ctx, w.checker)
	// Install signals only now: a SIGTERM during migrations
	// should kill the process outright,
	// not run shutdown against a half-migrated schema.
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	go func() {
		s := <-signals
		linf("got signal %v, shutting down", s)
		cancel()
	}()
	for user := range waitingUsers {
		w.sendTr(
			db.PriorityHigh, user.endpoint, w.db.EnsureUser(user.chatID), false,
			w.tr[user.endpoint].WeAreUp, nil, unprompted(db.MessagePacket))
	}
	w.sendText(
		db.PriorityHigh,
		w.cfg.OwnerEndpoint,
		w.ownerUserID,
		true,
		true,
		cmdlib.ParseRaw,
		"bot is up",
		unprompted(db.MessagePacket))
	w.pushOnlineRequest()
	// Leave maintenance last: it opens the payment gate,
	// so an admitted payment is consumed at once.
	w.maintenance.Store(false)
	return timers
}

func main() {
	version := pflag.BoolP("version", "v", false, "prints current version")
	printCfg := pflag.BoolP("print-config", "p", false, "print config and exit")
	botCfgPath := pflag.String("bot-config", "", "path to the bot config file (required)")
	checkerCfgPath := pflag.String("checker-config", "", "path to the checker config file (required)")
	pflag.Parse()
	if *version {
		fmt.Println(cmdlib.Version)
		os.Exit(0)
	}
	if *botCfgPath == "" || *checkerCfgPath == "" {
		fmt.Fprintln(os.Stderr, "both --bot-config and --checker-config are required")
		pflag.Usage()
		os.Exit(2)
	}

	cfg := botconfig.ReadConfig(*botCfgPath)
	cmdlib.SetVerbosity(cfg.Debug)
	checker, err := checkers.Build(cfg.Website, *checkerCfgPath)
	checkErr(err)
	if *printCfg {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		fmt.Println("bot config:")
		checkErr(enc.Encode(cfg))
		fmt.Println("checker config:")
		checkErr(enc.Encode(checker.Config()))
		os.Exit(0)
	}

	w := newWorker(cfg, checker)
	w.logConfig()
	// Before Telegram is pointed at this process: a zone table it cannot work from
	// stops a start rather than a bot that has already announced itself.
	// newWorker has connected to the server, so this waits on nothing further.
	w.initTimezones()
	w.setWebhook()
	w.setCommands()
	w.setDefaultAdminRights()
	w.initBotNames()
	w.serveEndpoints()
	incoming := w.incoming()
	go w.notificationFetcher()
	go w.fuzzySearchDaemon()
	// MaintenancePacket: the loop consumes its send result
	// without touching the database, which is not created yet.
	w.sendMaintenance(w.cfg.OwnerEndpoint, w.cfg.OwnerID, true, "bot started")
	// The main loop below owns the scheduler from the start;
	// the database work and the init that needs it run off the loop
	// and report back on databaseDone, which publishes their writes.
	// Suspend the single-goroutine check while that init runs off the loop.
	w.db.SuspendGIDCheck()
	databaseDone := make(chan bool)
	go func() {
		w.createDatabase()
		w.ownerUserID = w.db.EnsureUser(w.cfg.OwnerID)
		w.initCache()
		w.db.ResetNotificationSending()
		w.db.ResetCheckingToUnconfirmed()
		// Register the web app last:
		// once its routes exist, requests can reach the loop's channels
		// and must find the caches ready.
		w.registerWebApp()
		databaseDone <- true
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitingUsers := map[waitingUser]bool{}
	// The timers stay nil until the database is ready,
	// keeping every database-touching select arm dormant.
	var timers startupTimers
	for {
		select {
		case <-databaseDone:
			// The loop owns the database now; resume the single-goroutine check.
			w.db.ResumeGIDCheck()
			timers = w.finishStartup(ctx, cancel, waitingUsers)
		case <-timers.request:
			runtime.GC()
			w.periodic()
		case <-timers.maintainDB:
			w.maintainDB()
		case <-timers.subsConfirm:
			w.queryUnconfirmedSubs()
		case <-timers.notificationSender:
			w.sendReadyNotifications()
		case result := <-w.checkerResults:
			now := int(time.Now().Unix())
			processed := w.handleCheckerResults(result, now)
			updateQueryFields := map[string]any{
				"failed": result.Failed(),
				"count":  result.Count(),
			}
			maps.Copy(updateQueryFields, result.ExtraLogFields())
			w.db.LogPerformance(now, db.PerformanceLogUpdateQuery, int(result.Duration().Milliseconds()), updateQueryFields)
			w.db.LogPerformance(now, db.PerformanceLogUpdateProcessing, int(processed.elapsed.Milliseconds()), map[string]any{
				"unconfirmed_count":                    processed.unconfirmedChangesCount,
				"unconfirmed_offline_count":            processed.unconfirmedOfflineCount,
				"unconfirmed_online_count":             processed.unconfirmedOnlineCount,
				"confirmed_count":                      processed.confirmedChangesCount,
				"notifications_count":                  len(processed.notifications),
				"upsert_unconfirmed_streamers_ms":      processed.upsertTimings.UpsertStreamersMs,
				"insert_nicknames_ms":                  processed.upsertTimings.InsertNicknamesMs,
				"insert_unconfirmed_status_changes_ms": processed.upsertTimings.InsertStatusChangesMs,
				"commit_unconfirmed_ms":                processed.upsertTimings.CommitMs,
				"summarize_brin_ms":                    processed.upsertTimings.SummarizeBrinMs,
				"confirm_changes_ms":                   processed.confirmChangesMs,
				"store_notifications_ms":               processed.storeNotificationsMs,
			})
			ldbg("status updates processed in %v", processed.elapsed)
		case req := <-w.webAppAddRequests:
			w.performWebAppAdd(req)
		case req := <-w.webAppTimezoneRequests:
			w.performWebAppTimezone(req)
		case u := <-incoming:
			if w.maintenance.Load() {
				w.maintenanceReply(u, waitingUsers)
			} else {
				w.processTGUpdate(u)
			}
		case <-ctx.Done():
			w.shutdown(incoming)
			return
		case r := <-w.sendResults:
			w.completeSendResult(r)
		case userID := <-w.cooledUsers:
			w.onUserCooled(userID)
		case r := <-w.existenceListResults:
			now := int(time.Now().Unix())
			w.db.LogPerformance(now, db.PerformanceLogExistenceQuery, int(r.Duration().Milliseconds()), map[string]any{
				"failed": r.Failed(),
				"count":  r.Count(),
			})
			w.processSubsConfirmations(r)
		case batch := <-w.imagedNotifications:
			w.enqueueNotifications(batch)
		case r := <-w.imageDownloadLogs:
			now := int(time.Now().Unix())
			w.db.LogPerformance(now, db.PerformanceLogImageDownload, r.durationMs, map[string]any{
				"failed": !r.success,
			})
		}
	}
}
