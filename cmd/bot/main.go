package main

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/bcmk/go-smtpd/smtpd"
	"github.com/bcmk/siren/lib"
	"github.com/bcmk/siren/payments"
	tg "github.com/bcmk/telegram-bot-api"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

var (
	checkErr = lib.CheckErr
	lerr     = lib.Lerr
	linf     = lib.Linf
	ldbg     = lib.Ldbg
)

type tplData = map[string]interface{}

type timeDiff struct {
	Days        int
	Hours       int
	Minutes     int
	Seconds     int
	Nanoseconds int
}

type notification struct {
	id       int
	endpoint string
	chatID   int64
	modelID  string
	status   lib.StatusKind
	timeDiff *int
	imageURL string
	social   bool
	sound    bool
	priority int
	kind     packetKind
}

type model struct {
	modelID string
	status  lib.StatusKind
}

type statusChange struct {
	modelID   string
	status    lib.StatusKind
	timestamp int
}

type statRequest struct {
	endpoint string
	writer   http.ResponseWriter
	request  *http.Request
	done     chan bool
}

type ipnRequest struct {
	writer  http.ResponseWriter
	request *http.Request
	done    chan bool
}

type queryDurationsData struct {
	avg   float64
	count int
}

type user struct {
	chatID               int64
	maxModels            int
	reports              int
	blacklist            bool
	showImages           bool
	offlineNotifications bool
}

type subscription struct {
	chatID   int64
	modelID  string
	endpoint string
}

type worker struct {
	clients                  []*lib.Client
	bots                     map[string]*tg.BotAPI
	db                       *sql.DB
	cfg                      *config
	httpQueriesDuration      time.Duration
	updatesDuration          time.Duration
	cleaningDuration         time.Duration
	changesInPeriod          int
	confirmedChangesInPeriod int
	ourOnline                map[string]bool
	specialModels            map[string]bool
	siteStatuses             map[string]statusChange
	siteOnline               map[string]bool
	tr                       map[string]*lib.Translations
	tpl                      map[string]*template.Template
	trAds                    map[string]map[string]*lib.Translation
	tplAds                   map[string]*template.Template
	modelIDPreprocessing     func(string) string
	checker                  lib.Checker
	unsuccessfulRequests     []bool
	unsuccessfulRequestsPos  int
	downloadResults          chan bool
	downloadErrors           []bool
	downloadResultsPos       int
	nextErrorReport          time.Time
	coinPaymentsAPI          *payments.CoinPaymentsAPI
	mailTLS                  *tls.Config
	durations                map[string]queryDurationsData
	images                   map[string]string
	botNames                 map[string]string
	lowPriorityMsg           chan outgoingPacket
	highPriorityMsg          chan outgoingPacket
	outgoingMsgResults       chan msgSendResult
	unconfirmedSubsResults   chan lib.StatusResults
	onlineModelsChan         chan lib.StatusUpdateResults
	sendingNotifications     chan []notification
	sentNotifications        chan []notification
	mainGID                  int
	ourIDs                   []int64
}

type incomingPacket struct {
	message  tg.Update
	endpoint string
}

type outgoingPacket struct {
	message   baseChattable
	endpoint  string
	requested time.Time
	kind      packetKind
}

type packetKind int

const (
	notificationPacket packetKind = 0
	replyPacket                   = 1
	adPacket                      = 2
	messagePacket                 = 3
)

type email struct {
	chatID   int64
	endpoint string
	email    string
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
)

type msgSendResult struct {
	priority  int
	timestamp int
	result    int
	endpoint  string
	chatID    int64
	delay     int
	kind      packetKind
}

type waitingUser struct {
	chatID   int64
	endpoint string
}

func newWorker() *worker {
	if len(os.Args) != 2 {
		panic("usage: siren <config>")
	}
	cfg := readConfig(os.Args[1])

	var err error
	var mailTLS *tls.Config

	if cfg.Mail != nil && cfg.Mail.Certificate != "" {
		mailTLS, err = loadTLS(cfg.Mail.Certificate, cfg.Mail.CertificateKey)
		checkErr(err)
	}

	var clients []*lib.Client
	for _, address := range cfg.SourceIPAddresses {
		clients = append(clients, lib.HTTPClientWithTimeoutAndAddress(cfg.TimeoutSeconds, address, cfg.EnableCookies))
	}

	telegramClient := lib.HTTPClientWithTimeoutAndAddress(cfg.TelegramTimeoutSeconds, "", false)
	bots := make(map[string]*tg.BotAPI)
	for n, p := range cfg.Endpoints {
		//noinspection GoNilness
		var bot *tg.BotAPI
		bot, err = tg.NewBotAPIWithClient(p.BotToken, tg.APIEndpoint, telegramClient.Client)
		checkErr(err)
		bots[n] = bot
	}
	db, err := sql.Open("sqlite3", cfg.DBPath)
	checkErr(err)
	tr, tpl := lib.LoadAllTranslations(trsByEndpoint(cfg))
	trAds, tplAds := lib.LoadAllAds(trsAdsByEndpoint(cfg))
	for _, t := range tpl {
		template.Must(t.New("affiliate_link").Parse(cfg.AffiliateLink))
	}
	w := &worker{
		bots:                   bots,
		db:                     db,
		cfg:                    cfg,
		clients:                clients,
		tr:                     tr,
		tpl:                    tpl,
		trAds:                  trAds,
		tplAds:                 tplAds,
		unsuccessfulRequests:   make([]bool, cfg.errorDenominator),
		downloadErrors:         make([]bool, cfg.errorDenominator),
		downloadResults:        make(chan bool),
		mailTLS:                mailTLS,
		durations:              map[string]queryDurationsData{},
		images:                 map[string]string{},
		botNames:               map[string]string{},
		lowPriorityMsg:         make(chan outgoingPacket, 10000),
		highPriorityMsg:        make(chan outgoingPacket, 10000),
		outgoingMsgResults:     make(chan msgSendResult),
		unconfirmedSubsResults: make(chan lib.StatusResults),
		onlineModelsChan:       make(chan lib.StatusUpdateResults),
		sendingNotifications:   make(chan []notification, 1000),
		sentNotifications:      make(chan []notification),
		mainGID:                gid(),
		ourIDs:                 cfg.getOurIDs(),
	}

	if cp := cfg.CoinPayments; cp != nil {
		w.coinPaymentsAPI = payments.NewCoinPaymentsAPI(cp.PublicKey, cp.PrivateKey, "https://"+cp.IPNListenURL, cfg.TimeoutSeconds, cfg.Debug)
	}

	switch cfg.Website {
	case "test":
		w.checker = &lib.RandomChecker{}
		w.modelIDPreprocessing = lib.CanonicalModelID
	case "bongacams":
		w.checker = &lib.BongaCamsChecker{}
		w.modelIDPreprocessing = lib.CanonicalModelID
	case "chaturbate":
		w.checker = &lib.ChaturbateChecker{}
		w.modelIDPreprocessing = lib.ChaturbateCanonicalModelID
	case "stripchat":
		w.checker = &lib.StripchatChecker{}
		w.modelIDPreprocessing = lib.CanonicalModelID
	case "livejasmin":
		w.checker = &lib.LiveJasminChecker{}
		w.modelIDPreprocessing = lib.CanonicalModelID
	case "camsoda":
		w.checker = &lib.CamSodaChecker{}
		w.modelIDPreprocessing = lib.CanonicalModelID
	case "flirt4free":
		w.checker = &lib.Flirt4FreeChecker{}
		w.modelIDPreprocessing = lib.Flirt4FreeCanonicalModelID
	case "streamate":
		w.checker = &lib.StreamateChecker{}
		w.modelIDPreprocessing = lib.CanonicalModelID
	case "twitch":
		w.checker = &lib.TwitchChecker{}
		w.modelIDPreprocessing = lib.CanonicalModelID
	default:
		panic("wrong website")
	}

	return w
}

func trsByEndpoint(cfg *config) map[string][]string {
	result := make(map[string][]string)
	for k, v := range cfg.Endpoints {
		result[k] = v.Translation
	}
	return result
}

func trsAdsByEndpoint(cfg *config) map[string][]string {
	result := map[string][]string{}
	for k, v := range cfg.Endpoints {
		result[k] = v.Ads
	}
	return result
}

func (w *worker) setWebhook() {
	for n, p := range w.cfg.Endpoints {
		linf("setting webhook for endpoint %s...", n)
		if p.WebhookDomain == "" {
			continue
		}
		if p.CertificatePath == "" {
			var _, err = w.bots[n].SetWebhook(tg.NewWebhook(path.Join(p.WebhookDomain, p.ListenPath)))
			checkErr(err)
		} else {
			var _, err = w.bots[n].SetWebhook(tg.NewWebhookWithCert(path.Join(p.WebhookDomain, p.ListenPath), p.CertificatePath))
			checkErr(err)
		}
		info, err := w.bots[n].GetWebhookInfo()
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
	for n := range w.cfg.Endpoints {
		linf("removing webhook for endpoint %s...", n)
		_, err := w.bots[n].RemoveWebhook()
		checkErr(err)
		linf("OK")
	}
}

func (w *worker) initBotNames() {
	for n := range w.cfg.Endpoints {
		user, err := w.bots[n].GetMe()
		checkErr(err)
		linf("bot name for endpoint %s: %s", n, user.UserName)
		w.botNames[n] = user.UserName
	}
}

func (w *worker) setCommands() {
	for n := range w.cfg.Endpoints {
		text := templateToString(w.tpl[n], w.tr[n].RawCommands.Key, nil)
		lines := strings.Split(text, "\n")
		var commands []tg.BotCommand
		for _, l := range lines {
			pair := strings.SplitN(l, "-", 2)
			if len(pair) != 2 {
				checkErr(fmt.Errorf("unexpected command pair %q", l))
			}
			pair[0], pair[1] = strings.TrimSpace(pair[0]), strings.TrimSpace(pair[1])
			commands = append(commands, tg.BotCommand{Command: pair[0], Description: pair[1]})
			if w.cfg.Debug {
				ldbg("command %s - %s", pair[0], pair[1])
			}
		}
		linf("setting commands for endpoint %s...", n)
		err := w.bots[n].SetMyCommands(commands)
		checkErr(err)
		linf("OK")
	}
}

func (w *worker) sendText(
	queue chan outgoingPacket,
	endpoint string,
	chatID int64,
	notify bool,
	disablePreview bool,
	parse lib.ParseKind,
	text string,
	kind packetKind,
) {
	msg := tg.NewMessage(chatID, text)
	msg.DisableNotification = !notify
	msg.DisableWebPagePreview = disablePreview
	switch parse {
	case lib.ParseHTML, lib.ParseMarkdown:
		msg.ParseMode = parse.String()
	}
	w.enqueueMessage(queue, endpoint, &messageConfig{msg}, kind)
}

func (w *worker) sendImage(
	queue chan outgoingPacket,
	endpoint string,
	chatID int64,
	notify bool,
	parse lib.ParseKind,
	text string,
	image []byte,
	kind packetKind,
) {
	fileBytes := tg.FileBytes{Name: "preview", Bytes: image}
	msg := tg.NewPhotoUpload(chatID, fileBytes)
	msg.Caption = text
	msg.DisableNotification = !notify
	switch parse {
	case lib.ParseHTML, lib.ParseMarkdown:
		msg.ParseMode = parse.String()
	}
	w.enqueueMessage(queue, endpoint, &photoConfig{msg}, kind)
}

func (w *worker) enqueueMessage(queue chan outgoingPacket, endpoint string, msg baseChattable, kind packetKind) {
	select {
	case queue <- outgoingPacket{endpoint: endpoint, message: msg, requested: time.Now(), kind: kind}:
	default:
		lerr("the outgoing message queue is full")
	}
}

func (w *worker) sender(queue chan outgoingPacket, priority int) {
	for packet := range queue {
		now := int(time.Now().Unix())
		delay := 0
	resend:
		for {
			result := w.sendMessageInternal(packet.endpoint, packet.message)
			delay = int(time.Since(packet.requested).Milliseconds())
			w.outgoingMsgResults <- msgSendResult{
				priority:  priority,
				timestamp: now,
				result:    result,
				endpoint:  packet.endpoint,
				chatID:    packet.message.baseChat().ChatID,
				delay:     delay,
				kind:      packet.kind,
			}
			switch result {
			case messageTimeout:
				time.Sleep(1000 * time.Millisecond)
				continue resend
			case messageUnknownNetworkError:
				time.Sleep(1000 * time.Millisecond)
				continue resend
			case messageTooManyRequests:
				time.Sleep(8000 * time.Millisecond)
				continue resend
			default:
				time.Sleep(60 * time.Millisecond)
				break resend
			}
		}
	}
}

func (w *worker) sendMessageInternal(endpoint string, msg baseChattable) int {
	chatID := msg.baseChat().ChatID
	if _, err := w.bots[endpoint].Send(msg); err != nil {
		switch err := err.(type) {
		case tg.Error:
			switch err.Code {
			case messageBlocked:
				if w.cfg.Debug {
					ldbg("cannot send a message, bot blocked")
				}
				return messageBlocked
			case messageTooManyRequests:
				if w.cfg.Debug {
					ldbg("cannot send a message, too many requests")
				}
				return messageTooManyRequests
			case messageBadRequest:
				if err.ResponseParameters.MigrateToChatID != 0 {
					if w.cfg.Debug {
						ldbg("cannot send a message, group migration")
					}
					return messageMigrate
				}
				if err.Message == "Bad Request: chat not found" {
					if w.cfg.Debug {
						ldbg("cannot send a message, chat not found")
					}
					return messageChatNotFound
				}
				lerr("cannot send a message, bad request, code: %d, error: %v", err.Code, err)
				return err.Code
			default:
				lerr("cannot send a message, unknown code: %d, error: %v", err.Code, err)
				return err.Code
			}
		case net.Error:
			if err.Timeout() {
				if w.cfg.Debug {
					ldbg("cannot send a message, timeout")
				}
				return messageTimeout
			}
			lerr("cannot send a message, unknown network error")
			return messageUnknownNetworkError
		default:
			lerr("unexpected error type while sending a message to %d, %v", chatID, err)
			return messageUnknownError
		}
	}
	return messageSent
}

func templateToString(t *template.Template, key string, data map[string]interface{}) string {
	buf := &bytes.Buffer{}
	err := t.ExecuteTemplate(buf, key, data)
	checkErr(err)
	return buf.String()
}

func (w *worker) sendTr(
	queue chan outgoingPacket,
	endpoint string,
	chatID int64,
	notify bool,
	translation *lib.Translation,
	data map[string]interface{},
	kind packetKind,
) {
	tpl := w.tpl[endpoint]
	text := templateToString(tpl, translation.Key, data)
	w.sendText(queue, endpoint, chatID, notify, translation.DisablePreview, translation.Parse, text, kind)
}

func (w *worker) sendAdsTr(
	queue chan outgoingPacket,
	endpoint string,
	chatID int64,
	notify bool,
	translation *lib.Translation,
	data map[string]interface{},
) {
	tpl := w.tplAds[endpoint]
	text := templateToString(tpl, translation.Key, data)
	w.sendText(queue, endpoint, chatID, notify, translation.DisablePreview, translation.Parse, text, adPacket)
}

func (w *worker) sendTrImage(
	queue chan outgoingPacket,
	endpoint string,
	chatID int64,
	notify bool,
	translation *lib.Translation,
	data map[string]interface{},
	image []byte,
	kind packetKind,
) {
	tpl := w.tpl[endpoint]
	text := templateToString(tpl, translation.Key, data)
	w.sendImage(queue, endpoint, chatID, notify, translation.Parse, text, image, kind)
}

func (w *worker) createDatabase(done chan bool) {
	linf("creating database if needed...")
	for _, prelude := range w.cfg.SQLPrelude {
		w.mustExec(prelude)
	}
	w.mustExec(`create table if not exists schema_version (version integer);`)
	w.applyMigrations()
	done <- true
}

func (w *worker) initCache() {
	start := time.Now()
	w.siteStatuses = w.queryLastStatusChanges()
	w.siteOnline = w.getLastOnlineModels()
	w.ourOnline, w.specialModels = w.queryConfirmedModels()
	elapsed := time.Since(start)
	linf("cache initialized in %d ms", elapsed.Milliseconds())
}

func (w *worker) getLastOnlineModels() map[string]bool {
	res := map[string]bool{}
	for k, v := range w.siteStatuses {
		if v.status == lib.StatusOnline {
			res[k] = true
		}
	}
	return res
}

func (w *worker) confirmationSeconds(status lib.StatusKind) int {
	switch status {
	case lib.StatusOnline:
		return w.cfg.StatusConfirmationSeconds.Online
	case lib.StatusOffline:
		return w.cfg.StatusConfirmationSeconds.Offline
	case lib.StatusDenied:
		return w.cfg.StatusConfirmationSeconds.Denied
	case lib.StatusNotFound:
		return w.cfg.StatusConfirmationSeconds.NotFound
	default:
		return 0
	}
}

func (w *worker) updateStatus(insertStatusChangeStmt, updateLastStatusChangeStmt *sql.Stmt, next statusChange) {
	prev := w.siteStatuses[next.modelID]
	if next.status != prev.status {
		w.mustExecPrepared(insertStatusChange, insertStatusChangeStmt, next.modelID, next.status, next.timestamp)
		w.mustExecPrepared(updateLastStatusChange, updateLastStatusChangeStmt, next.modelID, next.status, next.timestamp)
		w.siteStatuses[next.modelID] = next
		if next.status == lib.StatusOnline {
			w.siteOnline[next.modelID] = true
		} else {
			delete(w.siteOnline, next.modelID)
		}
	}
}

func (w *worker) confirmStatus(updateModelStatusStmt *sql.Stmt, now int) []string {
	all := lib.HashDiffAll(w.ourOnline, w.siteOnline)
	var confirmations []string
	for _, modelID := range all {
		statusChange := w.siteStatuses[modelID]
		confirmationSeconds := w.confirmationSeconds(statusChange.status)
		durationConfirmed := confirmationSeconds == 0 || statusChange.timestamp == 0 || (now-statusChange.timestamp >= confirmationSeconds)
		if durationConfirmed {
			if statusChange.status == lib.StatusOnline {
				w.ourOnline[modelID] = true
			} else {
				delete(w.ourOnline, modelID)
			}
			w.mustExecPrepared(updateModelStatus, updateModelStatusStmt, modelID, statusChange.status)
			confirmations = append(confirmations, modelID)
		}
	}
	return confirmations
}

func (w *worker) notifyOfAddResults(queue chan outgoingPacket, notifications []notification, social bool) {
	for _, n := range notifications {
		data := tplData{"model": n.modelID}
		if n.status&(lib.StatusOnline|lib.StatusOffline|lib.StatusDenied) != 0 {
			w.sendTr(queue, n.endpoint, n.chatID, false, w.tr[n.endpoint].ModelAdded, data, replyPacket)
		} else {
			w.sendTr(queue, n.endpoint, n.chatID, false, w.tr[n.endpoint].AddError, data, replyPacket)
		}
	}
}

func (w *worker) downloadImages(notifications []notification) map[string][]byte {
	images := map[string][]byte{}
	for _, n := range notifications {
		if n.imageURL != "" {
			images[n.imageURL] = nil
		}
	}
	for url := range images {
		images[url] = w.downloadImage(url)
	}
	return images
}

func (w *worker) notifyOfStatuses(highPriorityQueue chan outgoingPacket, lowPriorityQueue chan outgoingPacket, notifications []notification) {
	images := w.downloadImages(notifications)
	for _, n := range notifications {
		queue := lowPriorityQueue
		if n.priority > 0 {
			queue = highPriorityQueue
		}
		w.notifyOfStatus(queue, n, images[n.imageURL], n.social)
	}
}

func (w *worker) trAdsSlice(endpoint string) []*lib.Translation {
	var res []*lib.Translation
	for _, v := range w.trAds[endpoint] {
		res = append(res, v)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Key < res[j].Key })
	return res
}

func (w *worker) ad(queue chan outgoingPacket, endpoint string, chatID int64) {
	trAds := w.trAdsSlice(endpoint)
	if len(trAds) == 0 {
		return
	}
	adNum := rand.Intn(len(trAds))
	w.sendAdsTr(queue, endpoint, chatID, false, trAds[adNum], nil)
}

func (w *worker) notifyOfStatus(queue chan outgoingPacket, n notification, image []byte, social bool) {
	if w.cfg.Debug {
		ldbg("notifying of status of the model %s", n.modelID)
	}
	var timeDiff *timeDiff
	if n.timeDiff != nil {
		temp := calcTimeDiff(*n.timeDiff)
		timeDiff = &temp
	}
	data := tplData{"model": n.modelID, "time_diff": timeDiff}
	switch n.status {
	case lib.StatusOnline:
		if image == nil {
			w.sendTr(queue, n.endpoint, n.chatID, true, w.tr[n.endpoint].Online, data, n.kind)
		} else {
			w.sendTrImage(queue, n.endpoint, n.chatID, true, w.tr[n.endpoint].Online, data, image, n.kind)
		}
	case lib.StatusOffline:
		w.sendTr(queue, n.endpoint, n.chatID, false, w.tr[n.endpoint].Offline, data, n.kind)
	case lib.StatusDenied:
		w.sendTr(queue, n.endpoint, n.chatID, false, w.tr[n.endpoint].Denied, data, n.kind)
	}
	if social && rand.Intn(10) == 0 {
		w.ad(queue, n.endpoint, n.chatID)
	}
}

func (w *worker) mustUser(chatID int64) (user user) {
	user, found := w.user(chatID)
	if !found {
		checkErr(fmt.Errorf("user not found: %d", chatID))
	}
	return
}

func (w *worker) showWeek(endpoint string, chatID int64, modelID string) {
	if modelID != "" {
		w.showWeekForModel(endpoint, chatID, modelID)
		return
	}
	models := w.modelsForChat(endpoint, chatID)
	for _, m := range models {
		w.showWeekForModel(endpoint, chatID, m)
	}
	if len(models) == 0 {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ZeroSubscriptions, nil, replyPacket)
	}

}

func (w *worker) showWeekForModel(endpoint string, chatID int64, modelID string) {
	modelID = w.modelIDPreprocessing(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"model": modelID}, replyPacket)
		return
	}
	hours, start := w.week(modelID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Week, tplData{
		"hours":   hours,
		"weekday": int(start.UTC().Weekday()),
		"model":   modelID,
	}, replyPacket)
}

func (w *worker) addModel(endpoint string, chatID int64, modelID string, now int) bool {
	if modelID == "" {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].SyntaxAdd, nil, replyPacket)
		return false
	}
	modelID = w.modelIDPreprocessing(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"model": modelID}, replyPacket)
		return false
	}

	if w.subscriptionExists(endpoint, chatID, modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].AlreadyAdded, tplData{"model": modelID}, replyPacket)
		return false
	}
	subscriptionsNumber := w.subscriptionsNumber(endpoint, chatID)
	user := w.mustUser(chatID)
	if subscriptionsNumber >= user.maxModels {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].NotEnoughSubscriptions, nil, replyPacket)
		w.subscriptionUsage(endpoint, chatID, true)
		return false
	}
	var confirmedStatus lib.StatusKind
	if w.ourOnline[modelID] {
		confirmedStatus = lib.StatusOnline
	} else if _, ok := w.siteStatuses[modelID]; ok {
		confirmedStatus = lib.StatusOffline
	} else if w.maybeModel(modelID) != nil {
		confirmedStatus = lib.StatusOffline
	} else {
		w.mustExec("insert into signals (chat_id, model_id, endpoint, confirmed) values (?,?,?,?)", chatID, modelID, endpoint, false)
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].CheckingModel, nil, replyPacket)
		return false
	}
	w.mustExec("insert into signals (chat_id, model_id, endpoint, confirmed) values (?,?,?,?)", chatID, modelID, endpoint, true)
	w.mustExec("insert or ignore into models (model_id, status) values (?,?)", modelID, confirmedStatus)
	subscriptionsNumber++
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ModelAdded, tplData{"model": modelID}, replyPacket)
	nots := []notification{{
		endpoint: endpoint,
		chatID:   chatID,
		modelID:  modelID,
		status:   confirmedStatus,
		timeDiff: w.modelDuration(modelID, now),
		social:   false,
		priority: 1,
		kind:     replyPacket}}
	if subscriptionsNumber >= user.maxModels-w.cfg.HeavyUserRemainder {
		w.subscriptionUsage(endpoint, chatID, true)
	}
	w.storeNotifications(nots)
	return true
}

func (w *worker) subscriptionUsage(endpoint string, chatID int64, ad bool) {
	subscriptionsNumber := w.subscriptionsNumber(endpoint, chatID)
	user := w.mustUser(chatID)
	tr := w.tr[endpoint].SubscriptionUsage
	if ad {
		tr = w.tr[endpoint].SubscriptionUsageAd
	}
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, tr,
		tplData{
			"subscriptions_used":  subscriptionsNumber,
			"total_subscriptions": user.maxModels,
		},
		replyPacket)
}

func (w *worker) wantMore(endpoint string, chatID int64) {
	w.showReferral(endpoint, chatID)

	if w.cfg.CoinPayments == nil || w.cfg.Mail == nil {
		return
	}

	tpl := w.tpl[endpoint]
	text := templateToString(tpl, w.tr[endpoint].BuyAd.Key, tplData{
		"price":                   w.cfg.CoinPayments.subscriptionPacketPrice,
		"number_of_subscriptions": w.cfg.CoinPayments.subscriptionPacketModelNumber,
	})
	buttonText := templateToString(tpl, w.tr[endpoint].BuyButton.Key, tplData{
		"number_of_subscriptions": w.cfg.CoinPayments.subscriptionPacketModelNumber,
	})

	buttons := [][]tg.InlineKeyboardButton{{tg.NewInlineKeyboardButtonData(buttonText, "buy")}}
	keyboard := tg.NewInlineKeyboardMarkup(buttons...)
	msg := tg.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	w.enqueueMessage(w.highPriorityMsg, endpoint, &messageConfig{msg}, replyPacket)
}

func (w *worker) settings(endpoint string, chatID int64) {
	subscriptionsNumber := w.subscriptionsNumber(endpoint, chatID)
	user := w.mustUser(chatID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Settings, tplData{
		"subscriptions_used":              subscriptionsNumber,
		"total_subscriptions":             user.maxModels,
		"show_images":                     user.showImages,
		"offline_notifications_supported": w.cfg.OfflineNotifications,
		"offline_notifications":           user.offlineNotifications,
	}, replyPacket)
}

func (w *worker) enableImages(endpoint string, chatID int64, showImages bool) {
	w.mustExec("update users set show_images=? where chat_id=?", showImages, chatID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].OK, nil, replyPacket)
}

func (w *worker) enableOfflineNotifications(endpoint string, chatID int64, offlineNotifications bool) {
	w.mustExec("update users set offline_notifications=? where chat_id=?", offlineNotifications, chatID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].OK, nil, replyPacket)
}

func (w *worker) removeModel(endpoint string, chatID int64, modelID string) {
	if modelID == "" {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].SyntaxRemove, nil, replyPacket)
		return
	}
	modelID = w.modelIDPreprocessing(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"model": modelID}, replyPacket)
		return
	}
	if !w.subscriptionExists(endpoint, chatID, modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ModelNotInList, tplData{"model": modelID}, replyPacket)
		return
	}
	w.mustExec("delete from signals where chat_id=? and model_id=? and endpoint=?", chatID, modelID, endpoint)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ModelRemoved, tplData{"model": modelID}, replyPacket)
}

func (w *worker) sureRemoveAll(endpoint string, chatID int64) {
	w.mustExec("delete from signals where chat_id=? and endpoint=?", chatID, endpoint)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].AllModelsRemoved, nil, replyPacket)
}

func (w *worker) buy(endpoint string, chatID int64) {
	var buttons [][]tg.InlineKeyboardButton
	for _, c := range w.cfg.CoinPayments.Currencies {
		buttons = append(buttons, []tg.InlineKeyboardButton{tg.NewInlineKeyboardButtonData(c, "buy_with "+c)})
	}

	user := w.mustUser(chatID)
	keyboard := tg.NewInlineKeyboardMarkup(buttons...)
	tpl := w.tpl[endpoint]
	text := templateToString(tpl, w.tr[endpoint].SelectCurrency.Key, tplData{
		"dollars":                 w.cfg.CoinPayments.subscriptionPacketPrice,
		"number_of_subscriptions": w.cfg.CoinPayments.subscriptionPacketModelNumber,
		"total_subscriptions":     user.maxModels + w.cfg.CoinPayments.subscriptionPacketModelNumber,
	})

	msg := tg.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	w.enqueueMessage(w.highPriorityMsg, endpoint, &messageConfig{msg}, replyPacket)
}

func (w *worker) buyWith(endpoint string, chatID int64, currency string) {
	found := false
	for _, c := range w.cfg.CoinPayments.Currencies {
		if currency == c {
			found = true
			break
		}
	}
	if !found {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].UnknownCurrency, nil, replyPacket)
		return
	}

	email := w.email(endpoint, chatID)
	localID := uuid.New()
	transaction, err := w.coinPaymentsAPI.CreateTransaction(w.cfg.CoinPayments.subscriptionPacketPrice, currency, email, localID.String())
	if err != nil {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].TryToBuyLater, nil, replyPacket)
		lerr("create transaction failed, %v", err)
		return
	}
	kind := "coinpayments"
	timestamp := int(time.Now().Unix())
	w.mustExec(`
		insert into transactions (
			status,
			kind,
			local_id,
			chat_id,
			remote_id,
			timeout,
			amount,
			address,
			dest_tag,
			status_url,
			checkout_url,
			timestamp,
			model_number,
			currency,
			endpoint)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		payments.StatusCreated,
		kind,
		localID,
		chatID,
		transaction.TXNID,
		transaction.Timeout,
		transaction.Amount,
		transaction.Address,
		transaction.DestTag,
		transaction.StatusURL,
		transaction.CheckoutURL,
		timestamp,
		w.cfg.CoinPayments.subscriptionPacketModelNumber,
		currency,
		endpoint)

	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].PayThis, tplData{
		"price":    transaction.Amount,
		"currency": currency,
		"link":     transaction.CheckoutURL,
	}, replyPacket)
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

func (w *worker) listModels(endpoint string, chatID int64, now int) {
	type data struct {
		Model    string
		TimeDiff *timeDiff
	}
	statuses := w.statusesForChat(endpoint, chatID)
	var online, offline, denied []data
	for _, s := range statuses {
		data := data{
			Model:    s.modelID,
			TimeDiff: w.modelTimeDiff(s.modelID, now),
		}
		switch s.status {
		case lib.StatusOnline:
			online = append(online, data)
		case lib.StatusDenied:
			denied = append(denied, data)
		default:
			offline = append(offline, data)
		}
	}
	tplData := tplData{"online": online, "offline": offline, "denied": denied}
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].List, tplData, replyPacket)
}

func (w *worker) modelDuration(modelID string, now int) *int {
	begin, end, prevStatus := w.lastSeenInfo(modelID, now)
	if end != 0 {
		timeDiff := now - end
		return &timeDiff
	}
	if begin != 0 && prevStatus != lib.StatusUnknown {
		timeDiff := now - begin
		return &timeDiff
	}
	return nil
}

func (w *worker) modelTimeDiff(modelID string, now int) *timeDiff {
	dur := w.modelDuration(modelID, now)
	if dur != nil {
		timeDiff := calcTimeDiff(*dur)
		return &timeDiff
	}
	return nil
}

func (w *worker) downloadSuccess(success bool) { w.downloadResults <- success }

func (w *worker) downloadImage(url string) []byte {
	imageBytes, err := w.downloadImageInternal(url)
	if err != nil {
		if w.cfg.Debug {
			ldbg("cannot download image, %v", err)
		}
	}
	w.downloadSuccess(err != nil)
	return imageBytes
}

func (w *worker) downloadImageInternal(url string) ([]byte, error) {
	resp, err := w.clients[0].Client.Get(url)
	if err != nil {
		return nil, errors.New("cannot make image query")
	}
	defer func() { checkErr(resp.Body.Close()) }()
	if resp.StatusCode != 200 {
		return nil, errors.New("cannot download image data")
	}
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		return nil, errors.New("cannot read image")
	}
	data := buf.Bytes()
	_, _, err = image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("cannot decode image")
	}
	return data, nil
}

func (w *worker) listOnlineModels(endpoint string, chatID int64, now int) {
	statuses := w.statusesForChat(endpoint, chatID)
	var online []model
	for _, s := range statuses {
		if s.status == lib.StatusOnline {
			online = append(online, s)
		}
	}
	if len(online) > w.cfg.MaxSubscriptionsForPics && chatID < -1 {
		data := tplData{"max_subs": w.cfg.MaxSubscriptionsForPics}
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].TooManySubscriptionsForPics, data, replyPacket)
		return
	}
	if len(online) == 0 {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].NoOnlineModels, nil, replyPacket)
		return
	}
	var nots []notification
	for _, s := range online {
		not := notification{
			priority: 1,
			endpoint: endpoint,
			chatID:   chatID,
			modelID:  s.modelID,
			status:   lib.StatusOnline,
			imageURL: w.images[s.modelID],
			timeDiff: w.modelDuration(s.modelID, now),
			kind:     replyPacket,
		}
		nots = append(nots, not)
	}
	w.storeNotifications(nots)
}

func (w *worker) week(modelID string) ([]bool, time.Time) {
	now := time.Now()
	nowTimestamp := int(now.Unix())
	today := now.Truncate(24 * time.Hour)
	start := today.Add(-6 * 24 * time.Hour)
	weekTimestamp := int(start.Unix())
	changes := w.changesFromTo(modelID, weekTimestamp, nowTimestamp)
	hours := make([]bool, (nowTimestamp-weekTimestamp+3599)/3600)
	for i, c := range changes[:len(changes)-1] {
		if c.status == lib.StatusOnline {
			begin := (c.timestamp - weekTimestamp) / 3600
			if begin < 0 {
				begin = 0
			}
			end := (changes[i+1].timestamp - weekTimestamp + 3599) / 3600
			for j := begin; j < end; j++ {
				hours[j] = true
			}
		}
	}
	return hours, start
}

func (w *worker) feedback(endpoint string, chatID int64, text string) {
	if text == "" {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].SyntaxFeedback, nil, replyPacket)
		return
	}
	w.mustExec("insert into feedback (endpoint, chat_id, text) values (?, ?, ?)", endpoint, chatID, text)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Feedback, nil, replyPacket)
	user := w.mustUser(chatID)
	if !user.blacklist {
		finalText := fmt.Sprintf("Feedback from %d: %s", chatID, text)
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, true, true, lib.ParseRaw, finalText, replyPacket)
	}
}

func (w *worker) unsuccessfulRequestsCount() int {
	var count = 0
	for _, s := range w.unsuccessfulRequests {
		if s {
			count++
		}
	}
	return count
}

func (w *worker) downloadErrorsCount() int {
	var count = 0
	for _, s := range w.downloadErrors {
		if s {
			count++
		}
	}
	return count
}

func (w *worker) statStrings(endpoint string) []string {
	stat := w.getStat(endpoint)
	return []string{
		fmt.Sprintf("Users: %d", stat.UsersCount),
		fmt.Sprintf("Groups: %d", stat.GroupsCount),
		fmt.Sprintf("Active users: %d", stat.ActiveUsersOnEndpointCount),
		fmt.Sprintf("Heavy: %d", stat.HeavyUsersCount),
		fmt.Sprintf("Models: %d", stat.ModelsCount),
		fmt.Sprintf("Models to poll: %d", stat.ModelsToPollOnEndpointCount),
		fmt.Sprintf("Models to poll total: %d", stat.ModelsToPollTotalCount),
		fmt.Sprintf("Models online: %d", stat.OnlineModelsCount),
		fmt.Sprintf("Status changes: %d", stat.StatusChangesCount),
		fmt.Sprintf("Queries duration: %d ms", stat.QueriesDurationMilliseconds),
		fmt.Sprintf("Updates duration: %d ms", stat.UpdatesDurationMilliseconds),
		fmt.Sprintf("Error rate: %d/%d", stat.ErrorRate[0], stat.ErrorRate[1]),
		fmt.Sprintf("Memory usage: %d KiB", stat.Rss),
		fmt.Sprintf("Transactions: %d/%d", stat.TransactionsOnEndpointFinished, stat.TransactionsOnEndpointCount),
		fmt.Sprintf("Reports: %d", stat.ReportsCount),
		fmt.Sprintf("User referrals: %d", stat.UserReferralsCount),
		fmt.Sprintf("Model referrals: %d", stat.ModelReferralsCount),
		fmt.Sprintf("Changes in period: %d", stat.ChangesInPeriod),
		fmt.Sprintf("Confirmed changes in period: %d", stat.ConfirmedChangesInPeriod),
	}
}

func (w *worker) stat(endpoint string) {
	text := strings.Join(w.statStrings(endpoint), "\n")
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, true, true, lib.ParseRaw, text, replyPacket)
}

func (w *worker) performanceStat(endpoint string, arguments string) {
	parts := strings.Split(arguments, " ")
	if len(parts) > 2 {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "wrong number of arguments", replyPacket)
		return
	}
	n := int64(10)
	if len(parts) == 2 {
		var err error
		n, err = strconv.ParseInt(parts[1], 10, 32)
		if err != nil {
			w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "cannot parse arguments", replyPacket)
			return
		}
	}
	durations := w.durations
	var queries []string
	for x := range durations {
		queries = append(queries, x)
	}
	if len(parts) >= 1 && parts[0] == "avg" {
		sort.SliceStable(queries, func(i, j int) bool {
			return durations[queries[i]].avg > durations[queries[j]].avg
		})
	} else {
		sort.SliceStable(queries, func(i, j int) bool {
			return durations[queries[i]].total() > durations[queries[j]].total()
		})
	}
	for _, x := range queries {
		if n == 0 {
			return
		}
		lines := []string{
			fmt.Sprintf("<b>Desc</b>: %s", html.EscapeString(x)),
			fmt.Sprintf("<b>Total</b>: %d", int(durations[x].avg*float64(durations[x].count)*1000.)),
			fmt.Sprintf("<b>Avg</b>: %d", int(durations[x].avg*1000.)),
			fmt.Sprintf("<b>Count</b>: %d", durations[x].count),
		}
		entry := strings.Join(lines, "\n")
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseHTML, entry, replyPacket)
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
	chats := w.broadcastChats(endpoint)
	for _, chatID := range chats {
		w.sendText(w.lowPriorityMsg, endpoint, chatID, true, false, lib.ParseRaw, text, messagePacket)
	}
	w.sendText(w.lowPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK", replyPacket)
}

func (w *worker) direct(endpoint string, arguments string) {
	parts := strings.SplitN(arguments, " ", 2)
	if len(parts) < 2 {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "usage: /direct chatID text", replyPacket)
		return
	}
	whom, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "first argument is invalid", replyPacket)
		return
	}
	text := parts[1]
	if text == "" {
		return
	}
	w.sendText(w.highPriorityMsg, endpoint, whom, true, false, lib.ParseRaw, text, messagePacket)
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK", replyPacket)
}

func (w *worker) blacklist(endpoint string, arguments string) {
	whom, err := strconv.ParseInt(arguments, 10, 64)
	if err != nil {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "first argument is invalid", replyPacket)
		return
	}
	w.mustExec("update users set blacklist=1 where chat_id=?", whom)
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK", replyPacket)
}

func (w *worker) addSpecialModel(endpoint string, arguments string) {
	parts := strings.Split(arguments, " ")
	if len(parts) != 2 || (parts[0] != "set" && parts[0] != "unset") {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "usage: /special set/unset MODEL_ID", replyPacket)
		return
	}
	modelID := w.modelIDPreprocessing(parts[1])
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "MODEL_ID is invalid", replyPacket)
		return
	}
	set := parts[0] == "set"
	w.mustExec(`
		insert into models (model_id, special) values (?,?)
		on conflict(model_id) do update set special=excluded.special`,
		modelID,
		set)
	if set {
		w.specialModels[modelID] = true
	} else {
		delete(w.specialModels, modelID)
	}
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK", replyPacket)
}

func (w *worker) serveEndpoints() {
	go func() {
		err := http.ListenAndServe(w.cfg.ListenAddress, nil)
		checkErr(err)
	}()
}

func (w *worker) logConfig() {
	cfgString, err := json.MarshalIndent(w.cfg, "", "    ")
	checkErr(err)
	linf("config: " + string(cfgString))
	for k, v := range w.trAds {
		linf("ads for %s: %d", k, len(v))
	}
}

func (w *worker) myEmail(endpoint string) {
	email := w.email(endpoint, w.cfg.AdminID)
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, true, true, lib.ParseRaw, email, replyPacket)
}

func (w *worker) processAdminMessage(endpoint string, chatID int64, command, arguments string) (processed bool, maintenance bool) {
	switch command {
	case "stat":
		w.stat(endpoint)
		return true, false
	case "performance":
		w.performanceStat(endpoint, arguments)
		return true, false
	case "email":
		w.myEmail(endpoint)
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
	case "special":
		w.addSpecialModel(endpoint, arguments)
		return true, false
	case "set_max_models":
		parts := strings.Fields(arguments)
		if len(parts) != 2 {
			w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, lib.ParseRaw, "expecting two arguments", replyPacket)
			return true, false
		}
		who, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, lib.ParseRaw, "first argument is invalid", replyPacket)
			return true, false
		}
		maxModels, err := strconv.Atoi(parts[1])
		if err != nil {
			w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, lib.ParseRaw, "second argument is invalid", replyPacket)
			return true, false
		}
		w.setLimit(who, maxModels)
		w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, lib.ParseRaw, "OK", replyPacket)
		return true, false
	case "maintenance":
		w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, lib.ParseRaw, "OK", replyPacket)
		return true, true
	}
	return false, false
}

func splitAddress(a string) (string, string) {
	a = strings.ToLower(a)
	parts := strings.Split(a, "@")
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func (w *worker) mailReceived(e *env) {
	emails := make(map[email]bool)
	for _, r := range e.rcpts {
		username, host := splitAddress(r.Email())
		if host != w.cfg.Mail.Host {
			continue
		}
		email := w.recordForEmail(username)
		if email != nil {
			emails[*email] = true
		}
	}

	for email := range emails {
		w.sendTr(w.lowPriorityMsg, email.endpoint, email.chatID, true, w.tr[email.endpoint].MailReceived, tplData{
			"subject": e.mime.GetHeader("Subject"),
			"from":    e.mime.GetHeader("From"),
			"text":    e.mime.Text,
		}, messagePacket)
		for _, inline := range e.mime.Inlines {
			b := tg.FileBytes{Name: inline.FileName, Bytes: inline.Content}
			switch {
			case strings.HasPrefix(inline.ContentType, "image/"):
				msg := tg.NewPhotoUpload(email.chatID, b)
				w.enqueueMessage(w.lowPriorityMsg, email.endpoint, &photoConfig{msg}, messagePacket)
			default:
				msg := tg.NewDocumentUpload(email.chatID, b)
				w.enqueueMessage(w.lowPriorityMsg, email.endpoint, &documentConfig{msg}, messagePacket)
			}
		}
		for _, inline := range e.mime.Attachments {
			b := tg.FileBytes{Name: inline.FileName, Bytes: inline.Content}
			msg := tg.NewDocumentUpload(email.chatID, b)
			w.enqueueMessage(w.lowPriorityMsg, email.endpoint, &documentConfig{msg}, messagePacket)
		}
	}
}

func envelopeFactory(ch chan *env) func(smtpd.Connection, smtpd.MailAddress, *int) (smtpd.Envelope, error) {
	return func(c smtpd.Connection, from smtpd.MailAddress, size *int) (smtpd.Envelope, error) {
		return &env{BasicEnvelope: &smtpd.BasicEnvelope{}, from: from, ch: ch}, nil
	}
}

//noinspection SpellCheckingInspection
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
		if w.mustInt("select count(*) from referrals where referral_id=?", id) == 0 {
			break
		}
	}
	return
}

func (w *worker) refer(followerChatID int64, referrer string) (applied appliedKind) {
	referrerChatID := w.chatForReferralID(referrer)
	if referrerChatID == nil {
		return invalidReferral
	}
	if _, exists := w.user(followerChatID); exists {
		return followerExists
	}
	w.mustExec("insert into users (chat_id, max_models) values (?, ?)", followerChatID, w.cfg.MaxModels+w.cfg.FollowerBonus)
	w.mustExec(`
		insert into users (chat_id, max_models) values (?, ?)
		on conflict(chat_id) do update set max_models=max_models+?`,
		*referrerChatID,
		w.cfg.MaxModels+w.cfg.ReferralBonus,
		w.cfg.ReferralBonus)
	w.mustExec("update referrals set referred_users=referred_users+1 where chat_id=?", referrerChatID)
	return referralApplied
}

func (w *worker) showReferral(endpoint string, chatID int64) {
	referralID := w.referralID(chatID)
	if referralID == nil {
		temp := w.newRandReferralID()
		referralID = &temp
		w.mustExec("insert into referrals (chat_id, referral_id) values (?, ?)", chatID, *referralID)
	}
	referralLink := fmt.Sprintf("https://t.me/%s?start=%s", w.botNames[endpoint], *referralID)
	subscriptionsNumber := w.subscriptionsNumber(endpoint, chatID)
	user := w.mustUser(chatID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ReferralLink, tplData{
		"link":                referralLink,
		"referral_bonus":      w.cfg.ReferralBonus,
		"follower_bonus":      w.cfg.FollowerBonus,
		"subscriptions_used":  subscriptionsNumber,
		"total_subscriptions": user.maxModels,
	}, replyPacket)
}

func (w *worker) start(endpoint string, chatID int64, referrer string, now int) {
	modelID := ""
	switch {
	case strings.HasPrefix(referrer, "m-"):
		modelID = referrer[2:]
		modelID = w.modelIDPreprocessing(modelID)
		referrer = ""
	case referrer != "":
		referralID := w.referralID(chatID)
		if referralID != nil && *referralID == referrer {
			w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].OwnReferralLinkHit, nil, replyPacket)
			return
		}
	}
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Help, tplData{
		"website_link": w.cfg.WebsiteLink,
	}, replyPacket)
	if chatID > 0 && referrer != "" {
		applied := w.refer(chatID, referrer)
		switch applied {
		case referralApplied:
			w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ReferralApplied, nil, replyPacket)
		case invalidReferral:
			w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].InvalidReferralLink, nil, replyPacket)
		case followerExists:
			w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].FollowerExists, nil, replyPacket)
		}
	}
	w.addUser(endpoint, chatID)
	if modelID != "" {
		if w.addModel(endpoint, chatID, modelID, now) {
			w.mustExec("update models set referred_users=referred_users+1 where model_id=?", modelID)
		}
	}
}

func (w *worker) processIncomingCommand(endpoint string, chatID int64, command, arguments string, now int) bool {
	w.resetBlock(endpoint, chatID)
	command = strings.ToLower(command)
	if command != "start" {
		w.addUser(endpoint, chatID)
	}
	linf("chat: %d, command: %s %s", chatID, command, arguments)

	if chatID == w.cfg.AdminID {
		if proc, maintenance := w.processAdminMessage(endpoint, chatID, command, arguments); proc {
			return maintenance
		}
	}

	unknown := func() {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].UnknownCommand, nil, replyPacket)
	}

	switch command {
	case "add":
		arguments = strings.Replace(arguments, "—", "--", -1)
		_ = w.addModel(endpoint, chatID, arguments, now)
	case "remove":
		arguments = strings.Replace(arguments, "—", "--", -1)
		w.removeModel(endpoint, chatID, arguments)
	case "list":
		w.listModels(endpoint, chatID, now)
	case "pics", "online":
		w.listOnlineModels(endpoint, chatID, now)
	case "start", "help":
		w.start(endpoint, chatID, arguments, now)
	case "ad":
		w.ad(w.highPriorityMsg, endpoint, chatID)
	case "faq":
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].FAQ, tplData{
			"dollars":                 w.cfg.CoinPayments.subscriptionPacketPrice,
			"number_of_subscriptions": w.cfg.CoinPayments.subscriptionPacketModelNumber,
			"max_models":              w.cfg.MaxModels,
		}, replyPacket)
	case "feedback":
		w.feedback(endpoint, chatID, arguments)
	case "social":
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Social, nil, replyPacket)
	case "version":
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Version, tplData{"version": version}, replyPacket)
	case "remove_all", "stop":
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].RemoveAll, nil, replyPacket)
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
	case "buy":
		if w.cfg.CoinPayments == nil || w.cfg.Mail == nil {
			unknown()
			return false
		}
		w.buy(endpoint, chatID)
	case "buy_with":
		if w.cfg.CoinPayments == nil || w.cfg.Mail == nil {
			unknown()
			return false
		}
		w.buyWith(endpoint, chatID, arguments)
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

func (w *worker) subscriptions() map[string]lib.StatusKind {
	subs := w.mustStrings("select distinct(model_id) from signals")
	result := map[string]lib.StatusKind{}
	for _, s := range subs {
		result[s] = w.siteStatuses[s].status
	}
	return result
}

func (w *worker) periodic() {
	unsuccessfulRequestsCount := w.unsuccessfulRequestsCount()
	now := time.Now()
	if w.nextErrorReport.Before(now) && unsuccessfulRequestsCount > w.cfg.errorThreshold {
		text := fmt.Sprintf("Dangerous error rate reached: %d/%d", unsuccessfulRequestsCount, w.cfg.errorDenominator)
		w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, true, true, lib.ParseRaw, text, messagePacket)
		w.nextErrorReport = now.Add(time.Minute * time.Duration(w.cfg.ErrorReportingPeriodMinutes))
	}
	w.initiateOnlineQuery()
}

func (w *worker) initiateOnlineQuery() {
	err := w.checker.Updater().QueryUpdates(lib.StatusUpdateRequest{
		Callback:      func(res lib.StatusUpdateResults) { w.onlineModelsChan <- res },
		SpecialModels: w.specialModels,
		Subscriptions: w.subscriptions(),
	})
	if err != nil {
		lerr("%v", err)
	}
}

func (w *worker) initiateSpecificQuery(resultsCh chan lib.StatusResults, specific map[string]bool) {
	err := w.checker.QueryStatuses(lib.StatusRequest{
		Callback:  func(res lib.StatusResults) { resultsCh <- res },
		Specific:  specific,
		CheckMode: lib.CheckStatuses})
	if err != nil {
		lerr("%v", err)
	}
}

func (w *worker) processStatusUpdates(updates []lib.StatusUpdate, now int) (
	changesCount int,
	confirmedChangesCount int,
	notifications []notification,
	elapsed time.Duration,
) {
	start := time.Now()
	usersForModels, endpointsForModels := w.usersForModels()
	tx, err := w.db.Begin()
	checkErr(err)

	insertStatusChangeStmt, err := tx.Prepare(insertStatusChange)
	checkErr(err)
	updateLastStatusChangeStmt, err := tx.Prepare(updateLastStatusChange)
	checkErr(err)
	updateModelStatusStmt, err := tx.Prepare(updateModelStatus)
	checkErr(err)

	changesCount = len(updates)

	statusDone := w.measure("db: status updates")
	for _, u := range updates {
		statusChange := statusChange{modelID: u.ModelID, status: u.Status, timestamp: now}
		w.updateStatus(insertStatusChangeStmt, updateLastStatusChangeStmt, statusChange)
	}
	statusDone()

	confirmationsDone := w.measure("db: confirmations")
	confirmations := w.confirmStatus(updateModelStatusStmt, now)
	confirmationsDone()

	if w.cfg.Debug {
		ldbg("confirmed online models: %d", len(w.ourOnline))
	}

	for _, c := range confirmations {
		users := usersForModels[c]
		endpoints := endpointsForModels[c]
		for i, user := range users {
			status := w.siteStatuses[c].status
			if (w.cfg.OfflineNotifications && user.offlineNotifications) || status != lib.StatusOffline {
				n := notification{
					endpoint: endpoints[i],
					chatID:   user.chatID,
					modelID:  c,
					status:   status,
					social:   true,
					sound:    status == lib.StatusOnline,
					kind:     notificationPacket}
				if user.showImages {
					n.imageURL = w.images[c]
				}
				notifications = append(notifications, n)
			}
		}
	}

	confirmedChangesCount = len(confirmations)

	defer w.measure("db: status updates commit")()
	checkErr(insertStatusChangeStmt.Close())
	checkErr(updateLastStatusChangeStmt.Close())
	checkErr(updateModelStatusStmt.Close())
	checkErr(tx.Commit())
	elapsed = time.Since(start)
	return
}

func getCommandAndArgs(update tg.Update, mention string, ourIDs []int64) (int64, string, string) {
	var text string
	var chatID int64
	var forceMention bool
	if update.Message != nil && update.Message.Chat != nil {
		text = update.Message.Text
		chatID = update.Message.Chat.ID
		if update.Message.NewChatMembers != nil {
			for _, m := range *update.Message.NewChatMembers {
				for _, ourID := range ourIDs {
					if int64(m.ID) == ourID {
						return chatID, "start", ""
					}
				}
			}
		}
	} else if update.ChannelPost != nil && update.ChannelPost.Chat != nil {
		text = update.ChannelPost.Text
		chatID = update.ChannelPost.Chat.ID
		forceMention = true
	} else if update.CallbackQuery != nil && update.CallbackQuery.From != nil {
		text = update.CallbackQuery.Data
		chatID = int64(update.CallbackQuery.From.ID)
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

func (w *worker) processTGUpdate(p incomingPacket) bool {
	now := int(time.Now().Unix())
	u := p.message
	mention := "@" + w.botNames[p.endpoint]
	chatID, command, args := getCommandAndArgs(u, mention, w.ourIDs)
	if u.CallbackQuery != nil {
		callback := tg.CallbackConfig{CallbackQueryID: u.CallbackQuery.ID}
		_, err := w.bots[p.endpoint].AnswerCallbackQuery(callback)
		if err != nil {
			lerr("cannot answer callback query, %v", err)
		}
	}
	if command != "" {
		return w.processIncomingCommand(p.endpoint, chatID, command, args, now)
	}
	return false
}

func getRss() (int64, error) {
	buf, err := ioutil.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, err
	}

	fields := strings.Split(string(buf), " ")
	if len(fields) < 2 {
		return 0, errors.New("cannot parse statm")
	}

	rss, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}

	return rss * int64(os.Getpagesize()), err
}

func (w *worker) getStat(endpoint string) statistics {
	rss, err := getRss()
	checkErr(err)
	var rusage syscall.Rusage
	checkErr(syscall.Getrusage(syscall.RUSAGE_SELF, &rusage))

	return statistics{
		UsersCount:                     w.usersCount(endpoint),
		GroupsCount:                    w.groupsCount(endpoint),
		ActiveUsersOnEndpointCount:     w.activeUsersOnEndpointCount(endpoint),
		ActiveUsersTotalCount:          w.activeUsersTotalCount(),
		HeavyUsersCount:                w.heavyUsersCount(endpoint),
		ModelsCount:                    w.modelsCount(endpoint),
		ModelsToPollOnEndpointCount:    w.modelsToPollOnEndpointCount(endpoint),
		ModelsToPollTotalCount:         w.modelsToPollTotalCount(),
		OnlineModelsCount:              len(w.ourOnline),
		KnownModelsCount:               len(w.siteStatuses),
		SpecialModelsCount:             len(w.specialModels),
		StatusChangesCount:             w.statusChangesCount(),
		TransactionsOnEndpointCount:    w.transactionsOnEndpoint(endpoint),
		TransactionsOnEndpointFinished: w.transactionsOnEndpointFinished(endpoint),
		QueriesDurationMilliseconds:    int(w.httpQueriesDuration.Milliseconds()),
		UpdatesDurationMilliseconds:    int(w.updatesDuration.Milliseconds()),
		CleaningDurationMilliseconds:   int(w.cleaningDuration.Milliseconds()),
		ErrorRate:                      [2]int{w.unsuccessfulRequestsCount(), w.cfg.errorDenominator},
		DownloadErrorRate:              [2]int{w.downloadErrorsCount(), w.cfg.errorDenominator},
		Rss:                            rss / 1024,
		MaxRss:                         rusage.Maxrss,
		UserReferralsCount:             w.userReferralsCount(),
		ModelReferralsCount:            w.modelReferralsCount(),
		ReportsCount:                   w.reports(),
		ChangesInPeriod:                w.changesInPeriod,
		ConfirmedChangesInPeriod:       w.confirmedChangesInPeriod,
		Interactions:                   w.interactionsByResultToday(endpoint),
		InteractionsByKind:             w.interactionsByKindToday(endpoint),
	}
}

func (w *worker) handleStat(endpoint string, statRequests chan statRequest) func(writer http.ResponseWriter, r *http.Request) {
	return func(writer http.ResponseWriter, r *http.Request) {
		command := statRequest{
			endpoint: endpoint,
			writer:   writer,
			request:  r,
			done:     make(chan bool),
		}
		statRequests <- command
		<-command.done
	}
}

func (w *worker) processStatCommand(endpoint string, writer http.ResponseWriter, r *http.Request, done chan bool) {
	defer func() { done <- true }()
	passwords, ok := r.URL.Query()["password"]
	if !ok || len(passwords) < 1 {
		return
	}
	password := passwords[0]
	if password != w.cfg.StatPassword {
		return
	}
	writer.WriteHeader(http.StatusOK)
	writer.Header().Set("Content-Type", "application/json")

	measureDone := w.measure("db: retrieving stats")
	statJSON, err := json.MarshalIndent(w.getStat(endpoint), "", "    ")
	measureDone()
	checkErr(err)
	_, err = writer.Write(statJSON)
	if err != nil {
		lerr("error on processing stat command, %v", err)
	}
}

func (w *worker) handleIPN(ipnRequests chan ipnRequest) func(writer http.ResponseWriter, r *http.Request) {
	return func(writer http.ResponseWriter, r *http.Request) {
		command := ipnRequest{
			writer:  writer,
			request: r,
			done:    make(chan bool),
		}
		ipnRequests <- command
		<-command.done
	}
}

func (w *worker) processIPN(writer http.ResponseWriter, r *http.Request, done chan bool) {
	defer func() { done <- true }()

	linf("got IPN data")

	newStatus, custom, err := payments.ParseIPN(r, w.cfg.CoinPayments.IPNSecret, w.cfg.Debug)
	if err != nil {
		lerr("error on processing IPN, %v", err)
		return
	}

	switch newStatus {
	case payments.StatusFinished:
		oldStatus, chatID, endpoint, found := w.transaction(custom)
		if !found {
			lerr("transaction not found: %s", custom)
			return
		}
		if oldStatus == payments.StatusFinished {
			lerr("transaction is already finished")
			return
		}
		if oldStatus == payments.StatusUnknown {
			lerr("unknown transaction ID")
			return
		}
		w.mustExec("update transactions set status=? where local_id=?", payments.StatusFinished, custom)
		w.mustExec("update users set max_models = max_models + (select coalesce(sum(model_number), 0) from transactions where local_id=?)", custom)
		user := w.mustUser(chatID)
		w.sendTr(w.lowPriorityMsg, endpoint, chatID, false, w.tr[endpoint].PaymentComplete, tplData{"max_models": user.maxModels}, messagePacket)
		linf("payment %s is finished", custom)
		text := fmt.Sprintf("payment %s is finished", custom)
		w.sendText(w.lowPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, text, messagePacket)
	case payments.StatusCanceled:
		w.mustExec("update transactions set status=? where local_id=?", payments.StatusCanceled, custom)
		linf("payment %s is canceled", custom)
		text := fmt.Sprintf("payment %s is cancelled", custom)
		w.sendText(w.lowPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, text, messagePacket)
	default:
		linf("payment %s is still pending", custom)
		text := fmt.Sprintf("payment %s is still pending", custom)
		w.sendText(w.lowPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, text, messagePacket)
	}
}

func (w *worker) handleStatEndpoints(statRequests chan statRequest) {
	for n, p := range w.cfg.Endpoints {
		if p.StatPath != "" {
			http.HandleFunc(p.WebhookDomain+p.StatPath, w.handleStat(n, statRequests))
		}
	}
}

func (w *worker) handleIPNEndpoint(ipnRequests chan ipnRequest) {
	http.HandleFunc(w.cfg.CoinPayments.IPNListenURL, w.handleIPN(ipnRequests))
}

func (w *worker) incoming() chan incomingPacket {
	result := make(chan incomingPacket)
	for n, p := range w.cfg.Endpoints {
		linf("listening for a webhook for endpoint %s", n)
		incoming := w.bots[n].ListenForWebhook(p.WebhookDomain + p.ListenPath)
		go func(n string, incoming tg.UpdatesChannel) {
			for i := range incoming {
				result <- incomingPacket{message: i, endpoint: n}
			}
		}(n, incoming)
	}
	return result
}

func (c *config) getOurIDs() []int64 {
	var ids []int64
	for _, e := range c.Endpoints {
		if idx := strings.Index(e.BotToken, ":"); idx != -1 {
			id, err := strconv.ParseInt(e.BotToken[:idx], 10, 64)
			checkErr(err)
			ids = append(ids, id)
		} else {
			checkErr(errors.New("cannot get our ID"))
		}
	}
	return ids
}

func loadTLS(certFile string, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}

func (q queryDurationsData) total() float64 {
	return q.avg * float64(q.count)
}

func (w *worker) logQuerySuccess(success bool, errors int) {
	for i := 0; i < errors; i++ {
		w.unsuccessfulRequests[w.unsuccessfulRequestsPos] = !success
		w.unsuccessfulRequestsPos = (w.unsuccessfulRequestsPos + 1) % w.cfg.errorDenominator
	}
}

func (w *worker) cleanStatusChanges(now int64) time.Duration {
	start := time.Now()
	threshold := int(now) - w.cfg.KeepStatusesForDays*24*60*60
	limit := w.mustInt("select coalesce(min(timestamp), 0) from status_changes") + w.cfg.MaxCleanSeconds
	if limit < threshold {
		threshold = limit
	}
	w.mustExec("delete from status_changes where timestamp < ?", threshold)
	w.mustExec("delete from last_status_changes where timestamp < ?", threshold)
	for k, v := range w.siteStatuses {
		if v.timestamp < threshold {
			delete(w.siteStatuses, k)
			delete(w.siteOnline, k)
		}
	}
	return time.Since(start)
}

func (w *worker) vacuum() time.Duration {
	start := time.Now()
	w.mustExec("vacuum")
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
				w.sendTr(w.highPriorityMsg, u.endpoint, chatID, false, w.tr[u.endpoint].Maintenance, nil, replyPacket)
				linf("ignoring command %s %s", command, args)
			}
		case <-done:
			for user := range waitingUsers {
				w.sendTr(w.highPriorityMsg, user.endpoint, user.chatID, false, w.tr[user.endpoint].WeAreUp, nil, messagePacket)
			}
			return
		case <-w.outgoingMsgResults:
		}
	}
}

func (w *worker) sendReadyNotifications() { w.sendingNotifications <- w.newNotifications() }

func (w *worker) sendNotificationsDaemon() {
	for nots := range w.sendingNotifications {
		w.notifyOfStatuses(w.highPriorityMsg, w.lowPriorityMsg, nots)
		w.sentNotifications <- nots
	}
}

func (w *worker) queryUnconfirmedSubs() {
	unconfirmed := map[string]bool{}
	var modelID string
	w.mustQuery("select model_id from signals where confirmed=0", nil, scanTo{&modelID}, func() { unconfirmed[modelID] = true })
	if len(unconfirmed) > 0 {
		ldbg("queueing unconfirmed subscriptions check for %d channels", len(unconfirmed))
		w.initiateSpecificQuery(w.unconfirmedSubsResults, unconfirmed)
	}
}

func (w *worker) processSubsConfirmations(res lib.StatusResults) {
	statusesNumber := 0
	if res.Data != nil {
		statusesNumber = len(res.Data.Statuses)
	}
	ldbg("processing subscription confirmations for %d channels", statusesNumber)
	unconfirmed := map[string][]subscription{}
	var iter subscription
	w.mustQuery(
		"select endpoint, model_id, chat_id from signals where confirmed=0",
		nil,
		scanTo{&iter.endpoint, &iter.modelID, &iter.chatID},
		func() { unconfirmed[iter.modelID] = append(unconfirmed[iter.modelID], iter) })
	var nots []notification
	if res.Data != nil {
		for modelID, status := range res.Data.Statuses {
			for _, sub := range unconfirmed[modelID] {
				if status&(lib.StatusOnline|lib.StatusOffline|lib.StatusDenied) != 0 {
					w.confirmSub(sub)
				} else {
					w.denySub(sub)
				}
				n := notification{endpoint: sub.endpoint, chatID: sub.chatID, modelID: modelID, status: status, social: false, priority: 1, kind: replyPacket}
				nots = append(nots, n)
			}
		}
	} else {
		lerr("confirmations query failed")
	}
	w.notifyOfAddResults(w.highPriorityMsg, nots, false)
	w.storeNotifications(nots)
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
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "still processing", replyPacket)
					} else {
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK", replyPacket)
						for user := range users {
							w.sendTr(w.highPriorityMsg, user.endpoint, chatID, false, w.tr[user.endpoint].WeAreUp, nil, messagePacket)
						}
						return true
					}
				case "clean":
					if processing {
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "still processing", replyPacket)
					} else {
						processing = true
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK", replyPacket)
						go func() {
							processingDone <- w.cleanStatusChanges(time.Now().Unix())
						}()
					}
				case "vacuum":
					if processing {
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "still processing", replyPacket)
					} else {
						processing = true
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK", replyPacket)
						go func() {
							processingDone <- w.vacuum()
						}()
					}
				case "":
				default:
					w.sendTr(w.highPriorityMsg, u.endpoint, chatID, false, w.tr[u.endpoint].Maintenance, nil, replyPacket)
				}
			} else {
				if command != "" {
					users[waitingUser{chatID: chatID, endpoint: u.endpoint}] = true
					w.sendTr(w.highPriorityMsg, u.endpoint, chatID, false, w.tr[u.endpoint].Maintenance, nil, replyPacket)
					linf("ignoring command %s %s", command, args)
				}
			}
		case elapsed := <-processingDone:
			processing = false
			text := fmt.Sprintf("processing done in %v", elapsed)
			w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, lib.ParseRaw, text, messagePacket)
		case <-w.outgoingMsgResults:
		}
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	w := newWorker()
	w.logConfig()
	w.setWebhook()
	w.setCommands()
	w.initBotNames()
	databaseDone := make(chan bool)
	w.serveEndpoints()
	incoming := w.incoming()
	go w.sender(w.highPriorityMsg, 0)
	go w.sender(w.lowPriorityMsg, 1)
	go w.maintenanceStartupReply(incoming, databaseDone)
	go w.sendNotificationsDaemon()
	w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, true, true, lib.ParseRaw, "bot started", messagePacket)
	w.createDatabase(databaseDone)
	w.initCache()
	w.mustExec("update notification_queue set sending=0")

	statRequests := make(chan statRequest)
	w.handleStatEndpoints(statRequests)

	ipnRequests := make(chan ipnRequest)
	if w.cfg.CoinPayments != nil {
		w.handleIPNEndpoint(ipnRequests)
	}

	mail := make(chan *env)

	if w.cfg.Mail != nil {
		smtp := &smtpd.Server{
			Hostname:  w.cfg.Mail.Host,
			Addr:      w.cfg.Mail.ListenAddress,
			OnNewMail: envelopeFactory(mail),
			TLSConfig: w.mailTLS,
		}
		go func() {
			err := smtp.ListenAndServe()
			checkErr(err)
		}()
	}

	var requestTimer = time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second)
	var cleaningTimer = time.NewTicker(time.Duration(w.cfg.CleaningPeriodSeconds) * time.Second)
	var subsConfirmTimer = time.NewTicker(time.Duration(w.cfg.SubsConfirmationPeriodSeconds) * time.Second)
	var notificationSenderTimer = time.NewTicker(time.Duration(w.cfg.NotificationsReadyPeriodSeconds) * time.Second)
	w.checker.Init(w.checker, lib.CheckerConfig{
		UsersOnlineEndpoints: w.cfg.UsersOnlineEndpoint,
		Clients:              w.clients,
		Headers:              w.cfg.Headers,
		Dbg:                  w.cfg.Debug,
		SpecificConfig:       w.cfg.SpecificConfig,
		QueueSize:            5,
		SiteOnlineModels:     w.siteOnline,
		Subscriptions:        w.subscriptions(),
		IntervalMs:           w.cfg.IntervalMs,
	})
	w.checker.Start()
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT, syscall.SIGTSTP, syscall.SIGCONT)
	w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, true, true, lib.ParseRaw, "bot is up", messagePacket)
	w.initiateOnlineQuery()
	for {
		select {
		case <-requestTimer.C:
			runtime.GC()
			w.periodic()
		case <-cleaningTimer.C:
			w.cleaningDuration = w.cleanStatusChanges(time.Now().Unix())
		case <-subsConfirmTimer.C:
			w.queryUnconfirmedSubs()
		case <-notificationSenderTimer.C:
			w.sendReadyNotifications()
		case onlineModels := <-w.onlineModelsChan:
			if onlineModels.Data != nil {
				w.httpQueriesDuration = onlineModels.Data.Elapsed
				now := int(time.Now().Unix())
				w.images = onlineModels.Data.Images
				changesInPeriod, confirmedChangesInPeriod, notifications, elapsed := w.processStatusUpdates(onlineModels.Data.Updates, now)
				w.updatesDuration = elapsed
				w.changesInPeriod = changesInPeriod
				w.confirmedChangesInPeriod = confirmedChangesInPeriod
				w.storeNotifications(notifications)
				if w.cfg.Debug {
					ldbg("status updates processed in %v", elapsed)
				}
				w.logQuerySuccess(true, onlineModels.Errors)
			} else {
				w.logQuerySuccess(false, onlineModels.Errors)
			}
		case u := <-incoming:
			if w.processTGUpdate(u) {
				if !w.maintenance(signals, incoming) {
					return
				}
			}
		case m := <-mail:
			w.mailReceived(m)
		case s := <-statRequests:
			w.processStatCommand(s.endpoint, s.writer, s.request, s.done)
		case s := <-ipnRequests:
			w.processIPN(s.writer, s.request, s.done)
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
				w.incrementBlock(r.endpoint, r.chatID)
			case messageSent:
				w.resetBlock(r.endpoint, r.chatID)
			}
			query := "insert into interactions (timestamp, chat_id, result, endpoint, priority, delay, kind) values (?,?,?,?,?,?,?)"
			w.mustExec(query, r.timestamp, r.chatID, r.result, r.endpoint, r.priority, r.delay, r.kind)
		case r := <-w.unconfirmedSubsResults:
			w.processSubsConfirmations(r)
		case nots := <-w.sentNotifications:
			for _, n := range nots {
				w.mustExec("delete from notification_queue where id=?", n.id)
				w.mustExec("update users set reports=reports+1 where chat_id=?", n.chatID)
			}
		case r := <-w.downloadResults:
			w.downloadErrors[w.downloadResultsPos] = !r
			w.downloadResultsPos = (w.downloadResultsPos + 1) % w.cfg.errorDenominator
		}
	}
}
