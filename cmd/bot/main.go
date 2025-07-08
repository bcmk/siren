// This is the main executable package
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"image"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"regexp"
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

	_ "golang.org/x/image/webp"

	"github.com/bcmk/siren/internal/botconfig"
	"github.com/bcmk/siren/internal/checkers"
	"github.com/bcmk/siren/internal/db"
	"github.com/bcmk/siren/lib/cmdlib"
	tg "github.com/bcmk/telegram-bot-api"
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

type statRequest struct {
	endpoint string
	writer   http.ResponseWriter
	request  *http.Request
	done     chan bool
}

type worker struct {
	db                       db.Database
	clients                  []*cmdlib.Client
	bots                     map[string]*tg.BotAPI
	cfg                      *botconfig.Config
	httpQueriesDuration      time.Duration
	updatesDuration          time.Duration
	cleaningDuration         time.Duration
	changesInPeriod          int
	confirmedChangesInPeriod int
	ourOnline                map[string]bool
	specialModels            map[string]bool
	siteStatuses             map[string]db.StatusChange
	siteOnline               map[string]bool
	tr                       map[string]*cmdlib.Translations
	tpl                      map[string]*template.Template
	trAds                    map[string]map[string]*cmdlib.Translation
	tplAds                   map[string]*template.Template
	modelIDPreprocessing     func(string) string
	checker                  cmdlib.Checker
	unsuccessfulRequests     []bool
	unsuccessfulRequestsPos  int
	downloadResults          chan bool
	downloadErrors           []bool
	downloadResultsPos       int
	nextErrorReport          time.Time
	images                   map[string]string
	botNames                 map[string]string
	lowPriorityMsg           chan outgoingPacket
	highPriorityMsg          chan outgoingPacket
	outgoingMsgResults       chan msgSendResult
	unconfirmedSubsResults   chan cmdlib.StatusResults
	onlineModelsChan         chan cmdlib.StatusUpdateResults
	sendingNotifications     chan []db.Notification
	sentNotifications        chan []db.Notification
	ourIDs                   []int64
	modelIDRegexp            *regexp.Regexp
}

type incomingPacket struct {
	message  tg.Update
	endpoint string
}

type outgoingPacket struct {
	message   baseChattable
	endpoint  string
	requested time.Time
	kind      db.PacketKind
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
	kind      db.PacketKind
}

type waitingUser struct {
	chatID   int64
	endpoint string
}

func newWorker(args []string) *worker {
	if len(args) != 1 {
		panic("usage: siren <config>")
	}
	cfg := botconfig.ReadConfig(args[0])

	var err error

	var clients []*cmdlib.Client
	for _, address := range cfg.SourceIPAddresses {
		clients = append(clients, cmdlib.HTTPClientWithTimeoutAndAddress(cfg.TimeoutSeconds, address, cfg.EnableCookies))
	}

	telegramClient := cmdlib.HTTPClientWithTimeoutAndAddress(cfg.TelegramTimeoutSeconds, "", false)
	bots := make(map[string]*tg.BotAPI)
	for n, p := range cfg.Endpoints {
		//noinspection GoNilness
		var bot *tg.BotAPI
		bot, err = tg.NewBotAPIWithClient(p.BotToken, tg.APIEndpoint, telegramClient.Client)
		checkErr(err)
		bots[n] = bot
	}
	tr, tpl := cmdlib.LoadAllTranslations(trsByEndpoint(cfg))
	trAds, tplAds := cmdlib.LoadAllAds(trsAdsByEndpoint(cfg))
	for _, t := range tpl {
		template.Must(t.New("affiliate_link").Parse(cfg.AffiliateLink))
	}
	w := &worker{
		bots:                   bots,
		db:                     db.NewDatabase(cfg.DBPath, cfg.CheckGID),
		cfg:                    cfg,
		clients:                clients,
		tr:                     tr,
		tpl:                    tpl,
		trAds:                  trAds,
		tplAds:                 tplAds,
		unsuccessfulRequests:   make([]bool, cfg.ErrorDenominator),
		downloadErrors:         make([]bool, cfg.ErrorDenominator),
		downloadResults:        make(chan bool),
		images:                 map[string]string{},
		botNames:               map[string]string{},
		lowPriorityMsg:         make(chan outgoingPacket, 10000),
		highPriorityMsg:        make(chan outgoingPacket, 10000),
		outgoingMsgResults:     make(chan msgSendResult),
		unconfirmedSubsResults: make(chan cmdlib.StatusResults),
		onlineModelsChan:       make(chan cmdlib.StatusUpdateResults),
		sendingNotifications:   make(chan []db.Notification, 1000),
		sentNotifications:      make(chan []db.Notification),
		ourIDs:                 getOurIDs(cfg),
		specialModels:          map[string]bool{},
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
		w.modelIDPreprocessing = cmdlib.CanonicalModelID
		w.modelIDRegexp = cmdlib.ModelIDRegexp
	case "bongacams":
		w.checker = &checkers.BongaCamsChecker{}
		w.modelIDPreprocessing = cmdlib.CanonicalModelID
		w.modelIDRegexp = cmdlib.ModelIDRegexp
	case "chaturbate":
		w.checker = &checkers.ChaturbateChecker{}
		w.modelIDPreprocessing = checkers.ChaturbateCanonicalModelID
		w.modelIDRegexp = cmdlib.ModelIDRegexp
	case "stripchat":
		w.checker = &checkers.StripchatChecker{}
		w.modelIDPreprocessing = cmdlib.CanonicalModelID
		w.modelIDRegexp = cmdlib.ModelIDRegexp
	case "livejasmin":
		w.checker = &checkers.LiveJasminChecker{}
		w.modelIDPreprocessing = cmdlib.CanonicalModelID
		w.modelIDRegexp = cmdlib.ModelIDRegexp
	case "camsoda":
		w.checker = &checkers.CamSodaChecker{}
		w.modelIDPreprocessing = cmdlib.CanonicalModelID
		w.modelIDRegexp = cmdlib.ModelIDRegexp
	case "flirt4free":
		w.checker = &checkers.Flirt4FreeChecker{}
		w.modelIDPreprocessing = checkers.Flirt4FreeCanonicalModelID
		w.modelIDRegexp = cmdlib.ModelIDRegexp
	case "streamate":
		w.checker = &checkers.StreamateChecker{}
		w.modelIDPreprocessing = cmdlib.CanonicalModelID
		w.modelIDRegexp = cmdlib.ModelIDRegexp
	case "twitch":
		w.checker = &checkers.TwitchChecker{}
		w.modelIDPreprocessing = checkers.TwitchCanonicalModelID
		w.modelIDRegexp = checkers.TwitchModelIDRegexp
	case "cam4":
		w.checker = &checkers.Cam4Checker{}
		w.modelIDPreprocessing = checkers.Cam4CanonicalModelID
		w.modelIDRegexp = checkers.Cam4ModelIDRegexp
	default:
		panic("wrong website")
	}

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
	parse cmdlib.ParseKind,
	text string,
	kind db.PacketKind,
) {
	msg := tg.NewMessage(chatID, text)
	msg.DisableNotification = !notify
	msg.DisableWebPagePreview = disablePreview
	switch parse {
	case cmdlib.ParseHTML, cmdlib.ParseMarkdown:
		msg.ParseMode = parse.String()
	}
	w.enqueueMessage(queue, endpoint, &messageConfig{msg}, kind)
}

func (w *worker) sendImage(
	queue chan outgoingPacket,
	endpoint string,
	chatID int64,
	notify bool,
	parse cmdlib.ParseKind,
	text string,
	image []byte,
	kind db.PacketKind,
) {
	fileBytes := tg.FileBytes{Name: "preview", Bytes: image}
	msg := tg.NewPhotoUpload(chatID, fileBytes)
	msg.Caption = text
	msg.DisableNotification = !notify
	switch parse {
	case cmdlib.ParseHTML, cmdlib.ParseMarkdown:
		msg.ParseMode = parse.String()
	}
	w.enqueueMessage(queue, endpoint, &photoConfig{msg}, kind)
}

func (w *worker) enqueueMessage(queue chan outgoingPacket, endpoint string, msg baseChattable, kind db.PacketKind) {
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
	translation *cmdlib.Translation,
	data map[string]interface{},
	kind db.PacketKind,
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
	translation *cmdlib.Translation,
	data map[string]interface{},
) {
	tpl := w.tplAds[endpoint]
	text := templateToString(tpl, translation.Key, data)
	if translation.Image == "" {
		w.sendText(queue, endpoint, chatID, notify, translation.DisablePreview, translation.Parse, text, db.AdPacket)
	} else {
		w.sendImage(queue, endpoint, chatID, notify, translation.Parse, text, translation.ImageBytes, db.AdPacket)
	}
}

func (w *worker) sendTrImage(
	queue chan outgoingPacket,
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
	w.sendImage(queue, endpoint, chatID, notify, translation.Parse, text, image, kind)
}

func (w *worker) createDatabase(done chan bool) {
	linf("creating database if needed...")
	for _, prelude := range w.cfg.SQLPrelude {
		w.db.MustExec(prelude)
	}
	w.db.MustExec(`create table if not exists schema_version (version integer);`)
	w.db.ApplyMigrations()
	done <- true
}

func (w *worker) initCache() {
	start := time.Now()
	w.siteStatuses = w.db.QueryLastStatusChanges()
	w.siteOnline = w.getLastOnlineModels()
	w.ourOnline = w.db.QueryConfirmedModels()
	if w.cfg.SpecialModels {
		w.specialModels = w.db.QuerySpecialModels()
	}
	elapsed := time.Since(start)
	linf("cache initialized in %d ms", elapsed.Milliseconds())
}

func (w *worker) getLastOnlineModels() map[string]bool {
	res := map[string]bool{}
	for k, v := range w.siteStatuses {
		if v.Status == cmdlib.StatusOnline {
			res[k] = true
		}
	}
	return res
}

func (w *worker) confirmationSeconds(status cmdlib.StatusKind) int {
	switch status {
	case cmdlib.StatusOnline:
		return w.cfg.StatusConfirmationSeconds.Online
	case cmdlib.StatusOffline:
		return w.cfg.StatusConfirmationSeconds.Offline
	case cmdlib.StatusDenied:
		return w.cfg.StatusConfirmationSeconds.Denied
	case cmdlib.StatusNotFound:
		return w.cfg.StatusConfirmationSeconds.NotFound
	default:
		return 0
	}
}

func (w *worker) changedStatuses(newStatuses []cmdlib.StatusUpdate, now int) []db.StatusChange {
	result := []db.StatusChange{}
	for _, next := range newStatuses {
		prev := w.siteStatuses[next.ModelID]
		if next.Status != prev.Status {
			result = append(result, db.StatusChange{ModelID: next.ModelID, Status: next.Status, Timestamp: now})
		}
	}
	return result
}

func (w *worker) updateCachedStatus(changedStatuses []db.StatusChange) {
	for _, statusChange := range changedStatuses {
		w.siteStatuses[statusChange.ModelID] = statusChange
		if statusChange.Status == cmdlib.StatusOnline {
			w.siteOnline[statusChange.ModelID] = true
		} else {
			delete(w.siteOnline, statusChange.ModelID)
		}
	}
}

func (w *worker) confirmStatusChanges(now int) []db.StatusChange {
	unmatchedStatusesStreamIDs := cmdlib.HashDiffAll(w.ourOnline, w.siteOnline)
	var result []db.StatusChange
	for _, modelID := range unmatchedStatusesStreamIDs {
		statusChange := w.siteStatuses[modelID]
		confirmationSeconds := w.confirmationSeconds(statusChange.Status)
		durationConfirmed := confirmationSeconds == 0 || statusChange.Timestamp == 0 || (now-statusChange.Timestamp >= confirmationSeconds)
		if durationConfirmed {
			if statusChange.Status == cmdlib.StatusOnline {
				w.ourOnline[modelID] = true
			} else {
				delete(w.ourOnline, modelID)
			}
			result = append(result, db.StatusChange{ModelID: modelID, Status: statusChange.Status, Timestamp: now})
		}
	}
	return result
}

func (w *worker) notifyOfAddResults(queue chan outgoingPacket, notifications []db.Notification) {
	for _, n := range notifications {
		data := tplData{"model": n.ModelID}
		if n.Status&(cmdlib.StatusOnline|cmdlib.StatusOffline|cmdlib.StatusDenied) != 0 {
			w.sendTr(queue, n.Endpoint, n.ChatID, false, w.tr[n.Endpoint].ModelAdded, data, db.ReplyPacket)
		} else {
			w.sendTr(queue, n.Endpoint, n.ChatID, false, w.tr[n.Endpoint].AddError, data, db.ReplyPacket)
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

func (w *worker) notifyOfStatuses(highPriorityQueue chan outgoingPacket, lowPriorityQueue chan outgoingPacket, notifications []db.Notification) {
	images := map[string][]byte{}
	if w.cfg.ShowImages {
		images = w.downloadImages(notifications)
	}
	for _, n := range notifications {
		queue := lowPriorityQueue
		if n.Priority > 0 {
			queue = highPriorityQueue
		}
		w.notifyOfStatus(queue, n, images[n.ImageURL], n.Social)
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

func (w *worker) ad(queue chan outgoingPacket, endpoint string, chatID int64) {
	trAds := w.trAdsSlice(endpoint)
	if len(trAds) == 0 {
		return
	}
	adNum := rand.Intn(len(trAds))
	w.sendAdsTr(queue, endpoint, chatID, false, trAds[adNum], nil)
}

func (w *worker) notifyOfStatus(queue chan outgoingPacket, n db.Notification, image []byte, social bool) {
	if w.cfg.Debug {
		ldbg("notifying of status of the model %s", n.ModelID)
	}
	var timeDiff *timeDiff
	if n.TimeDiff != nil {
		temp := calcTimeDiff(*n.TimeDiff)
		timeDiff = &temp
	}
	data := tplData{"model": n.ModelID, "time_diff": timeDiff}
	switch n.Status {
	case cmdlib.StatusOnline:
		if image == nil {
			w.sendTr(queue, n.Endpoint, n.ChatID, true, w.tr[n.Endpoint].Online, data, n.Kind)
		} else {
			w.sendTrImage(queue, n.Endpoint, n.ChatID, true, w.tr[n.Endpoint].Online, data, image, n.Kind)
		}
	case cmdlib.StatusOffline:
		w.sendTr(queue, n.Endpoint, n.ChatID, false, w.tr[n.Endpoint].Offline, data, n.Kind)
	case cmdlib.StatusDenied:
		w.sendTr(queue, n.Endpoint, n.ChatID, false, w.tr[n.Endpoint].Denied, data, n.Kind)
	}
	if social && rand.Intn(5) == 0 {
		w.ad(queue, n.Endpoint, n.ChatID)
	}
}

func (w *worker) mustUser(chatID int64) (user db.User) {
	user, found := w.db.User(chatID)
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
	models := w.db.ModelsForChat(endpoint, chatID)
	for _, m := range models {
		w.showWeekForModel(endpoint, chatID, m)
	}
	if len(models) == 0 {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ZeroSubscriptions, nil, db.ReplyPacket)
	}

}

func (w *worker) showWeekForModel(endpoint string, chatID int64, modelID string) {
	modelID = w.modelIDPreprocessing(modelID)
	if !w.modelIDRegexp.MatchString(modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"model": modelID}, db.ReplyPacket)
		return
	}
	hours, start := w.week(modelID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Week, tplData{
		"hours":   hours,
		"weekday": int(start.UTC().Weekday()),
		"model":   modelID,
	}, db.ReplyPacket)
}

func (w *worker) addModel(endpoint string, chatID int64, modelID string, now int) bool {
	if modelID == "" {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].SyntaxAdd, nil, db.ReplyPacket)
		return false
	}
	modelID = w.modelIDPreprocessing(modelID)
	if !w.modelIDRegexp.MatchString(modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"model": modelID}, db.ReplyPacket)
		return false
	}

	if w.db.SubscriptionExists(endpoint, chatID, modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].AlreadyAdded, tplData{"model": modelID}, db.ReplyPacket)
		return false
	}
	subscriptionsNumber := w.db.SubscriptionsNumber(endpoint, chatID)
	user := w.mustUser(chatID)
	if subscriptionsNumber >= user.MaxModels {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].NotEnoughSubscriptions, nil, db.ReplyPacket)
		w.subscriptionUsage(endpoint, chatID, true)
		return false
	}
	var confirmedStatus cmdlib.StatusKind
	if w.ourOnline[modelID] {
		confirmedStatus = cmdlib.StatusOnline
	} else if _, ok := w.siteStatuses[modelID]; ok {
		confirmedStatus = cmdlib.StatusOffline
	} else if w.db.MaybeModel(modelID) != nil {
		confirmedStatus = cmdlib.StatusOffline
	} else {
		w.db.MustExec("insert into signals (chat_id, model_id, endpoint, confirmed) values ($1, $2, $3, $4)", chatID, modelID, endpoint, 0)
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].CheckingModel, nil, db.ReplyPacket)
		return false
	}
	w.db.MustExec("insert into signals (chat_id, model_id, endpoint, confirmed) values ($1, $2, $3, $4)", chatID, modelID, endpoint, 1)
	w.db.MustExec("insert into models (model_id, status) values ($1, $2) on conflict(model_id) do nothing", modelID, confirmedStatus)
	subscriptionsNumber++
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ModelAdded, tplData{"model": modelID}, db.ReplyPacket)
	nots := []db.Notification{{
		Endpoint: endpoint,
		ChatID:   chatID,
		ModelID:  modelID,
		Status:   confirmedStatus,
		TimeDiff: w.modelDuration(modelID, now),
		Social:   false,
		Priority: 1,
		Kind:     db.ReplyPacket}}
	if subscriptionsNumber >= user.MaxModels-w.cfg.HeavyUserRemainder {
		w.subscriptionUsage(endpoint, chatID, true)
	}
	w.db.StoreNotifications(nots)
	return true
}

func (w *worker) subscriptionUsage(endpoint string, chatID int64, ad bool) {
	subscriptionsNumber := w.db.SubscriptionsNumber(endpoint, chatID)
	user := w.mustUser(chatID)
	tr := w.tr[endpoint].SubscriptionUsage
	if ad {
		tr = w.tr[endpoint].SubscriptionUsageAd
	}
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, tr,
		tplData{
			"subscriptions_used":  subscriptionsNumber,
			"total_subscriptions": user.MaxModels,
		},
		db.ReplyPacket)
}

func (w *worker) wantMore(endpoint string, chatID int64) {
	w.showReferral(endpoint, chatID)
}

func (w *worker) settings(endpoint string, chatID int64) {
	subscriptionsNumber := w.db.SubscriptionsNumber(endpoint, chatID)
	user := w.mustUser(chatID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Settings, tplData{
		"subscriptions_used":              subscriptionsNumber,
		"total_subscriptions":             user.MaxModels,
		"show_images":                     user.ShowImages,
		"offline_notifications_supported": w.cfg.OfflineNotifications,
		"offline_notifications":           user.OfflineNotifications,
	}, db.ReplyPacket)
}

func (w *worker) enableImages(endpoint string, chatID int64, showImages bool) {
	w.db.MustExec("update users set show_images = $1 where chat_id = $2", showImages, chatID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].OK, nil, db.ReplyPacket)
}

func (w *worker) enableOfflineNotifications(endpoint string, chatID int64, offlineNotifications bool) {
	w.db.MustExec("update users set offline_notifications = $1 where chat_id = $2", offlineNotifications, chatID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].OK, nil, db.ReplyPacket)
}

func (w *worker) removeModel(endpoint string, chatID int64, modelID string) {
	if modelID == "" {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].SyntaxRemove, nil, db.ReplyPacket)
		return
	}
	modelID = w.modelIDPreprocessing(modelID)
	if !w.modelIDRegexp.MatchString(modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"model": modelID}, db.ReplyPacket)
		return
	}
	if !w.db.SubscriptionExists(endpoint, chatID, modelID) {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ModelNotInList, tplData{"model": modelID}, db.ReplyPacket)
		return
	}
	w.db.MustExec("delete from signals where chat_id = $1 and model_id = $2 and endpoint = $3", chatID, modelID, endpoint)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ModelRemoved, tplData{"model": modelID}, db.ReplyPacket)
}

func (w *worker) sureRemoveAll(endpoint string, chatID int64) {
	w.db.MustExec("delete from signals where chat_id = $1 and endpoint = $2", chatID, endpoint)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].AllModelsRemoved, nil, db.ReplyPacket)
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

func chunkModels(xs []db.Model, chunkSize int) [][]db.Model {
	if len(xs) == 0 {
		return nil
	}
	divided := make([][]db.Model, (len(xs)+chunkSize-1)/chunkSize)
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

func listModelsSortWeight(s cmdlib.StatusKind) int {
	switch s {
	case cmdlib.StatusOnline:
		return 0
	case cmdlib.StatusDenied:
		return 2
	default:
		return 1
	}
}

func (w *worker) listModels(endpoint string, chatID int64, now int) {
	type data struct {
		Model    string
		TimeDiff *timeDiff
	}
	statuses := w.db.StatusesForChat(endpoint, chatID)
	sort.SliceStable(statuses, func(i, j int) bool {
		return listModelsSortWeight(statuses[i].Status) < listModelsSortWeight(statuses[j].Status)
	})
	chunks := chunkModels(statuses, 50)
	for _, chunk := range chunks {
		var online, offline, denied []data
		for _, s := range chunk {
			data := data{
				Model:    s.ModelID,
				TimeDiff: w.modelTimeDiff(s.ModelID, now),
			}
			switch s.Status {
			case cmdlib.StatusOnline:
				online = append(online, data)
			case cmdlib.StatusDenied:
				denied = append(denied, data)
			default:
				offline = append(offline, data)
			}
		}
		tplData := tplData{"online": online, "offline": offline, "denied": denied}
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].List, tplData, db.ReplyPacket)
	}
}

func (w *worker) modelDuration(modelID string, now int) *int {
	begin, end, prevStatus := w.db.LastSeenInfo(modelID)
	if end != 0 {
		timeDiff := now - end
		return &timeDiff
	}
	if begin != 0 && prevStatus != cmdlib.StatusUnknown {
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
		lerr("cannot download image, %v", err)
	}
	w.downloadSuccess(err == nil)
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

func (w *worker) listOnlineModels(endpoint string, chatID int64, now int) {
	statuses := w.db.StatusesForChat(endpoint, chatID)
	var online []db.Model
	for _, s := range statuses {
		if s.Status == cmdlib.StatusOnline {
			online = append(online, s)
		}
	}
	if len(online) > w.cfg.MaxSubscriptionsForPics && chatID < -1 {
		data := tplData{"max_subs": w.cfg.MaxSubscriptionsForPics}
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].TooManySubscriptionsForPics, data, db.ReplyPacket)
		return
	}
	if len(online) == 0 {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].NoOnlineModels, nil, db.ReplyPacket)
		return
	}
	var nots []db.Notification
	for _, s := range online {
		not := db.Notification{
			Priority: 1,
			Endpoint: endpoint,
			ChatID:   chatID,
			ModelID:  s.ModelID,
			Status:   cmdlib.StatusOnline,
			ImageURL: w.images[s.ModelID],
			TimeDiff: w.modelDuration(s.ModelID, now),
			Kind:     db.ReplyPacket,
		}
		nots = append(nots, not)
	}
	w.db.StoreNotifications(nots)
}

func (w *worker) week(modelID string) ([]bool, time.Time) {
	now := time.Now()
	nowTimestamp := int(now.Unix())
	today := now.Truncate(24 * time.Hour)
	start := today.Add(-6 * 24 * time.Hour)
	weekTimestamp := int(start.Unix())
	changes := w.db.ChangesFromTo(modelID, weekTimestamp, nowTimestamp)
	hours := make([]bool, (nowTimestamp-weekTimestamp+3599)/3600)
	for i, c := range changes[:len(changes)-1] {
		if c.Status == cmdlib.StatusOnline {
			begin := (c.Timestamp - weekTimestamp) / 3600
			if begin < 0 {
				begin = 0
			}
			end := (changes[i+1].Timestamp - weekTimestamp + 3599) / 3600
			for j := begin; j < end; j++ {
				hours[j] = true
			}
		}
	}
	return hours, start
}

func (w *worker) feedback(endpoint string, chatID int64, text string, now int) {
	if text == "" {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].SyntaxFeedback, nil, db.ReplyPacket)
		return
	}
	w.db.MustExec("insert into feedback (endpoint, chat_id, text, timestamp) values ($1, $2, $3, $4)", endpoint, chatID, text, now)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Feedback, nil, db.ReplyPacket)
	user := w.mustUser(chatID)
	if !user.Blacklist {
		finalText := fmt.Sprintf("Feedback from %d: %s", chatID, text)
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, true, true, cmdlib.ParseRaw, finalText, db.ReplyPacket)
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
		fmt.Sprintf("Reports: %d", stat.ReportsCount),
		fmt.Sprintf("User referrals: %d", stat.UserReferralsCount),
		fmt.Sprintf("Model referrals: %d", stat.ModelReferralsCount),
		fmt.Sprintf("Changes in period: %d", stat.ChangesInPeriod),
		fmt.Sprintf("Confirmed changes in period: %d", stat.ConfirmedChangesInPeriod),
	}
}

func (w *worker) stat(endpoint string) {
	text := strings.Join(w.statStrings(endpoint), "\n")
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, true, true, cmdlib.ParseRaw, text, db.ReplyPacket)
}

func (w *worker) performanceStat(endpoint string, arguments string) {
	parts := strings.Split(arguments, " ")
	if len(parts) > 2 {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "wrong number of arguments", db.ReplyPacket)
		return
	}
	n := int64(10)
	if len(parts) == 2 {
		var err error
		n, err = strconv.ParseInt(parts[1], 10, 32)
		if err != nil {
			w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "cannot parse arguments", db.ReplyPacket)
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
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseHTML, entry, db.ReplyPacket)
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
		w.sendText(w.lowPriorityMsg, endpoint, chatID, true, false, cmdlib.ParseRaw, text, db.MessagePacket)
	}
	w.sendText(w.lowPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
}

func (w *worker) direct(endpoint string, arguments string) {
	parts := strings.SplitN(arguments, " ", 2)
	if len(parts) < 2 {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "usage: /direct chatID text", db.ReplyPacket)
		return
	}
	whom, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "first argument is invalid", db.ReplyPacket)
		return
	}
	text := parts[1]
	if text == "" {
		return
	}
	w.sendText(w.highPriorityMsg, endpoint, whom, true, false, cmdlib.ParseRaw, text, db.MessagePacket)
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
}

func (w *worker) blacklist(endpoint string, arguments string) {
	whom, err := strconv.ParseInt(arguments, 10, 64)
	if err != nil {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "first argument is invalid", db.ReplyPacket)
		return
	}
	w.db.MustExec("update users set blacklist=1 where chat_id = $1", whom)
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
}

func (w *worker) addSpecialModel(endpoint string, arguments string) {
	parts := strings.Split(arguments, " ")
	if len(parts) != 2 || (parts[0] != "set" && parts[0] != "unset") {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "usage: /special set/unset MODEL_ID", db.ReplyPacket)
		return
	}
	modelID := w.modelIDPreprocessing(parts[1])
	if !w.modelIDRegexp.MatchString(modelID) {
		w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "MODEL_ID is invalid", db.ReplyPacket)
		return
	}
	set := parts[0] == "set"
	w.db.MustExec(`
		insert into models (model_id, special) values ($1, $2)
		on conflict(model_id) do update set special=excluded.special`,
		modelID,
		set)
	if w.cfg.SpecialModels {
		if set {
			w.specialModels[modelID] = true
		} else {
			delete(w.specialModels, modelID)
		}
	}
	w.sendText(w.highPriorityMsg, endpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
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

func (w *worker) processAdminMessage(endpoint string, chatID int64, command, arguments string) (processed bool, maintenance bool) {
	switch command {
	case "stat":
		w.stat(endpoint)
		return true, false
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
	case "special":
		w.addSpecialModel(endpoint, arguments)
		return true, false
	case "set_max_models":
		parts := strings.Fields(arguments)
		if len(parts) != 2 {
			w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, cmdlib.ParseRaw, "expecting two arguments", db.ReplyPacket)
			return true, false
		}
		who, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, cmdlib.ParseRaw, "first argument is invalid", db.ReplyPacket)
			return true, false
		}
		maxModels, err := strconv.Atoi(parts[1])
		if err != nil {
			w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, cmdlib.ParseRaw, "second argument is invalid", db.ReplyPacket)
			return true, false
		}
		w.db.SetLimit(who, maxModels)
		w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
		return true, false
	case "maintenance":
		w.sendText(w.highPriorityMsg, endpoint, chatID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
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

func (w *worker) refer(followerChatID int64, referrer string) (applied appliedKind) {
	referrerChatID := w.db.ChatForReferralID(referrer)
	if referrerChatID == nil {
		return invalidReferral
	}
	if _, exists := w.db.User(followerChatID); exists {
		return followerExists
	}
	w.db.MustExec("insert into users (chat_id, max_models) values ($1, $2)", followerChatID, w.cfg.MaxModels+w.cfg.FollowerBonus)
	w.db.MustExec(`
		insert into users as included (chat_id, max_models) values ($1, $2)
		on conflict(chat_id) do update set max_models=included.max_models + $3`,
		*referrerChatID,
		w.cfg.MaxModels+w.cfg.ReferralBonus,
		w.cfg.ReferralBonus)
	w.db.MustExec("update referrals set referred_users=referred_users+1 where chat_id = $1", referrerChatID)
	return referralApplied
}

func (w *worker) showReferral(endpoint string, chatID int64) {
	referralID := w.db.ReferralID(chatID)
	if referralID == nil {
		temp := w.newRandReferralID()
		referralID = &temp
		w.db.MustExec("insert into referrals (chat_id, referral_id) values ($1, $2)", chatID, *referralID)
	}
	referralLink := fmt.Sprintf("https://t.me/%s?start=%s", w.botNames[endpoint], *referralID)
	subscriptionsNumber := w.db.SubscriptionsNumber(endpoint, chatID)
	user := w.mustUser(chatID)
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ReferralLink, tplData{
		"link":                referralLink,
		"referral_bonus":      w.cfg.ReferralBonus,
		"follower_bonus":      w.cfg.FollowerBonus,
		"subscriptions_used":  subscriptionsNumber,
		"total_subscriptions": user.MaxModels,
	}, db.ReplyPacket)
}

func (w *worker) start(endpoint string, chatID int64, referrer string, now int) {
	modelID := ""
	switch {
	case strings.HasPrefix(referrer, "m-"):
		modelID = referrer[2:]
		modelID = w.modelIDPreprocessing(modelID)
		referrer = ""
	case referrer != "":
		referralID := w.db.ReferralID(chatID)
		if referralID != nil && *referralID == referrer {
			w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].OwnReferralLinkHit, nil, db.ReplyPacket)
			return
		}
	}
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Start, tplData{
		"website_link": w.cfg.WebsiteLink,
	}, db.ReplyPacket)
	if chatID > 0 && referrer != "" {
		applied := w.refer(chatID, referrer)
		switch applied {
		case referralApplied:
			w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].ReferralApplied, nil, db.ReplyPacket)
		case invalidReferral:
			w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].InvalidReferralLink, nil, db.ReplyPacket)
		case followerExists:
			w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].FollowerExists, nil, db.ReplyPacket)
		}
	}
	w.db.AddUser(chatID, w.cfg.MaxModels)
	if modelID != "" {
		if w.addModel(endpoint, chatID, modelID, now) {
			w.db.MustExec("update models set referred_users=referred_users+1 where model_id = $1", modelID)
		}
	}
}

func (w *worker) help(endpoint string, chatID int64) {
	w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Help, tplData{
		"website_link": w.cfg.WebsiteLink,
	}, db.ReplyPacket)
}

func (w *worker) processIncomingCommand(endpoint string, chatID int64, command, arguments string, now int) bool {
	w.db.ResetBlock(endpoint, chatID)
	command = strings.ToLower(command)
	if command != "start" {
		w.db.AddUser(chatID, w.cfg.MaxModels)
	}
	linf("chat: %d, command: %s %s", chatID, command, arguments)

	if chatID == w.cfg.AdminID {
		if proc, maintenance := w.processAdminMessage(endpoint, chatID, command, arguments); proc {
			return maintenance
		}
	}

	unknown := func() {
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].UnknownCommand, nil, db.ReplyPacket)
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
	case "start":
		w.start(endpoint, chatID, arguments, now)
	case "help":
		w.help(endpoint, chatID)
	case "ad":
		w.ad(w.highPriorityMsg, endpoint, chatID)
	case "faq":
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].FAQ, tplData{
			"max_models": w.cfg.MaxModels,
		}, db.ReplyPacket)
	case "feedback":
		w.feedback(endpoint, chatID, arguments, now)
	case "social":
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].Social, nil, db.ReplyPacket)
	case "version":
		w.sendTr(
			w.highPriorityMsg,
			endpoint,
			chatID,
			false,
			w.tr[endpoint].Version,
			tplData{"version": cmdlib.Version},
			db.ReplyPacket)
	case "remove_all", "stop":
		w.sendTr(w.highPriorityMsg, endpoint, chatID, false, w.tr[endpoint].RemoveAll, nil, db.ReplyPacket)
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

func (w *worker) subscriptions() map[string]cmdlib.StatusKind {
	subs := w.db.MustStrings("select distinct(model_id) from signals where confirmed = 1")
	result := map[string]cmdlib.StatusKind{}
	for _, s := range subs {
		result[s] = w.siteStatuses[s].Status
	}
	return result
}

func (w *worker) periodic() {
	unsuccessfulRequestsCount := w.unsuccessfulRequestsCount()
	now := time.Now()
	if w.nextErrorReport.Before(now) && unsuccessfulRequestsCount > w.cfg.ErrorThreshold {
		text := fmt.Sprintf("Dangerous error rate reached: %d/%d", unsuccessfulRequestsCount, w.cfg.ErrorDenominator)
		w.sendText(
			w.highPriorityMsg,
			w.cfg.AdminEndpoint,
			w.cfg.AdminID,
			true,
			true,
			cmdlib.ParseRaw,
			text,
			db.MessagePacket)
		w.nextErrorReport = now.Add(time.Minute * time.Duration(w.cfg.ErrorReportingPeriodMinutes))
	}
	w.pushOnlineRequest()
}

func (w *worker) pushOnlineRequest() {
	err := w.checker.Updater().PushUpdateRequest(cmdlib.StatusUpdateRequest{
		Callback:      func(res cmdlib.StatusUpdateResults) { w.onlineModelsChan <- res },
		SpecialModels: w.specialModels,
		Subscriptions: w.subscriptions(),
	})
	if err != nil {
		lerr("%v", err)
	}
}

func (w *worker) pushSpecificRequest(resultsCh chan cmdlib.StatusResults, specific map[string]bool) error {
	err := w.checker.PushStatusRequest(cmdlib.StatusRequest{
		Callback:  func(res cmdlib.StatusResults) { resultsCh <- res },
		Specific:  specific,
		CheckMode: cmdlib.CheckStatuses})
	if err != nil {
		lerr("%v", err)
	}
	return err
}

func (w *worker) processStatusUpdates(updates []cmdlib.StatusUpdate, now int) (
	changesCount int,
	confirmedChangesCount int,
	notifications []db.Notification,
	elapsed time.Duration,
) {
	start := time.Now()
	usersForModels, endpointsForModels := w.db.UsersForModels()

	changesCount = len(updates)

	changedStatuses := w.changedStatuses(updates, now)
	w.db.InsertStatusChanges(changedStatuses)
	w.updateCachedStatus(changedStatuses)

	confirmedStatusChanges := w.confirmStatusChanges(now)
	w.db.InsertConfirmedStatusChanges(confirmedStatusChanges)

	if w.cfg.Debug {
		ldbg("confirmed online models: %d", len(w.ourOnline))
	}

	for _, c := range confirmedStatusChanges {
		users := usersForModels[c.ModelID]
		endpoints := endpointsForModels[c.ModelID]
		for i, user := range users {
			status := w.siteStatuses[c.ModelID].Status
			if (w.cfg.OfflineNotifications && user.OfflineNotifications) || status != cmdlib.StatusOffline {
				n := db.Notification{
					Endpoint: endpoints[i],
					ChatID:   user.ChatID,
					ModelID:  c.ModelID,
					Status:   status,
					Social:   user.ChatID > 0,
					Sound:    status == cmdlib.StatusOnline,
					Kind:     db.NotificationPacket}
				if user.ShowImages {
					n.ImageURL = w.images[c.ModelID]
				}
				notifications = append(notifications, n)
			}
		}
	}

	confirmedChangesCount = len(confirmedStatusChanges)

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
	buf, err := os.ReadFile("/proc/self/statm")
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
	measureDone := w.db.Measure("db: retrieving stats")
	defer measureDone()
	rss, _ := getRss()
	var rusage syscall.Rusage
	checkErr(syscall.Getrusage(syscall.RUSAGE_SELF, &rusage))

	return statistics{
		UsersCount:                   w.db.UsersCount(endpoint),
		GroupsCount:                  w.db.GroupsCount(endpoint),
		ActiveUsersOnEndpointCount:   w.db.ActiveUsersOnEndpointCount(endpoint),
		ActiveUsersTotalCount:        w.db.ActiveUsersTotalCount(),
		HeavyUsersCount:              w.db.HeavyUsersCount(endpoint, w.cfg.MaxModels, w.cfg.HeavyUserRemainder),
		ModelsCount:                  w.db.ModelsCount(endpoint),
		ModelsToPollOnEndpointCount:  w.db.ModelsToPollOnEndpointCount(endpoint, w.cfg.BlockThreshold),
		ModelsToPollTotalCount:       w.db.ModelsToPollTotalCount(w.cfg.BlockThreshold),
		OnlineModelsCount:            len(w.ourOnline),
		KnownModelsCount:             len(w.siteStatuses),
		SpecialModelsCount:           len(w.specialModels),
		StatusChangesCount:           w.db.StatusChangesCount(),
		QueriesDurationMilliseconds:  int(w.httpQueriesDuration.Milliseconds()),
		UpdatesDurationMilliseconds:  int(w.updatesDuration.Milliseconds()),
		CleaningDurationMilliseconds: int(w.cleaningDuration.Milliseconds()),
		ErrorRate:                    [2]int{w.unsuccessfulRequestsCount(), w.cfg.ErrorDenominator},
		DownloadErrorRate:            [2]int{w.downloadErrorsCount(), w.cfg.ErrorDenominator},
		Rss:                          rss / 1024,
		MaxRss:                       rusage.Maxrss,
		UserReferralsCount:           w.db.UserReferralsCount(),
		ModelReferralsCount:          w.db.ModelReferralsCount(),
		ReportsCount:                 w.db.Reports(),
		ChangesInPeriod:              w.changesInPeriod,
		ConfirmedChangesInPeriod:     w.confirmedChangesInPeriod,
		Interactions:                 w.db.InteractionsByResultToday(endpoint),
		InteractionsByKind:           w.db.InteractionsByKindToday(endpoint),
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

	statJSON, err := json.MarshalIndent(w.getStat(endpoint), "", "    ")
	checkErr(err)
	_, err = writer.Write(statJSON)
	if err != nil {
		lerr("error on processing stat command, %v", err)
	}
}

func (w *worker) handleStatEndpoints(statRequests chan statRequest) {
	for n, p := range w.cfg.Endpoints {
		if p.StatPath != "" {
			http.HandleFunc(p.WebhookDomain+p.StatPath, w.handleStat(n, statRequests))
		}
	}
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

func getOurIDs(c *botconfig.Config) []int64 {
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

func (w *worker) logQueryErrors(errors int) {
	for i := 0; i < errors; i++ {
		w.logSingleQueryResult(false)
	}
	if errors == 0 {
		w.logSingleQueryResult(true)
	}
}

func (w *worker) logSingleQueryResult(success bool) {
	w.unsuccessfulRequests[w.unsuccessfulRequestsPos] = !success
	w.unsuccessfulRequestsPos = (w.unsuccessfulRequestsPos + 1) % w.cfg.ErrorDenominator
}

func (w *worker) cleanStatusChanges(now int64) time.Duration {
	start := time.Now()
	threshold := int(now) - w.cfg.KeepStatusesForDays*24*60*60
	if w.cfg.MaxCleanSeconds != 0 {
		limit := w.db.MustInt("select coalesce(min(timestamp), 0) from status_changes") + w.cfg.MaxCleanSeconds
		if limit < threshold {
			threshold = limit
		}
	}
	w.db.MustExec("delete from status_changes where timestamp < $1", threshold)
	for k, v := range w.siteStatuses {
		if v.Timestamp < threshold {
			delete(w.siteStatuses, k)
			delete(w.siteOnline, k)
		}
	}
	return time.Since(start)
}

func (w *worker) adminSQL(query string) time.Duration {
	start := time.Now()
	var result string
	if w.db.MaybeRecord(query, nil, db.ScanTo{&result}) {
		w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, result, db.ReplyPacket)
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
				w.sendText(w.highPriorityMsg, u.endpoint, chatID, false, true, cmdlib.ParseRaw, w.cfg.Endpoints[u.endpoint].MaintenanceResponse, db.ReplyPacket)
				linf("ignoring command %s %s", command, args)
			}
		case <-done:
			for user := range waitingUsers {
				w.sendTr(w.highPriorityMsg, user.endpoint, user.chatID, false, w.tr[user.endpoint].WeAreUp, nil, db.MessagePacket)
			}
			return
		case <-w.outgoingMsgResults:
		}
	}
}

func (w *worker) sendReadyNotifications() { w.sendingNotifications <- w.db.NewNotifications() }

func (w *worker) sendNotificationsDaemon() {
	for nots := range w.sendingNotifications {
		w.notifyOfStatuses(w.highPriorityMsg, w.lowPriorityMsg, nots)
		w.sentNotifications <- nots
	}
}

func (w *worker) queryUnconfirmedSubs() {
	unconfirmed := map[string]bool{}
	var modelID string
	w.db.MustQuery("select model_id from signals where confirmed = 0", nil, db.ScanTo{&modelID}, func() { unconfirmed[modelID] = true })
	if len(unconfirmed) > 0 {
		w.db.MustExec("update signals set confirmed = 2 where confirmed = 0")
		ldbg("queueing unconfirmed subscriptions check for %d channels", len(unconfirmed))
		if w.pushSpecificRequest(w.unconfirmedSubsResults, unconfirmed) != nil {
			w.db.MustExec("update signals set confirmed = 0 where confirmed = 2")
		}
	}
}

func (w *worker) processSubsConfirmations(res cmdlib.StatusResults) {
	statusesNumber := 0
	if res.Data != nil {
		statusesNumber = len(res.Data.Statuses)
	}
	ldbg("processing subscription confirmations for %d channels", statusesNumber)
	confirmationsInWork := map[string][]db.Subscription{}
	var iter db.Subscription
	w.db.MustQuery(
		"select endpoint, model_id, chat_id from signals where confirmed = 2",
		nil,
		db.ScanTo{&iter.Endpoint, &iter.ModelID, &iter.ChatID},
		func() { confirmationsInWork[iter.ModelID] = append(confirmationsInWork[iter.ModelID], iter) })
	var nots []db.Notification
	if res.Data != nil {
		for modelID, status := range res.Data.Statuses {
			for _, sub := range confirmationsInWork[modelID] {
				if status&(cmdlib.StatusOnline|cmdlib.StatusOffline|cmdlib.StatusDenied) != 0 {
					w.db.ConfirmSub(sub)
				} else {
					w.db.DenySub(sub)
				}
				n := db.Notification{
					Endpoint: sub.Endpoint,
					ChatID:   sub.ChatID,
					ModelID:  modelID,
					Status:   status,
					Social:   false,
					Priority: 1,
					Kind:     db.ReplyPacket,
				}
				nots = append(nots, n)
			}
		}
	} else {
		lerr("confirmations query failed")
	}
	w.notifyOfAddResults(w.highPriorityMsg, nots)
	w.db.StoreNotifications(nots)
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
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "still processing", db.ReplyPacket)
					} else {
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
						for user := range users {
							w.sendTr(w.highPriorityMsg, user.endpoint, chatID, false, w.tr[user.endpoint].WeAreUp, nil, db.MessagePacket)
						}
						return true
					}
				case "clean":
					if processing {
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "still processing", db.ReplyPacket)
					} else {
						processing = true
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
						go func() {
							processingDone <- w.cleanStatusChanges(time.Now().Unix())
						}()
					}
				case "sql":
					if processing {
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "still processing", db.ReplyPacket)
					} else {
						processing = true
						w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, "OK", db.ReplyPacket)
						go func() {
							processingDone <- w.adminSQL(args)
						}()
					}
				case "":
				default:
					w.sendText(w.highPriorityMsg, u.endpoint, chatID, false, true, cmdlib.ParseRaw, w.cfg.Endpoints[u.endpoint].MaintenanceResponse, db.ReplyPacket)
				}
			} else {
				if command != "" {
					users[waitingUser{chatID: chatID, endpoint: u.endpoint}] = true
					w.sendText(w.highPriorityMsg, u.endpoint, chatID, false, true, cmdlib.ParseRaw, w.cfg.Endpoints[u.endpoint].MaintenanceResponse, db.ReplyPacket)
					linf("ignoring command %s %s", command, args)
				}
			}
		case elapsed := <-processingDone:
			processing = false
			text := fmt.Sprintf("processing done in %v", elapsed)
			w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, false, true, cmdlib.ParseRaw, text, db.MessagePacket)
		case <-w.outgoingMsgResults:
		}
	}
}

func main() {
	version := flag.Bool("v", false, "prints current version")
	flag.Parse()
	if *version {
		fmt.Println(cmdlib.Version)
		os.Exit(0)
	}

	w := newWorker(flag.Args())
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
	w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, true, true, cmdlib.ParseRaw, "bot started", db.MessagePacket)
	w.createDatabase(databaseDone)
	w.initCache()
	w.db.MustExec("update notification_queue set sending=0")
	w.db.MustExec("update signals set confirmed = 0 where confirmed = 2")

	statRequests := make(chan statRequest)
	w.handleStatEndpoints(statRequests)

	var requestTimer = time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second)
	var cleaningTimer = time.NewTicker(time.Duration(w.cfg.CleaningPeriodSeconds) * time.Second)
	var subsConfirmTimer = time.NewTicker(time.Duration(w.cfg.SubsConfirmationPeriodSeconds) * time.Second)
	var notificationSenderTimer = time.NewTicker(time.Duration(w.cfg.NotificationsReadyPeriodSeconds) * time.Second)
	w.checker.Init(w.checker, cmdlib.CheckerConfig{
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
	w.sendText(w.highPriorityMsg, w.cfg.AdminEndpoint, w.cfg.AdminID, true, true, cmdlib.ParseRaw, "bot is up", db.MessagePacket)
	w.pushOnlineRequest()
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
				w.db.StoreNotifications(notifications)
				if w.cfg.Debug {
					ldbg("status updates processed in %v", elapsed)
				}
			}
			w.logQueryErrors(onlineModels.Errors)
		case u := <-incoming:
			if w.processTGUpdate(u) {
				if !w.maintenance(signals, incoming) {
					return
				}
			}
		case s := <-statRequests:
			w.processStatCommand(s.endpoint, s.writer, s.request, s.done)
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
			}
			query := "insert into interactions (timestamp, chat_id, result, endpoint, priority, delay, kind) values ($1, $2, $3, $4, $5, $6, $7)"
			w.db.MustExec(query, r.timestamp, r.chatID, r.result, r.endpoint, r.priority, r.delay, r.kind)
		case r := <-w.unconfirmedSubsResults:
			w.processSubsConfirmations(r)
		case nots := <-w.sentNotifications:
			for _, n := range nots {
				w.db.MustExec("delete from notification_queue where id = $1", n.ID)
				w.db.MustExec("update users set reports=reports+1 where chat_id = $1", n.ChatID)
			}
		case r := <-w.downloadResults:
			w.downloadErrors[w.downloadResultsPos] = !r
			w.downloadResultsPos = (w.downloadResultsPos + 1) % w.cfg.ErrorDenominator
		}
	}
}
