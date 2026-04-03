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
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	texttemplate "text/template"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/bcmk/siren/v2/internal/botconfig"
	"github.com/bcmk/siren/v2/internal/checkers"
	"github.com/bcmk/siren/v2/internal/db"
	"github.com/bcmk/siren/v2/lib/cmdlib"
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

type worker struct {
	db                         db.Database
	fuzzySearchDB              db.Database
	clients                    []*cmdlib.Client
	bots                       map[string]*bot.Bot
	cfg                        *botconfig.Config
	tr                         map[string]*cmdlib.Translations
	tpl                        map[string]*texttemplate.Template
	trAds                      map[string]map[string]*cmdlib.Translation
	tplAds                     map[string]*texttemplate.Template
	nicknamePreprocessing      func(string) string
	checker                    cmdlib.Checker
	imageDownloadLogs          chan imageDownloadLog
	unconfirmedOnlineStreamers map[string]cmdlib.StreamerInfo
	botNames                   map[string]string
	outgoingMsgCh              chan outgoingPacket
	outgoingMsgResults         chan msgSendResult
	existenceListResults       chan *cmdlib.ExistenceListResults
	checkerResults             chan cmdlib.CheckerResults
	sendingNotifications       chan []db.Notification
	sentNotifications          chan []db.Notification
	ourIDs                     []int64
	nicknameRegexp             *regexp.Regexp
	searchHTML                 *htmltemplate.Template
	searchRequests             chan searchRequest
	addRequests                chan addRequest
	incomingPackets            chan incomingPacket
}

type searchRequest struct {
	endpoint string
	userID   int64
	term     string
	resultCh chan []string
}

type addRequest struct {
	endpoint string
	chatID   int64
	nickname string
	doneCh   chan struct{}
}

type incomingPacket struct {
	message  *models.Update
	endpoint string
}

type outgoingPacket struct {
	message     sendable
	endpoint    string
	requestedAt time.Time
	kind        db.PacketKind
	priority    db.Priority
	seq         uint64
	heapIndex   int
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
)

type msgSendResult struct {
	priority  db.Priority
	timestamp int
	result    int
	endpoint  string
	chatID    int64
	latency   int
	kind      db.PacketKind
}

type waitingUser struct {
	chatID   int64
	endpoint string
}

type imageDownloadLog struct {
	success    bool
	durationMs int
}

func newWorker(cfg *botconfig.Config) *worker {
	var clients []*cmdlib.Client
	for _, address := range cfg.SourceIPAddresses {
		clients = append(clients, cmdlib.HTTPClientWithTimeoutAndAddress(cfg.TimeoutSeconds, address, cfg.EnableCookies))
	}

	incomingPackets := make(chan incomingPacket)
	telegramClient := cmdlib.HTTPClientWithTimeoutAndAddress(cfg.TelegramTimeoutSeconds, "", false)
	bots := make(map[string]*bot.Bot)
	for n, p := range cfg.Endpoints {
		endpointName := n
		handler := func(_ context.Context, _ *bot.Bot, update *models.Update) {
			incomingPackets <- incomingPacket{message: update, endpoint: endpointName}
		}
		b, err := bot.New(string(p.BotToken), bot.WithHTTPClient(0, telegramClient.Client), bot.WithDefaultHandler(handler))
		checkErr(err)
		bots[n] = b
	}
	tr, tpl := cmdlib.LoadAllTranslations(trsByEndpoint(cfg))
	trAds, tplAds := cmdlib.LoadAllAds(trsAdsByEndpoint(cfg))
	for _, t := range tpl {
		texttemplate.Must(t.New("affiliate_link").Parse(cfg.AffiliateLink))
	}
	w := &worker{
		bots:                       bots,
		db:                         db.NewDatabase(string(cfg.DBConnectionString), cfg.CheckGID),
		fuzzySearchDB:              db.NewDatabase(string(cfg.DBConnectionString), false),
		cfg:                        cfg,
		clients:                    clients,
		tr:                         tr,
		tpl:                        tpl,
		trAds:                      trAds,
		tplAds:                     tplAds,
		imageDownloadLogs:          make(chan imageDownloadLog),
		unconfirmedOnlineStreamers: map[string]cmdlib.StreamerInfo{},
		botNames:                   map[string]string{},
		outgoingMsgCh:              make(chan outgoingPacket, maxHeapLen),
		outgoingMsgResults:         make(chan msgSendResult),
		existenceListResults:       make(chan *cmdlib.ExistenceListResults),
		checkerResults:             make(chan cmdlib.CheckerResults),
		sendingNotifications:       make(chan []db.Notification, 1000),
		sentNotifications:          make(chan []db.Notification),
		ourIDs:                     getOurIDs(cfg),
		searchRequests:             make(chan searchRequest),
		addRequests:                make(chan addRequest),
		incomingPackets:            incomingPackets,
	}
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

	switch cfg.Website {
	case "test":
		w.checker = &checkers.RandomChecker{}
		w.nicknamePreprocessing = cmdlib.CanonicalNicknamePreprocessing
		w.nicknameRegexp = cmdlib.CommonNicknameRegexp
	case "bongacams":
		w.checker = &checkers.BongaCamsChecker{}
		w.nicknamePreprocessing = cmdlib.CanonicalNicknamePreprocessing
		w.nicknameRegexp = cmdlib.CommonNicknameRegexp
	case "chaturbate":
		w.checker = &checkers.ChaturbateChecker{}
		w.nicknamePreprocessing = checkers.ChaturbateCanonicalModelID
		w.nicknameRegexp = cmdlib.CommonNicknameRegexp
	case "stripchat":
		w.checker = &checkers.StripchatChecker{}
		w.nicknamePreprocessing = cmdlib.CanonicalNicknamePreprocessing
		w.nicknameRegexp = cmdlib.CommonNicknameRegexp
	case "livejasmin":
		w.checker = &checkers.LiveJasminChecker{}
		w.nicknamePreprocessing = cmdlib.CanonicalNicknamePreprocessing
		w.nicknameRegexp = cmdlib.CommonNicknameRegexp
	case "camsoda":
		w.checker = &checkers.CamSodaChecker{}
		w.nicknamePreprocessing = cmdlib.CanonicalNicknamePreprocessing
		w.nicknameRegexp = cmdlib.CommonNicknameRegexp
	case "flirt4free":
		w.checker = &checkers.Flirt4FreeChecker{}
		w.nicknamePreprocessing = checkers.Flirt4FreeCanonicalModelID
		w.nicknameRegexp = cmdlib.CommonNicknameRegexp
	case "streamate":
		w.checker = &checkers.StreamateChecker{}
		w.nicknamePreprocessing = cmdlib.CanonicalNicknamePreprocessing
		w.nicknameRegexp = cmdlib.CommonNicknameRegexp
	case "twitch":
		w.checker = &checkers.TwitchChecker{}
		w.nicknamePreprocessing = checkers.TwitchCanonicalChannelID
		w.nicknameRegexp = checkers.TwitchChannelIDRegexp
	case "cam4":
		w.checker = &checkers.Cam4Checker{}
		w.nicknamePreprocessing = checkers.Cam4CanonicalModelID
		w.nicknameRegexp = checkers.Cam4ModelIDRegexp
	default:
		panic("wrong website")
	}

	searchHTMLBytes, err := os.ReadFile("res/webapp/search.html")
	checkErr(err)
	w.searchHTML, err = htmltemplate.New("search").Parse(string(searchHTMLBytes))
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
		if p.WebhookDomain == "" {
			continue
		}
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

func (w *worker) removeWebhook() {
	ctx := context.Background()
	for n := range w.cfg.Endpoints {
		linf("removing webhook for endpoint %s...", n)
		_, err := w.bots[n].DeleteWebhook(ctx, &bot.DeleteWebhookParams{})
		checkErr(err)
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
			commands = append(commands, models.BotCommand{Command: pair[0], Description: pair[1]})
			if w.cfg.Debug {
				ldbg("command %s - %s", pair[0], pair[1])
			}
		}
		linf("setting commands for endpoint %s...", n)
		_, err := w.bots[n].SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: commands})
		checkErr(err)
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
	chatID int64,
	notify bool,
	disablePreview bool,
	parse cmdlib.ParseKind,
	text string,
	kind db.PacketKind,
) {
	params := &bot.SendMessageParams{
		ChatID:              chatID,
		Text:                text,
		DisableNotification: !notify,
	}
	if disablePreview {
		disabled := true
		params.LinkPreviewOptions = &models.LinkPreviewOptions{IsDisabled: &disabled}
	}
	switch parse {
	case cmdlib.ParseHTML:
		params.ParseMode = models.ParseModeHTML
	case cmdlib.ParseMarkdown:
		params.ParseMode = models.ParseModeMarkdown
	}
	w.enqueueMessage(priority, endpoint, &messageParams{params}, kind)
}

func (w *worker) sendImage(
	priority db.Priority,
	endpoint string,
	chatID int64,
	notify bool,
	parse cmdlib.ParseKind,
	text string,
	img []byte,
	kind db.PacketKind,
) {
	params := &bot.SendPhotoParams{
		ChatID:              chatID,
		Caption:             text,
		DisableNotification: !notify,
	}
	switch parse {
	case cmdlib.ParseHTML:
		params.ParseMode = models.ParseModeHTML
	case cmdlib.ParseMarkdown:
		params.ParseMode = models.ParseModeMarkdown
	}
	w.enqueueMessage(priority, endpoint, &photoParams{SendPhotoParams: params, imageData: img}, kind)
}

func (w *worker) sendMessageInternal(endpoint string, msg sendable) int {
	chatID := msg.chatID()
	if !w.cfg.ChatWhitelisted(chatID) {
		return messageSkipped
	}
	ctx := context.Background()
	if _, err := msg.send(ctx, w.bots[endpoint]); err != nil {
		var migrateErr *bot.MigrateError
		if errors.As(err, &migrateErr) {
			if w.cfg.Debug {
				ldbg("cannot send a message, group migration")
			}
			return messageMigrate
		}
		var tooManyErr *bot.TooManyRequestsError
		if errors.As(err, &tooManyErr) {
			if w.cfg.Debug {
				ldbg("cannot send a message, too many requests")
			}
			return messageTooManyRequests
		}
		if errors.Is(err, bot.ErrorForbidden) {
			if w.cfg.Debug {
				ldbg("cannot send a message, bot blocked")
			}
			return messageBlocked
		}
		if errors.Is(err, bot.ErrorBadRequest) {
			if strings.Contains(err.Error(), "chat not found") {
				if w.cfg.Debug {
					ldbg("cannot send a message, chat not found")
				}
				return messageChatNotFound
			}
			if strings.Contains(err.Error(), "not enough rights to send photos") {
				if w.cfg.Debug {
					ldbg("cannot send a message, no photo rights")
				}
				return messageNoPhotoRights
			}
			lerr("cannot send a message, bad request, error: %v", err)
			return messageBadRequest
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			if netErr.Timeout() {
				if w.cfg.Debug {
					ldbg("cannot send a message, timeout")
				}
				return messageTimeout
			}
			lerr("cannot send a message, unknown network error")
			return messageUnknownNetworkError
		}
		lerr("unexpected error type while sending a message to %d, %v", chatID, err)
		return messageUnknownError
	}
	return messageSent
}

func templateToString(t *texttemplate.Template, key string, data map[string]interface{}) string {
	buf := &bytes.Buffer{}
	err := t.ExecuteTemplate(buf, key, data)
	checkErr(err)
	return buf.String()
}

func (w *worker) sendTr(
	priority db.Priority,
	endpoint string,
	chatID int64,
	notify bool,
	translation *cmdlib.Translation,
	data map[string]interface{},
	kind db.PacketKind,
) {
	tpl := w.tpl[endpoint]
	text := templateToString(tpl, translation.Key, data)
	w.sendText(priority, endpoint, chatID, notify, translation.DisablePreview, translation.Parse, text, kind)
}

func (w *worker) sendAdsTr(
	priority db.Priority,
	endpoint string,
	chatID int64,
	notify bool,
	translation *cmdlib.Translation,
	data map[string]interface{},
) {
	tpl := w.tplAds[endpoint]
	text := templateToString(tpl, translation.Key, data)
	if translation.Image == "" {
		w.sendText(priority, endpoint, chatID, notify, translation.DisablePreview, translation.Parse, text, db.AdPacket)
	} else {
		w.sendImage(priority, endpoint, chatID, notify, translation.Parse, text, translation.ImageBytes, db.AdPacket)
	}
}

func (w *worker) sendTrImage(
	priority db.Priority,
	endpoint string,
	chatID int64,
	notify bool,
	translation *cmdlib.Translation,
	data map[string]interface{},
	image []byte,
	kind db.PacketKind,
) {
	tpl := w.tpl[endpoint]
	text := templateToString(tpl, translation.Key, data)
	w.sendImage(priority, endpoint, chatID, notify, translation.Parse, text, image, kind)
}

func (w *worker) createDatabase(done chan bool) {
	linf("ensuring database created...")
	for _, prelude := range w.cfg.SQLPrelude {
		w.db.MustExec(prelude)
		w.fuzzySearchDB.MustExec(prelude)
	}
	w.db.ApplyMigrations()
	done <- true
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
		data := tplData{"streamer": n.Nickname}
		if n.Status&(cmdlib.StatusOnline|cmdlib.StatusOffline|cmdlib.StatusDenied) != 0 {
			w.sendTr(priority, n.Endpoint, n.ChatID, false, w.tr[n.Endpoint].StreamerAdded, data, db.ReplyPacket)
		} else {
			w.sendTr(priority, n.Endpoint, n.ChatID, false, w.tr[n.Endpoint].AddError, data, db.ReplyPacket)
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

func (w *worker) notifyOfStatuses(notifications []db.Notification) {
	images := map[string][]byte{}
	if w.cfg.ShowImages {
		images = w.downloadImages(notifications)
	}
	for _, n := range notifications {
		w.notifyOfStatus(n.Priority, n, images[n.ImageURL], n.Social)
	}
}

func (w *worker) trAdsSlice(endpoint string) []*cmdlib.Translation {
	var res []*cmdlib.Translation
	for _, v := range w.trAds[endpoint] {
		res = append(res, v)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Key < res[j].Key })
	return res
}

func (w *worker) ad(priority db.Priority, endpoint string, chatID int64) {
	trAds := w.trAdsSlice(endpoint)
	if len(trAds) == 0 {
		return
	}
	adNum := rand.Intn(len(trAds))
	w.sendAdsTr(priority, endpoint, chatID, false, trAds[adNum], nil)
}

func (w *worker) notifyOfStatus(priority db.Priority, n db.Notification, image []byte, social bool) {
	if w.tr[n.Endpoint] == nil {
		return
	}
	if w.cfg.Debug {
		ldbg("notifying of status of the streamer %s", n.Nickname)
	}
	var timeDiff *timeDiff
	if n.TimeDiff != nil {
		temp := calcTimeDiff(*n.TimeDiff)
		timeDiff = &temp
	}
	data := tplData{
		"streamer":  n.Nickname,
		"time_diff": timeDiff,
		"viewers":   n.Viewers,
		"show_kind": n.ShowKind,
		"subject":   html.EscapeString(n.Subject),
	}
	notify := !n.SilentMessages
	switch n.Status {
	case cmdlib.StatusOnline:
		if image == nil {
			w.sendTr(priority, n.Endpoint, n.ChatID, notify, w.tr[n.Endpoint].Online, data, n.Kind)
		} else {
			w.sendTrImage(priority, n.Endpoint, n.ChatID, notify, w.tr[n.Endpoint].Online, data, image, n.Kind)
		}
	case cmdlib.StatusOffline:
		w.sendTr(priority, n.Endpoint, n.ChatID, false, w.tr[n.Endpoint].Offline, data, n.Kind)
	case cmdlib.StatusDenied:
		w.sendTr(priority, n.Endpoint, n.ChatID, false, w.tr[n.Endpoint].Denied, data, n.Kind)
	}
	if social && rand.Intn(5) == 0 {
		w.ad(priority, n.Endpoint, n.ChatID)
	}
}

func (w *worker) mustUser(chatID int64) (user db.User) {
	user, found := w.db.User(chatID)
	if !found {
		checkErr(fmt.Errorf("user not found: %d", chatID))
	}
	return
}

func (w *worker) showWeek(endpoint string, chatID int64, nickname string) {
	if nickname != "" {
		nickname = w.nicknamePreprocessing(nickname)
		if !w.nicknameRegexp.MatchString(nickname) {
			w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"streamer": nickname}, db.ReplyPacket)
			return
		}
		hours, start := w.week(nickname)
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].Week, tplData{
			"hours":    hours,
			"weekday":  int(start.UTC().Weekday()),
			"streamer": nickname,
		}, db.ReplyPacket)
		return
	}
	streamers := w.db.StreamersForChat(endpoint, chatID)
	if len(streamers) == 0 {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].ZeroSubscriptions, nil, db.ReplyPacket)
		return
	}
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].WeekRetrieving, nil, db.ReplyPacket)
	ids := make([]int, len(streamers))
	for i, s := range streamers {
		ids[i] = s.ID
	}
	now := time.Now()
	hoursMap, start := w.weekForStreamers(ids, now)
	statuses := w.db.UnconfirmedStatusesForChat(endpoint, chatID)
	statusMap := make(map[string]db.Streamer, len(statuses))
	for _, s := range statuses {
		statusMap[s.Nickname] = s
	}
	type streamerData struct {
		Streamer string
		TimeDiff *timeDiff
	}
	var weeks []string
	var neverOnline []streamerData
	nowUnix := int(now.Unix())
	for _, s := range streamers {
		hours := hoursMap[s.ID]
		if !slices.Contains(hours, true) {
			var td *timeDiff
			if st, ok := statusMap[s.Nickname]; ok {
				td = w.streamerTimeDiff(st, nowUnix)
			}
			neverOnline = append(neverOnline, streamerData{
				Streamer: s.Nickname,
				TimeDiff: td,
			})
			continue
		}
		weeks = append(weeks, templateToString(w.tpl[endpoint], w.tr[endpoint].Week.Key, tplData{
			"hours":    hours,
			"weekday":  int(start.UTC().Weekday()),
			"streamer": s.Nickname,
		}))
	}
	for chunk := range slices.Chunk(weeks, 10) {
		w.sendText(db.PriorityLow, endpoint, chatID, false, true, w.tr[endpoint].Week.Parse, strings.Join(chunk, "\n\n"), db.ReplyPacket)
	}
	for chunk := range slices.Chunk(neverOnline, 50) {
		w.sendTr(db.PriorityLow, endpoint, chatID, false, w.tr[endpoint].WeekNeverOnline, tplData{"streamers": chunk}, db.ReplyPacket)
	}
}

func (w *worker) addStreamer(endpoint string, chatID int64, nickname string, now int, referral bool) *int {
	if nickname == "" {
		tr := w.tr[endpoint].SyntaxAdd
		text := templateToString(w.tpl[endpoint], tr.Key, nil)
		params := &bot.SendMessageParams{
			ChatID: chatID,
			Text:   text,
		}
		if chatID > 0 && !w.checker.UsesFixedList() {
			params.ReplyMarkup = &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{{
					{
						Text:   w.tr[endpoint].SearchButton.Str,
						WebApp: &models.WebAppInfo{URL: w.webAppURL(endpoint)},
					},
				}},
			}
		}
		if tr.DisablePreview {
			disabled := true
			params.LinkPreviewOptions = &models.LinkPreviewOptions{IsDisabled: &disabled}
		}
		switch tr.Parse {
		case cmdlib.ParseHTML:
			params.ParseMode = models.ParseModeHTML
		case cmdlib.ParseMarkdown:
			params.ParseMode = models.ParseModeMarkdown
		}
		w.enqueueMessage(db.PriorityHigh, endpoint, &messageParams{params}, db.ReplyPacket)
		return nil
	}
	nickname = w.nicknamePreprocessing(nickname)
	if !w.nicknameRegexp.MatchString(nickname) {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"streamer": nickname}, db.ReplyPacket)
		return nil
	}

	if w.db.SubscribedOrPending(endpoint, chatID, nickname) {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].AlreadyAdded, tplData{"streamer": nickname}, db.ReplyPacket)
		return nil
	}
	subscriptionsNumber := w.db.SubscribedOrPendingCount(endpoint, chatID)
	user := w.mustUser(chatID)
	if subscriptionsNumber >= user.MaxSubs {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].NotEnoughSubscriptions, nil, db.ReplyPacket)
		w.subscriptionUsage(endpoint, chatID, true)
		return nil
	}
	streamer := w.db.MaybeStreamer(nickname)
	if streamer == nil {
		w.db.AddPendingSubscription(chatID, nickname, endpoint, referral)
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].CheckingStreamer, nil, db.ReplyPacket)
		return nil
	}
	confirmedStatus := streamer.ConfirmedStatus
	if confirmedStatus != cmdlib.StatusOnline {
		confirmedStatus = cmdlib.StatusOffline
	}
	w.db.AddSubscription(chatID, streamer.ID, endpoint)
	subscriptionsNumber++
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].StreamerAdded, tplData{"streamer": nickname}, db.ReplyPacket)
	nots := []db.Notification{{
		Endpoint:   endpoint,
		ChatID:     chatID,
		StreamerID: &streamer.ID,
		Nickname:   nickname,
		Status:     confirmedStatus,
		TimeDiff:   w.streamerDuration(*streamer, now),
		Social:     false,
		Priority:   db.PriorityHigh,
		Kind:       db.ReplyPacket}}
	if subscriptionsNumber >= user.MaxSubs-w.cfg.HeavyUserRemainder {
		w.subscriptionUsage(endpoint, chatID, true)
	}
	w.db.StoreNotifications(nots)
	return &streamer.ID
}

func (w *worker) subscriptionUsage(endpoint string, chatID int64, ad bool) {
	subscriptionsNumber := w.db.SubscribedOrPendingCount(endpoint, chatID)
	user := w.mustUser(chatID)
	tr := w.tr[endpoint].SubscriptionUsage
	if ad {
		tr = w.tr[endpoint].SubscriptionUsageAd
	}
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, tr,
		tplData{
			"subscriptions_used":  subscriptionsNumber,
			"total_subscriptions": user.MaxSubs,
		},
		db.ReplyPacket)
}

func (w *worker) wantMore(endpoint string, chatID int64) {
	w.showReferral(endpoint, chatID)
}

func (w *worker) settings(endpoint string, chatID int64) {
	subscriptionsNumber := w.db.SubscribedOrPendingCount(endpoint, chatID)
	user := w.mustUser(chatID)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].Settings, tplData{
		"subscriptions_used":              subscriptionsNumber,
		"total_subscriptions":             user.MaxSubs,
		"show_images":                     user.ShowImages,
		"offline_notifications_supported": w.cfg.OfflineNotifications,
		"offline_notifications":           user.OfflineNotifications,
		"subject_supported":               w.checker.SubjectSupported(),
		"show_subject":                    user.ShowSubject,
		"silent_messages":                 user.SilentMessages,
	}, db.ReplyPacket)
}

func (w *worker) enableImages(endpoint string, chatID int64, showImages bool) {
	w.db.SetShowImages(chatID, showImages)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].OK, nil, db.ReplyPacket)
}

func (w *worker) enableOfflineNotifications(endpoint string, chatID int64, offlineNotifications bool) {
	w.db.SetOfflineNotifications(chatID, offlineNotifications)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].OK, nil, db.ReplyPacket)
}

func (w *worker) enableSubject(endpoint string, chatID int64, showSubject bool) {
	w.db.SetShowSubject(chatID, showSubject)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].OK, nil, db.ReplyPacket)
}

func (w *worker) enableSilentMessages(endpoint string, chatID int64, silentMessages bool) {
	w.db.SetSilentMessages(chatID, silentMessages)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].OK, nil, db.ReplyPacket)
}

func (w *worker) removeStreamer(endpoint string, chatID int64, nickname string) {
	if nickname == "" {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].SyntaxRemove, nil, db.ReplyPacket)
		return
	}
	nickname = w.nicknamePreprocessing(nickname)
	if !w.nicknameRegexp.MatchString(nickname) {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"streamer": nickname}, db.ReplyPacket)
		return
	}
	if !w.db.SubscribedOrPending(endpoint, chatID, nickname) {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].StreamerNotInList, tplData{"streamer": nickname}, db.ReplyPacket)
		return
	}
	w.db.RemoveSubscription(chatID, nickname, endpoint)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].StreamerRemoved, tplData{"streamer": nickname}, db.ReplyPacket)
}

func (w *worker) sureRemoveAll(endpoint string, chatID int64) {
	w.db.RemoveAllSubscriptions(chatID, endpoint)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].AllStreamersRemoved, nil, db.ReplyPacket)
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

func (w *worker) listStreamers(endpoint string, chatID int64, now int) {
	type data struct {
		Streamer string
		TimeDiff *timeDiff
	}
	statuses := w.db.UnconfirmedStatusesForChat(endpoint, chatID)
	if len(statuses) == 0 {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].ZeroSubscriptions, nil, db.ReplyPacket)
		return
	}
	sort.SliceStable(statuses, func(i, j int) bool {
		return listStreamersSortWeight(statuses[i].UnconfirmedStatus) < listStreamersSortWeight(statuses[j].UnconfirmedStatus)
	})
	chunks := chunkStreamers(statuses, 50)
	for _, chunk := range chunks {
		var online, offline []data
		for _, s := range chunk {
			data := data{
				Streamer: s.Nickname,
				TimeDiff: w.streamerTimeDiff(s, now),
			}
			switch s.UnconfirmedStatus {
			case cmdlib.StatusOnline:
				online = append(online, data)
			default:
				offline = append(offline, data)
			}
		}
		tplData := tplData{"online": online, "offline": offline}
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].List, tplData, db.ReplyPacket)
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
	resp, err := w.clients[0].Client.Get(url)
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

func (w *worker) listOnlineStreamers(endpoint string, chatID int64, now int) {
	statuses := w.db.UnconfirmedStatusesForChat(endpoint, chatID)
	var online []db.Streamer
	for _, s := range statuses {
		if s.UnconfirmedStatus == cmdlib.StatusOnline {
			online = append(online, s)
		}
	}
	if len(online) > w.cfg.MaxSubscriptionsForPics && chatID < -1 {
		data := tplData{"max_subs": w.cfg.MaxSubscriptionsForPics}
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].TooManySubscriptionsForPics, data, db.ReplyPacket)
		return
	}
	if len(online) == 0 {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].NoOnlineStreamers, nil, db.ReplyPacket)
		return
	}
	user := w.mustUser(chatID)
	var nots []db.Notification
	for _, s := range online {
		info := w.unconfirmedOnlineStreamers[s.Nickname]
		not := db.Notification{
			Priority:   db.PriorityHigh,
			Endpoint:   endpoint,
			ChatID:     chatID,
			StreamerID: &s.ID,
			Nickname:   s.Nickname,
			Status:     cmdlib.StatusOnline,
			ImageURL:   info.ImageURL,
			Viewers:    info.Viewers,
			ShowKind:   info.ShowKind,
			TimeDiff:   w.streamerDuration(s, now),
			Kind:       db.ReplyPacket,
		}
		if user.ShowSubject {
			not.Subject = info.Subject
		}
		nots = append(nots, not)
	}
	w.db.StoreNotifications(nots)
	w.sendTr(db.PriorityLow, endpoint, chatID, false, w.tr[endpoint].FieldsCustomizationHint, nil, db.ReplyPacket)
}

func (w *worker) week(nickname string) ([]bool, time.Time) {
	streamer := w.db.MaybeStreamer(nickname)
	if streamer == nil {
		return nil, time.Time{}
	}
	result, start := w.weekForStreamers([]int{streamer.ID}, time.Now())
	return result[streamer.ID], start
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

func (w *worker) weekForStreamers(streamerIDs []int, now time.Time) (map[int][]bool, time.Time) {
	today := now.Truncate(24 * time.Hour)
	start := today.Add(-6 * 24 * time.Hour)
	from := int(start.Unix())
	to := int(now.Unix())
	changesMap := w.db.ChangesFromToForStreamers(streamerIDs, from, to)
	return onlineCells(changesMap, from, to, 3600), start
}

func (w *worker) feedback(endpoint string, chatID int64, text string, now int) {
	if text == "" {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].SyntaxFeedback, nil, db.ReplyPacket)
		return
	}
	w.db.AddFeedback(endpoint, chatID, text, now)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].Feedback, nil, db.ReplyPacket)
	user := w.mustUser(chatID)
	if !user.Blacklist {
		finalText := fmt.Sprintf("Feedback from %d: %s", chatID, text)
		w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, true, true, cmdlib.ParseRaw, finalText, db.ReplyPacket)
	}
}

func (w *worker) performanceStat(endpoint string, arguments string) {
	parts := strings.Split(arguments, " ")
	if len(parts) > 2 {
		w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "wrong number of arguments", db.ReplyPacket)
		return
	}
	n := int64(10)
	if len(parts) == 2 {
		var err error
		n, err = strconv.ParseInt(parts[1], 10, 32)
		if err != nil {
			w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "cannot parse arguments", db.ReplyPacket)
			return
		}
	}
	durations := w.db.Durations
	var queries []string
	for x := range durations {
		queries = append(queries, x)
	}
	if len(parts) >= 1 && parts[0] == "avg" {
		sort.SliceStable(queries, func(i, j int) bool {
			return durations[queries[i]].Avg > durations[queries[j]].Avg
		})
	} else {
		sort.SliceStable(queries, func(i, j int) bool {
			return durations[queries[i]].Total() > durations[queries[j]].Total()
		})
	}
	for _, x := range queries {
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
		w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseHTML, entry, db.ReplyPacket)
		n--
	}
}

func (w *worker) broadcast(endpoint string, text string) {
	if text == "" {
		return
	}
	if w.cfg.Debug {
		ldbg("broadcasting")
	}
	chats := w.db.BroadcastChats(endpoint)
	for _, chatID := range chats {
		w.sendText(db.PriorityLow, endpoint, chatID, true, false, cmdlib.ParseRaw, text, db.MessagePacket)
	}
	w.sendText(db.PriorityLow, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
}

func (w *worker) direct(endpoint string, arguments string) {
	parts := strings.SplitN(arguments, " ", 2)
	if len(parts) < 2 {
		w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "usage: /direct chatID text", db.ReplyPacket)
		return
	}
	whom, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "first argument is invalid", db.ReplyPacket)
		return
	}
	text := parts[1]
	if text == "" {
		return
	}
	w.sendText(db.PriorityHigh, endpoint, whom, true, false, cmdlib.ParseRaw, text, db.MessagePacket)
	w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
}

func (w *worker) blacklist(endpoint string, arguments string) {
	whom, err := strconv.ParseInt(arguments, 10, 64)
	if err != nil {
		w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "first argument is invalid", db.ReplyPacket)
		return
	}
	w.db.BlacklistUser(whom)
	w.sendText(db.PriorityHigh, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
}

func (w *worker) webAppURL(endpoint string) string {
	return "https://" + w.cfg.Endpoints[endpoint].WebhookDomain +
		"/apps/add?endpoint=" + endpoint
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
	term := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("term")))
	var results []string
	if len(term) >= 1 && len(term) <= 32 {
		var userID int64
		var user struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal([]byte(values.Get("user")), &user) == nil {
			userID = user.ID
		}
		req := searchRequest{
			endpoint: endpoint,
			userID:   userID,
			term:     term,
			resultCh: make(chan []string, 1),
		}
		w.searchRequests <- req
		results = <-req.resultCh
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

func (w *worker) handleWebAppAdd(rw http.ResponseWriter, r *http.Request) {
	endpoint := r.URL.Query().Get("endpoint")
	if _, ok := w.cfg.Endpoints[endpoint]; !ok {
		lerr("web app add: bad endpoint %q", endpoint)
		http.Error(rw, "bad endpoint", http.StatusBadRequest)
		return
	}
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
	var user struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal([]byte(values.Get("user")), &user) != nil || user.ID == 0 {
		lerr("web app add: missing user id")
		http.Error(rw, "missing user id", http.StatusBadRequest)
		return
	}
	req := addRequest{
		endpoint: endpoint,
		chatID:   user.ID,
		nickname: nickname,
		doneCh:   make(chan struct{}, 1),
	}
	w.addRequests <- req
	<-req.doneCh
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
}

func (w *worker) logConfig() {
	cfgString, err := json.MarshalIndent(w.cfg, "", "    ")
	checkErr(err)
	linf("config: " + string(cfgString))
	for k, v := range w.trAds {
		linf("ads for %s: %d", k, len(v))
	}
}

func (w *worker) processAdminMessage(endpoint string, chatID int64, command, arguments string) (processed bool, maintenance bool) {
	switch command {
	case "performance":
		w.performanceStat(endpoint, arguments)
		return true, false
	case "broadcast":
		w.broadcast(endpoint, arguments)
		return true, false
	case "direct":
		w.direct(endpoint, arguments)
		return true, false
	case "blacklist":
		w.blacklist(endpoint, arguments)
		return true, false
	case "set_max_subs":
		parts := strings.Fields(arguments)
		if len(parts) != 2 {
			w.sendText(db.PriorityHigh, endpoint, chatID, false, true, cmdlib.ParseRaw, "expecting two arguments", db.ReplyPacket)
			return true, false
		}
		who, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			w.sendText(db.PriorityHigh, endpoint, chatID, false, true, cmdlib.ParseRaw, "first argument is invalid", db.ReplyPacket)
			return true, false
		}
		maxSubs, err := strconv.Atoi(parts[1])
		if err != nil {
			w.sendText(db.PriorityHigh, endpoint, chatID, false, true, cmdlib.ParseRaw, "second argument is invalid", db.ReplyPacket)
			return true, false
		}
		w.db.SetLimit(who, maxSubs)
		w.sendText(db.PriorityHigh, endpoint, chatID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
		return true, false
	case "maintenance":
		w.sendText(db.PriorityHigh, endpoint, chatID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
		return true, true
	}
	return false, false
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
	if chatID > 0 { // private chats don't have member count
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

// TODO: remove after 2026-05-01,
// we need to backfill chat_type for users who joined before we started storing it.
func (w *worker) getChatType(endpoint string, chatID int64) string {
	ctx := context.Background()
	chat, err := w.bots[endpoint].GetChat(ctx, &bot.GetChatParams{ChatID: chatID})
	if err != nil {
		linf("cannot get chat type, %v", err)
		return ""
	}
	return string(chat.Type)
}

func (w *worker) refer(followerChatID int64, referrer string, now int, chatType string) (applied appliedKind) {
	referrerChatID := w.db.ChatForReferralID(referrer)
	if referrerChatID == nil {
		return invalidReferral
	}
	if _, exists := w.db.User(followerChatID); exists {
		return followerExists
	}
	w.db.AddUserWithBonus(followerChatID, w.cfg.MaxSubs+w.cfg.FollowerBonus, now, chatType)
	w.db.AddOrUpdateReferrer(*referrerChatID, w.cfg.MaxSubs+w.cfg.ReferralBonus, w.cfg.ReferralBonus)
	w.db.IncrementReferredUsers(*referrerChatID)
	w.db.AddReferralEvent(now, referrerChatID, followerChatID, nil)
	return referralApplied
}

func (w *worker) showReferral(endpoint string, chatID int64) {
	referralID := w.db.ReferralID(chatID)
	if referralID == nil {
		temp := w.newRandReferralID()
		referralID = &temp
		w.db.AddReferral(chatID, *referralID)
	}
	referralLink := fmt.Sprintf("https://t.me/%s?start=%s", w.botNames[endpoint], *referralID)
	subscriptionsNumber := w.db.SubscribedOrPendingCount(endpoint, chatID)
	user := w.mustUser(chatID)
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].ReferralLink, tplData{
		"link":                referralLink,
		"referral_bonus":      w.cfg.ReferralBonus,
		"follower_bonus":      w.cfg.FollowerBonus,
		"subscriptions_used":  subscriptionsNumber,
		"total_subscriptions": user.MaxSubs,
	}, db.ReplyPacket)
}

func (w *worker) start(endpoint string, chatID int64, referrer string, now int, chatType string) {
	nickname := ""
	switch {
	case strings.HasPrefix(referrer, "m-"):
		nickname = referrer[2:]
		nickname = w.nicknamePreprocessing(nickname)
		referrer = ""
	case referrer != "":
		referralID := w.db.ReferralID(chatID)
		if referralID != nil && *referralID == referrer {
			w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].OwnReferralLinkHit, nil, db.ReplyPacket)
			return
		}
	}
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].Start, tplData{
		"website_link": w.cfg.WebsiteLink,
	}, db.ReplyPacket)
	if chatID > 0 && referrer != "" {
		applied := w.refer(chatID, referrer, now, chatType)
		switch applied {
		case referralApplied:
			w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].ReferralApplied, nil, db.ReplyPacket)
		case invalidReferral:
			w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].InvalidReferralLink, nil, db.ReplyPacket)
		case followerExists:
			w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].FollowerExists, nil, db.ReplyPacket)
		}
	}
	w.db.AddUser(chatID, w.cfg.MaxSubs, now, chatType)
	if nickname != "" {
		if streamerID := w.addStreamer(endpoint, chatID, nickname, now, true); streamerID != nil {
			w.db.AddReferralEvent(now, nil, chatID, streamerID)
		}
	}
}

func (w *worker) help(endpoint string, chatID int64) {
	w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].Help, tplData{
		"website_link": w.cfg.WebsiteLink,
	}, db.ReplyPacket)
}

func (w *worker) processIncomingCommand(endpoint string, chatID int64, command, arguments string, now int, chatType string) bool {
	w.db.ResetBlock(endpoint, chatID)
	command = strings.ToLower(command)
	if command != "start" {
		w.db.AddUser(chatID, w.cfg.MaxSubs, now, chatType)
	}
	linf("chat: %d, command: %s %s", chatID, command, arguments)

	if chatID == w.cfg.AdminID {
		if proc, maintenance := w.processAdminMessage(endpoint, chatID, command, arguments); proc {
			return maintenance
		}
	}

	unknown := func() {
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].UnknownCommand, nil, db.ReplyPacket)
	}

	switch command {
	case "add":
		arguments = strings.ReplaceAll(arguments, "—", "--")
		_ = w.addStreamer(endpoint, chatID, arguments, now, false)
	case "remove":
		arguments = strings.ReplaceAll(arguments, "—", "--")
		w.removeStreamer(endpoint, chatID, arguments)
	case "list":
		w.listStreamers(endpoint, chatID, now)
	case "pics", "online":
		w.listOnlineStreamers(endpoint, chatID, now)
	case "start":
		w.start(endpoint, chatID, arguments, now, chatType)
	case "help":
		w.help(endpoint, chatID)
	case "ad":
		w.ad(db.PriorityHigh, endpoint, chatID)
	case "faq":
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].FAQ, tplData{
			"max_subs": w.cfg.MaxSubs,
		}, db.ReplyPacket)
	case "feedback":
		w.feedback(endpoint, chatID, arguments, now)
	case "social":
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].Social, nil, db.ReplyPacket)
	case "version":
		w.sendTr(
			db.PriorityHigh,
			endpoint,
			chatID,
			false,
			w.tr[endpoint].Version,
			tplData{"version": cmdlib.Version},
			db.ReplyPacket)
	case "remove_all", "stop":
		w.sendTr(db.PriorityHigh, endpoint, chatID, false, w.tr[endpoint].RemoveAll, nil, db.ReplyPacket)
	case "sure_remove_all":
		w.sureRemoveAll(endpoint, chatID)
	case "want_more":
		w.wantMore(endpoint, chatID)
	case "settings":
		w.settings(endpoint, chatID)
	case "enable_images":
		w.enableImages(endpoint, chatID, true)
	case "disable_images":
		w.enableImages(endpoint, chatID, false)
	case "enable_offline_notifications":
		w.enableOfflineNotifications(endpoint, chatID, true)
	case "disable_offline_notifications":
		w.enableOfflineNotifications(endpoint, chatID, false)
	case "enable_subject":
		w.enableSubject(endpoint, chatID, true)
	case "disable_subject":
		w.enableSubject(endpoint, chatID, false)
	case "enable_silent_messages":
		w.enableSilentMessages(endpoint, chatID, true)
	case "disable_silent_messages":
		w.enableSilentMessages(endpoint, chatID, false)
	case "referral":
		w.showReferral(endpoint, chatID)
	case "week":
		if !w.cfg.EnableWeek {
			unknown()
			return false
		}
		w.showWeek(endpoint, chatID, arguments)
	default:
		unknown()
	}
	return false
}

func (w *worker) periodic() {
	w.pushOnlineRequest()
}

func (w *worker) pushOnlineRequest() {
	var request cmdlib.StatusRequest
	if w.checker.UsesFixedList() {
		request = &cmdlib.FixedListOnlineRequest{
			ResultsCh: w.checkerResults,
			Streamers: w.db.SubscribedStreamers(),
		}
	} else {
		request = &cmdlib.OnlineListRequest{
			ResultsCh: w.checkerResults,
		}
	}
	if err := w.checker.PushStatusRequest(request); err != nil {
		lerr("%v", err)
	}
}

func (w *worker) pushExistenceRequest(streamers map[string]bool) error {
	err := w.checker.PushStatusRequest(&cmdlib.FixedListStatusRequest{
		ResultsCh: w.existenceListResults,
		Streamers: streamers,
	})
	if err != nil {
		lerr("%v", err)
	}
	return err
}

type processingResult struct {
	changesCount          int
	confirmedChangesCount int
	notifications         []db.Notification
	elapsed               time.Duration
	upsertTimings         db.UpsertUnconfirmedTimings
	confirmChangesMs      int
	storeNotificationsMs  int
}

func (w *worker) handleCheckerResults(result cmdlib.CheckerResults, now int) processingResult {
	start := time.Now()
	if result.Failed() {
		return processingResult{}
	}

	var updates []db.StatusChange

	switch r := result.(type) {
	case *cmdlib.OnlineListResults:
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

	return processingResult{
		changesCount:          len(updates),
		confirmedChangesCount: len(confirmedStatusChanges),
		notifications:         notifications,
		elapsed:               time.Since(start),
		storeNotificationsMs:  storeNotificationsMs,
		upsertTimings:         upsertTimings,
		confirmChangesMs:      confirmChangesMs,
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
					ChatID:     user.ChatID,
					StreamerID: &c.StreamerID,
					Nickname:   c.Nickname,
					Status:     c.Status,
					Social:     user.ChatID > 0,
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

func getCommandAndArgs(update *models.Update, mention string, ourIDs []int64) (int64, string, string) {
	var text string
	var chatID int64
	var forceMention bool
	if update.Message != nil {
		text = update.Message.Text
		chatID = update.Message.Chat.ID
		for _, m := range update.Message.NewChatMembers {
			for _, ourID := range ourIDs {
				if m.ID == ourID {
					return chatID, "start", ""
				}
			}
		}
	} else if update.ChannelPost != nil {
		text = update.ChannelPost.Text
		chatID = update.ChannelPost.Chat.ID
		forceMention = true
	}
	text = strings.TrimLeft(text, " /")
	if text == "" {
		return 0, "", ""
	}
	parts := strings.SplitN(text, " ", 2)
	if strings.HasSuffix(parts[0], mention) {
		parts[0] = parts[0][:len(parts[0])-len(mention)]
	} else if forceMention {
		return 0, "", ""
	}
	for len(parts) < 2 {
		return chatID, parts[0], ""
	}
	return chatID, parts[0], strings.TrimSpace(parts[1])
}

var loggedCommands = map[string]bool{
	"ad":                            true,
	"add":                           true,
	"disable_images":                true,
	"disable_offline_notifications": true,
	"disable_subject":               true,
	"enable_images":                 true,
	"enable_offline_notifications":  true,
	"enable_subject":                true,
	"faq":                           true,
	"feedback":                      true,
	"help":                          true,
	"list":                          true,
	"online":                        true,
	"pics":                          true,
	"referral":                      true,
	"remove":                        true,
	"remove_all":                    true,
	"settings":                      true,
	"social":                        true,
	"start":                         true,
	"stop":                          true,
	"sure_remove_all":               true,
	"version":                       true,
	"want_more":                     true,
	"week":                          true,
}

func (w *worker) processTGUpdate(p incomingPacket) bool {
	now := int(time.Now().Unix())
	u := p.message
	mention := "@" + w.botNames[p.endpoint]
	chatID, command, args := getCommandAndArgs(u, mention, w.ourIDs)
	if !w.cfg.ChatWhitelisted(chatID) {
		linf("message from chat %d ignored, not in whitelist", chatID)
		return false
	}
	if command != "" {
		var loggedCommand *string
		if loggedCommands[command] {
			loggedCommand = &command
		}
		w.db.LogReceivedMessage(now, p.endpoint, chatID, loggedCommand)
		var chatType string
		if u.Message != nil {
			chatType = string(u.Message.Chat.Type)
		} else if u.ChannelPost != nil {
			chatType = string(u.ChannelPost.Chat.Type)
		}
		result := w.processIncomingCommand(p.endpoint, chatID, command, args, now, chatType)
		if memberCount := w.getChatMemberCount(p.endpoint, chatID); memberCount != nil {
			w.db.UpdateMemberCount(chatID, *memberCount)
		}
		// TODO: remove after 2026-05-01,
		// we need to backfill chat_type for users who joined before we started storing it.
		if now < 1777593600 {
			if user, exists := w.db.User(chatID); exists && user.ChatType == nil {
				w.db.UpdateChatType(chatID, chatType)
			}
		}
		return result
	}
	return false
}

func (w *worker) incoming() chan incomingPacket {
	ctx := context.Background()
	for n, p := range w.cfg.Endpoints {
		linf("listening for a webhook for endpoint %s", n)
		http.Handle(string(p.ListenPath), w.bots[n].WebhookHandler())
		go w.bots[n].StartWebhook(ctx)
	}
	return w.incomingPackets
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

func (w *worker) adminSQL(query string) time.Duration {
	start := time.Now()
	var result string
	if w.db.MaybeRecord(query, nil, db.ScanTo{&result}) {
		w.sendText(db.PriorityHigh, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, result, db.ReplyPacket)
	}
	return time.Since(start)
}

func (w *worker) maintenanceStartupReply(incoming chan incomingPacket, done chan bool) {
	waitingUsers := map[waitingUser]bool{}
	for {
		select {
		case u := <-incoming:
			mention := "@" + w.botNames[u.endpoint]
			chatID, command, args := getCommandAndArgs(u.message, mention, w.ourIDs)
			if command != "" {
				waitingUsers[waitingUser{chatID: chatID, endpoint: u.endpoint}] = true
				w.sendText(db.PriorityHigh, u.endpoint, chatID, false, true, cmdlib.ParseRaw, w.cfg.Endpoints[u.endpoint].MaintenanceResponse, db.ReplyPacket)
				linf("ignoring command %s %s", command, args)
			}
		case <-done:
			for user := range waitingUsers {
				w.sendTr(db.PriorityHigh, user.endpoint, user.chatID, false, w.tr[user.endpoint].WeAreUp, nil, db.MessagePacket)
			}
			return
		case <-w.outgoingMsgResults:
		}
	}
}

func (w *worker) sendReadyNotifications() { w.sendingNotifications <- w.db.NewNotifications() }

func (w *worker) sendNotificationsDaemon() {
	for nots := range w.sendingNotifications {
		w.notifyOfStatuses(nots)
		w.sentNotifications <- nots
	}
}

func (w *worker) fuzzySearchDaemon() {
	for req := range w.searchRequests {
		if req.userID != 0 {
			now := int(time.Now().Unix())
			command := "search"
			w.fuzzySearchDB.LogReceivedMessage(
				now, req.endpoint,
				req.userID, &command)
		}
		req.resultCh <- w.fuzzySearchDB.SearchStreamers(req.term)
	}
}

func (w *worker) queryUnconfirmedSubs() {
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
	confirmationsInWork := map[string][]db.PendingSubscription{}
	var iter db.PendingSubscription
	w.db.MustQuery(
		"select endpoint, nickname, chat_id, referral from pending_subscriptions where checking",
		nil,
		db.ScanTo{&iter.Endpoint, &iter.Nickname, &iter.ChatID, &iter.Referral},
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
						w.db.AddReferralEvent(now, nil, sub.ChatID, streamerID)
					}
				} else {
					w.db.DenySub(sub)
				}
				n := db.Notification{
					Endpoint:   sub.Endpoint,
					ChatID:     sub.ChatID,
					StreamerID: streamerID,
					Nickname:   nickname,
					Status:     info.Status,
					Social:     false,
					Priority:   db.PriorityHigh,
					Kind:       db.ReplyPacket,
				}
				nots = append(nots, n)
				if confirmed {
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

func (w *worker) maintenance(signals chan os.Signal, incoming chan incomingPacket) bool {
	processingDone := make(chan time.Duration)
	processing := false
	users := map[waitingUser]bool{}
	for {
		select {
		case n := <-signals:
			linf("got signal %v", n)
			if n == syscall.SIGINT || n == syscall.SIGTERM || n == syscall.SIGABRT {
				w.removeWebhook()
				return false
			}
			if n == syscall.SIGCONT {
				return true
			}
		case u := <-incoming:
			mention := "@" + w.botNames[u.endpoint]
			chatID, command, args := getCommandAndArgs(u.message, mention, w.ourIDs)
			if chatID == w.cfg.AdminID {
				switch command {
				case "continue":
					if processing {
						w.sendText(db.PriorityHigh, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "still processing", db.ReplyPacket)
					} else {
						w.sendText(db.PriorityHigh, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
						for user := range users {
							w.sendTr(db.PriorityHigh, user.endpoint, chatID, false, w.tr[user.endpoint].WeAreUp, nil, db.MessagePacket)
						}
						return true
					}
				case "sql":
					if processing {
						w.sendText(db.PriorityHigh, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "still processing", db.ReplyPacket)
					} else {
						processing = true
						w.sendText(db.PriorityHigh, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
						go func() {
							processingDone <- w.adminSQL(args)
						}()
					}
				case "":
				default:
					w.sendText(db.PriorityHigh, u.endpoint, chatID, false, true, cmdlib.ParseRaw, w.cfg.Endpoints[u.endpoint].MaintenanceResponse, db.ReplyPacket)
				}
			} else {
				if command != "" {
					users[waitingUser{chatID: chatID, endpoint: u.endpoint}] = true
					w.sendText(db.PriorityHigh, u.endpoint, chatID, false, true, cmdlib.ParseRaw, w.cfg.Endpoints[u.endpoint].MaintenanceResponse, db.ReplyPacket)
					linf("ignoring command %s %s", command, args)
				}
			}
		case elapsed := <-processingDone:
			processing = false
			text := fmt.Sprintf("processing done in %v", elapsed)
			w.sendText(db.PriorityHigh, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, text, db.MessagePacket)
		case <-w.outgoingMsgResults:
		}
	}
}

func main() {
	version := pflag.BoolP("version", "v", false, "prints current version")
	printCfg := pflag.BoolP("print-config", "p", false, "print config and exit")
	pflag.Parse()
	if *version {
		fmt.Println(cmdlib.Version)
		os.Exit(0)
	}

	cfg := botconfig.ReadConfig()
	if *printCfg {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		checkErr(enc.Encode(cfg))
		os.Exit(0)
	}

	w := newWorker(cfg)
	w.logConfig()
	w.setWebhook()
	w.setCommands()
	w.setDefaultAdminRights()
	w.initBotNames()
	databaseDone := make(chan bool)
	w.serveEndpoints()
	incoming := w.incoming()
	go w.sender()
	go w.maintenanceStartupReply(incoming, databaseDone)
	go w.sendNotificationsDaemon()
	go w.fuzzySearchDaemon()
	w.sendText(db.PriorityHigh, w.cfg.AdminEndpoint, w.cfg.AdminID, true, true, cmdlib.ParseRaw, "bot started", db.MessagePacket)
	w.createDatabase(databaseDone)
	w.registerWebApp()
	w.initCache()
	w.db.ResetNotificationSending()
	w.db.ResetCheckingToUnconfirmed()

	var requestTimer = time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second)
	var maintainDBTimerChannel <-chan time.Time
	if w.cfg.MaintainDBPeriodSeconds != 0 {
		maintainDBTimerChannel = time.NewTicker(time.Duration(w.cfg.MaintainDBPeriodSeconds) * time.Second).C
	}
	var subsConfirmTimer = time.NewTicker(time.Duration(w.cfg.SubsConfirmationPeriodSeconds) * time.Second)
	var notificationSenderTimer = time.NewTicker(time.Duration(w.cfg.NotificationsReadyPeriodSeconds) * time.Second)

	w.checker.Init(cmdlib.CheckerConfig{
		UsersOnlineEndpoints: w.cfg.UsersOnlineEndpoint,
		Clients:              w.clients,
		Headers:              w.cfg.Headers,
		Dbg:                  w.cfg.Debug,
		SpecificConfig:       w.cfg.SpecificConfig,
		QueueSize:            5,
		IntervalMs:           w.cfg.IntervalMs,
	})
	cmdlib.StartCheckerDaemon(w.checker)
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT, syscall.SIGTSTP, syscall.SIGCONT)
	w.sendText(db.PriorityHigh, w.cfg.AdminEndpoint, w.cfg.AdminID, true, true, cmdlib.ParseRaw, "bot is up", db.MessagePacket)
	w.pushOnlineRequest()
	for {
		select {
		case <-requestTimer.C:
			runtime.GC()
			w.periodic()
		case <-maintainDBTimerChannel:
			w.maintainDB()
		case <-subsConfirmTimer.C:
			w.queryUnconfirmedSubs()
		case <-notificationSenderTimer.C:
			w.sendReadyNotifications()
		case result := <-w.checkerResults:
			now := int(time.Now().Unix())
			cr := w.handleCheckerResults(result, now)
			w.db.LogPerformance(now, db.PerformanceLogUpdateQuery, int(result.Duration().Milliseconds()), map[string]any{
				"failed": result.Failed(),
				"count":  result.Count(),
			})
			w.db.LogPerformance(now, db.PerformanceLogUpdateProcessing, int(cr.elapsed.Milliseconds()), map[string]any{
				"unconfirmed_count":                    cr.changesCount,
				"confirmed_count":                      cr.confirmedChangesCount,
				"notifications_count":                  len(cr.notifications),
				"upsert_unconfirmed_streamers_ms":      cr.upsertTimings.UpsertStreamersMs,
				"insert_nicknames_ms":                  cr.upsertTimings.InsertNicknamesMs,
				"insert_unconfirmed_status_changes_ms": cr.upsertTimings.InsertStatusChangesMs,
				"commit_unconfirmed_ms":                cr.upsertTimings.CommitMs,
				"summarize_brin_ms":                    cr.upsertTimings.SummarizeBrinMs,
				"confirm_changes_ms":                   cr.confirmChangesMs,
				"store_notifications_ms":               cr.storeNotificationsMs,
			})
			if w.cfg.Debug {
				ldbg("status updates processed in %v", cr.elapsed)
			}
		case req := <-w.addRequests:
			now := int(time.Now().Unix())
			w.addStreamer(req.endpoint, req.chatID, req.nickname, now, false)
			req.doneCh <- struct{}{}
		case u := <-incoming:
			if w.processTGUpdate(u) {
				if !w.maintenance(signals, incoming) {
					return
				}
			}
		case s := <-signals:
			linf("got signal %v", s)
			if s == syscall.SIGINT || s == syscall.SIGTERM || s == syscall.SIGABRT {
				w.removeWebhook()
				return
			}
			if s == syscall.SIGTSTP {
				if !w.maintenance(signals, incoming) {
					return
				}
			}
		case r := <-w.outgoingMsgResults:
			switch r.result {
			case messageBlocked:
				w.db.IncrementBlock(r.endpoint, r.chatID)
			case messageSent:
				w.db.ResetBlock(r.endpoint, r.chatID)
				// TODO: remove after 2026-05-01,
				// we need to backfill chat_type for users who joined before we started storing it.
				if r.timestamp < 1777593600 {
					if user, exists := w.db.User(r.chatID); exists && user.ChatType == nil {
						if chatType := w.getChatType(r.endpoint, r.chatID); chatType != "" {
							w.db.UpdateChatType(r.chatID, chatType)
						}
					}
				}
				if memberCount := w.getChatMemberCount(r.endpoint, r.chatID); memberCount != nil {
					w.db.UpdateMemberCount(r.chatID, *memberCount)
				}
			}
			w.db.LogSentMessage(r.timestamp, r.chatID, r.result, r.endpoint, r.priority, r.latency, r.kind)
		case r := <-w.existenceListResults:
			now := int(time.Now().Unix())
			w.db.LogPerformance(now, db.PerformanceLogExistenceQuery, int(r.Duration().Milliseconds()), map[string]any{
				"failed": r.Failed(),
				"count":  r.Count(),
			})
			w.processSubsConfirmations(r)
		case nots := <-w.sentNotifications:
			for _, n := range nots {
				w.db.DeleteNotification(n.ID)
				w.db.IncrementReports(n.ChatID)
			}
		case r := <-w.imageDownloadLogs:
			now := int(time.Now().Unix())
			w.db.LogPerformance(now, db.PerformanceLogImageDownload, r.durationMs, map[string]any{
				"failed": !r.success,
			})
		}
	}
}
