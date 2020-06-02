package main

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/bcmk/go-smtpd/smtpd"
	"github.com/bcmk/siren/lib"
	"github.com/bcmk/siren/payments"
	tg "github.com/bcmk/telegram-bot-api"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

var (
	version  = "5.0"
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
	endpoint string
	chatID   int64
	modelID  string
	status   lib.StatusKind
}

type statusChange struct {
	ModelID   string
	Status    lib.StatusKind
	Timestamp int
}

type worker struct {
	clients      []*lib.Client
	bots         map[string]*tg.BotAPI
	db           *sql.DB
	cfg          *config
	elapsed      time.Duration
	tr           map[string]*lib.Translations
	tpl          map[string]*template.Template
	checkModel   func(client *lib.Client, modelID string, headers [][2]string, dbg bool) lib.StatusKind
	startChecker func(
		usersOnlineEndpoint string,
		clients []*lib.Client,
		headers [][2]string,
		intervalMs int,
		debug bool,
	) (
		requests chan lib.StatusRequest,
		output chan []lib.StatusUpdate,
		elapsed chan time.Duration)

	senders               map[string]func(msg tg.Chattable) (tg.Message, error)
	unsuccessfulRequests  []bool
	successfulRequestsPos int
	nextErrorReport       time.Time
	coinPaymentsAPI       *payments.CoinPaymentsAPI
	ipnServeMux           *http.ServeMux
	mailTLS               *tls.Config
}

type packet struct {
	message  tg.Update
	endpoint string
}

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

//go:generate jsonenums -type=checkerKind
type checkerKind int

const (
	checkerAPI checkerKind = iota
	checkerPolling
)

func (c checkerKind) String() string {
	switch c {
	case checkerAPI:
		return "api"
	case checkerPolling:
		return "polling"
	default:
		return "unknown"
	}
}

func newWorker() *worker {
	if len(os.Args) != 2 {
		panic("usage: siren <config>")
	}
	cfg := readConfig(os.Args[1])

	var err error
	var mailTLS *tls.Config

	if cfg.Mail != nil {
		mailTLS, err = loadTLS(cfg.Mail.Certificate, cfg.Mail.CertificateKey)
		checkErr(err)
	}

	var clients []*lib.Client
	for _, address := range cfg.SourceIPAddresses {
		clients = append(clients, lib.HTTPClientWithTimeoutAndAddress(cfg.TimeoutSeconds, address, cfg.EnableCookies))
	}

	bots := make(map[string]*tg.BotAPI)
	senders := make(map[string]func(msg tg.Chattable) (tg.Message, error))
	for n, p := range cfg.Endpoints {
		//noinspection GoNilness
		bot, err := tg.NewBotAPIWithClient(p.BotToken, clients[0].Client)
		checkErr(err)
		bots[n] = bot
		senders[n] = bot.Send
	}
	db, err := sql.Open("sqlite3", cfg.DBPath)
	checkErr(err)
	tr, tpl := lib.LoadAllTranslations(trsByEndpoint(cfg))
	w := &worker{
		bots:                 bots,
		db:                   db,
		cfg:                  cfg,
		clients:              clients,
		tr:                   tr,
		tpl:                  tpl,
		senders:              senders,
		unsuccessfulRequests: make([]bool, cfg.errorDenominator),
		ipnServeMux:          http.NewServeMux(),
		mailTLS:              mailTLS,
	}

	if cp := cfg.CoinPayments; cp != nil {
		w.coinPaymentsAPI = payments.NewCoinPaymentsAPI(cp.PublicKey, cp.PrivateKey, "https://"+cp.IPNListenURL, cfg.TimeoutSeconds, cfg.Debug)
	}

	switch cfg.Website {
	case "bongacams":
		w.checkModel = lib.CheckModelBongaCams
		switch w.cfg.Checker {
		case checkerAPI:
			w.startChecker = lib.StartBongaCamsAPIChecker
		case checkerPolling:
			w.startChecker = lib.StartBongaCamsPollingChecker
		default:
			panic("specify checker")
		}
	case "chaturbate":
		w.checkModel = lib.CheckModelChaturbate
		w.startChecker = lib.StartChaturbateAPIChecker
	case "stripchat":
		w.checkModel = lib.CheckModelStripchat
		w.startChecker = lib.StartStripchatAPIChecker
	default:
		panic("wrong website")
	}

	return w
}

func (w *worker) cleanStatuses() {
	if w.cfg.Checker != checkerPolling {
		return
	}
	now := int(time.Now().Unix())
	query, err := w.db.Query(`
		select model_id from models
		where status != ? and not exists(select * from signals where signals.model_id = models.model_id)`,
		lib.StatusUnknown)
	checkErr(err)
	var models []string
	for query.Next() {
		var modelID string
		checkErr(query.Scan(&modelID))
		models = append(models, modelID)
	}
	w.mustExec("begin")
	for _, modelID := range models {
		w.mustExec("update models set status=? where model_id=?", lib.StatusUnknown, modelID)
		w.mustExec("insert into status_changes (model_id, status, timestamp) values (?,?,?)", modelID, lib.StatusUnknown, now)
	}
	w.mustExec("end")
}

func trsByEndpoint(cfg *config) map[string][]string {
	result := make(map[string][]string)
	for k, v := range cfg.Endpoints {
		result[k] = v.Translation
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

func (w *worker) mustExec(query string, args ...interface{}) {
	stmt, err := w.db.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
	checkErr(stmt.Close())
}

func (w *worker) incrementBlock(endpoint string, chatID int64) {
	w.mustExec(`
		insert into block (endpoint, chat_id, block) values (?,?,1)
		on conflict(chat_id, endpoint) do update set block=block+1`,
		endpoint,
		chatID)
}

func (w *worker) resetBlock(endpoint string, chatID int64) {
	w.mustExec("update block set block=0 where endpoint=? and chat_id=?", endpoint, chatID)
}

func (w *worker) sendText(endpoint string, chatID int64, notify bool, disablePreview bool, parse lib.ParseKind, text string) {
	msg := tg.NewMessage(chatID, text)
	msg.DisableNotification = !notify
	msg.DisableWebPagePreview = disablePreview
	switch parse {
	case lib.ParseHTML, lib.ParseMarkdown:
		msg.ParseMode = parse.String()
	}
	w.sendMessage(endpoint, &messageConfig{msg})
}

func (w *worker) sendMessage(endpoint string, msg baseChattable) {
	chatID := msg.baseChat().ChatID
	if _, err := w.bots[endpoint].Send(msg); err != nil {
		switch err := err.(type) {
		case tg.Error:
			if err.Code == 403 {
				linf("bot is blocked by the user %d, %v", chatID, err)
				w.incrementBlock(endpoint, chatID)
			} else {
				lerr("cannot send a message to %d, code %d, %v", chatID, err.Code, err)
			}
		default:
			lerr("unexpected error type while sending a message to %d, %v", msg.baseChat().ChatID, err)
		}
		return
	}
	if w.cfg.Debug {
		ldbg("message sent to %d", chatID)
	}
	w.resetBlock(endpoint, chatID)
}

func templateToString(t *template.Template, key string, data map[string]interface{}) string {
	buf := &bytes.Buffer{}
	err := t.ExecuteTemplate(buf, key, data)
	checkErr(err)
	return buf.String()
}

func (w *worker) sendTr(endpoint string, chatID int64, notify bool, translation *lib.Translation, data map[string]interface{}) {
	tpl := w.tpl[endpoint]
	text := templateToString(tpl, translation.Key, data)
	w.sendText(endpoint, chatID, notify, translation.DisablePreview, translation.Parse, text)
}

func (w *worker) createDatabase() {
	linf("creating database if needed...")
	w.mustExec(`
		create table if not exists signals (
			chat_id integer,
			model_id text,
			endpoint text not null default '',
			primary key (chat_id, model_id, endpoint));`)
	w.mustExec(`
		create index if not exists idx_signals_model_id
		on signals(model_id);`)
	w.mustExec(`
		create index if not exists idx_signals_chat_id
		on signals(chat_id);`)
	w.mustExec(`
		create index if not exists idx_signals_endpoint
		on signals(endpoint);`)
	w.mustExec(`
		create table if not exists status_changes (
			model_id text,
			status integer not null default 0,
			timestamp integer not null default 0);`)
	w.mustExec(`
		create index if not exists idx_status_changes_timestamp
		on status_changes(timestamp);`)
	w.mustExec(`
		create index if not exists idx_status_changes_model_id
		on status_changes(model_id);`)
	w.mustExec(`
		create table if not exists models (
			model_id text primary key,
			status integer not null default 0,
			referred_users integer not null default 0);`)
	w.mustExec(`
		create index if not exists idx_models_model_id
		on models(model_id);`)
	w.mustExec(`
		create table if not exists feedback (
			chat_id integer,
			text text,
			endpoint text not null default '');`)
	w.mustExec(`
		create table if not exists block (
			chat_id integer,
			block integer not null default 0,
			endpoint text not null default '',
			primary key(chat_id, endpoint));`)
	w.mustExec(`
		create index if not exists idx_block_chat_id
		on block(chat_id);`)
	w.mustExec(`
		create index if not exists idx_block_endpoint
		on block(endpoint);`)
	w.mustExec(`
		create table if not exists users (
			chat_id integer primary key,
			max_models integer not null default 0,
			reports integer not null default 0);`)
	w.mustExec(`
		create table if not exists emails (
			chat_id integer,
			endpoint text not null default '',
			email text not null default '',
			primary key(chat_id, endpoint));`)
	w.mustExec(`
		create table if not exists transactions (
			local_id text primary key,
			kind text,
			chat_id integer,
			remote_id text,
			timeout integer,
			amount text,
			address string,
			status_url string,
			checkout_url string,
			dest_tag string,
			status integer,
			timestamp integer,
			model_number integer,
			currency text,
			endpoint text not null default '');`)
	w.mustExec(`
		create table if not exists referrals (
			chat_id integer primary key,
			referral_id text not null default '',
			referred_users integer not null default 0);`)
}

func (w *worker) lastStatusChange(modelID string) statusChange {
	query, err := w.db.Query("select status, timestamp from status_changes where model_id=? order by timestamp desc limit 1", modelID)
	checkErr(err)
	defer query.Close()
	if !query.Next() {
		return statusChange{ModelID: modelID, Status: lib.StatusUnknown, Timestamp: 0}
	}
	var statusChange statusChange
	checkErr(query.Scan(&statusChange.Status, &statusChange.Timestamp))
	return statusChange
}

func (w *worker) lastSeenInfo(modelID string, now int) (begin int, end int, prevStatus lib.StatusKind) {
	query, err := w.db.Query(`
		select timestamp, end, prev_status from (
			select
				*,
				lead(timestamp) over (order by timestamp) as end,
				lag(status) over (order by timestamp) as prev_status
			from status_changes
			where model_id=?)
		where status=?
		order by timestamp desc limit 1`,
		modelID,
		lib.StatusOnline)
	checkErr(err)
	defer query.Close()
	if !query.Next() {
		return 0, 0, lib.StatusUnknown
	}
	var maybeEnd *int
	var maybePrevStatus *lib.StatusKind
	checkErr(query.Scan(&begin, &maybeEnd, &maybePrevStatus))
	if maybeEnd == nil {
		zero := 0
		maybeEnd = &zero
	}
	if maybePrevStatus == nil {
		unknown := lib.StatusUnknown
		maybePrevStatus = &unknown
	}
	return begin, *maybeEnd, *maybePrevStatus
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

func (w *worker) updateStatus(
	statusChange statusChange,
	lastStatusChange statusChange,
	lastConfirmedStatus lib.StatusKind,
) (
	changeConfirmed bool,
) {
	if statusChange.Status != lastStatusChange.Status {
		w.mustExec("insert into status_changes (model_id, status, timestamp) values (?,?,?)",
			statusChange.ModelID,
			statusChange.Status,
			statusChange.Timestamp)
	}
	confirmationSeconds := w.confirmationSeconds(statusChange.Status)
	durationConfirmed := false ||
		confirmationSeconds == 0 ||
		(statusChange.Status == lastStatusChange.Status && statusChange.Timestamp-lastStatusChange.Timestamp >= confirmationSeconds)
	if lastConfirmedStatus != statusChange.Status && durationConfirmed {
		w.mustExec(`
			insert into models (model_id, status) values (?, ?)
			on conflict(model_id) do update set status=excluded.status`,
			statusChange.ModelID,
			statusChange.Status)
		return true
	}
	return false
}

func (w *worker) knownModels() (models []string) {
	modelsQuery, err := w.db.Query("select model_id from models order by model_id")
	checkErr(err)
	defer func() { checkErr(modelsQuery.Close()) }()
	for modelsQuery.Next() {
		var modelID string
		checkErr(modelsQuery.Scan(&modelID))
		models = append(models, modelID)
	}
	return
}

func (w *worker) modelsToPoll() (models []string) {
	modelsQuery, err := w.db.Query(`
		select distinct model_id from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where block.block is null or block.block<?
		order by model_id`,
		w.cfg.BlockThreshold)
	checkErr(err)
	defer func() { checkErr(modelsQuery.Close()) }()
	for modelsQuery.Next() {
		var modelID string
		checkErr(modelsQuery.Scan(&modelID))
		models = append(models, modelID)
	}
	return
}

func (w *worker) chatsForModels() (chats map[string][]int64, endpoints map[string][]string) {
	chats = make(map[string][]int64)
	endpoints = make(map[string][]string)
	chatsQuery, err := w.db.Query(`select model_id, chat_id, endpoint from signals`)
	checkErr(err)
	defer func() { checkErr(chatsQuery.Close()) }()
	for chatsQuery.Next() {
		var modelID string
		var chatID int64
		var endpoint string
		checkErr(chatsQuery.Scan(&modelID, &chatID, &endpoint))
		chats[modelID] = append(chats[modelID], chatID)
		endpoints[modelID] = append(endpoints[modelID], endpoint)
	}
	return
}

func (w *worker) chatsForModel(modelID string) (chats []int64, endpoints []string) {
	chatsQuery, err := w.db.Query(`select chat_id, endpoint from signals where model_id=? order by chat_id`, modelID)
	checkErr(err)
	defer func() { checkErr(chatsQuery.Close()) }()
	for chatsQuery.Next() {
		var chatID int64
		var endpoint string
		checkErr(chatsQuery.Scan(&chatID, &endpoint))
		chats = append(chats, chatID)
		endpoints = append(endpoints, endpoint)
	}
	return
}

func (w *worker) broadcastChats(endpoint string) (chats []int64) {
	chatsQuery, err := w.db.Query(`select distinct chat_id from signals where endpoint=? order by chat_id`, endpoint)
	checkErr(err)
	defer func() { checkErr(chatsQuery.Close()) }()
	for chatsQuery.Next() {
		var chatID int64
		checkErr(chatsQuery.Scan(&chatID))
		chats = append(chats, chatID)
	}
	return
}

func (w *worker) statusesForChat(endpoint string, chatID int64) []lib.StatusUpdate {
	statusesQuery, err := w.db.Query(`
		select models.model_id, models.status
		from models
		join signals on signals.model_id=models.model_id
		where signals.chat_id=? and signals.endpoint=?
		order by models.model_id`,
		chatID,
		endpoint)
	checkErr(err)
	defer func() { checkErr(statusesQuery.Close()) }()
	var statuses []lib.StatusUpdate
	for statusesQuery.Next() {
		var modelID string
		var status lib.StatusKind
		checkErr(statusesQuery.Scan(&modelID, &status))
		statuses = append(statuses, lib.StatusUpdate{ModelID: modelID, Status: status})
	}
	return statuses
}

func (w *worker) notifyOfStatus(endpoint string, chatID int64, modelID string, status lib.StatusKind) {
	if w.cfg.Debug {
		ldbg("notifying of status of the model %s", modelID)
	}
	data := tplData{"model": modelID}
	switch status {
	case lib.StatusOnline:
		w.sendTr(endpoint, chatID, true, w.tr[endpoint].Online, data)
	case lib.StatusOffline:
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Offline, data)
	case lib.StatusDenied:
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Denied, data)
	}
	w.addUser(endpoint, chatID)
	w.mustExec("update users set reports=reports+1 where chat_id=?", chatID)
}

func singleInt(row *sql.Row) (result int) {
	checkErr(row.Scan(&result))
	return result
}

func (w *worker) subscriptionExists(endpoint string, chatID int64, modelID string) bool {
	duplicate := w.db.QueryRow("select count(*) from signals where chat_id=? and model_id=? and endpoint=?", chatID, modelID, endpoint)
	count := singleInt(duplicate)
	return count != 0
}

func (w *worker) userExists(chatID int64) bool {
	count := singleInt(w.db.QueryRow("select count(*) from users where chat_id=?", chatID))
	return count != 0
}

func (w *worker) subscriptionsNumber(endpoint string, chatID int64) int {
	return singleInt(w.db.QueryRow("select count(*) from signals where chat_id=? and endpoint=?", chatID, endpoint))
}

func (w *worker) maxModels(chatID int64) int {
	query, err := w.db.Query("select max_models from users where chat_id=?", chatID)
	checkErr(err)
	defer func() { checkErr(query.Close()) }()
	if !query.Next() {
		return w.cfg.MaxModels
	}
	var result int
	checkErr(query.Scan(&result))
	return result
}

func (w *worker) addUser(endpoint string, chatID int64) {
	w.mustExec(`insert or ignore into users (chat_id, max_models) values (?, ?)`, chatID, w.cfg.MaxModels)
	w.mustExec(`insert or ignore into emails (endpoint, chat_id, email) values (?, ?, ?)`, endpoint, chatID, uuid.New())
}

func (w *worker) addModel(endpoint string, chatID int64, modelID string) bool {
	if modelID == "" {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].SyntaxAdd, nil)
		return false
	}
	modelID = strings.ToLower(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"model": modelID})
		return false
	}

	w.addUser(endpoint, chatID)

	if w.subscriptionExists(endpoint, chatID, modelID) {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].AlreadyAdded, tplData{"model": modelID})
		return false
	}
	subscriptionsNumber := w.subscriptionsNumber(endpoint, chatID)
	maxModels := w.maxModels(chatID)
	if subscriptionsNumber >= maxModels {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].NotEnoughSubscriptions, nil)
		w.subscriptionUsage(endpoint, chatID, true)
		return false
	}
	confirmedStatus := w.confirmedStatus(modelID)
	newStatus := confirmedStatus
	if newStatus == lib.StatusUnknown {
		newStatus = w.checkModel(w.clients[0], modelID, w.cfg.Headers, w.cfg.Debug)
	}
	if newStatus == lib.StatusUnknown || newStatus == lib.StatusNotFound {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].AddError, tplData{"model": modelID})
		return false
	}
	w.mustExec("insert into signals (chat_id, model_id, endpoint) values (?,?,?)", chatID, modelID, endpoint)
	w.mustExec("insert or ignore into models (model_id, status) values (?,?)", modelID, newStatus)
	subscriptionsNumber++
	if newStatus != lib.StatusExists {
		lastStatusChange := w.lastStatusChange(modelID)
		statusChange := statusChange{ModelID: modelID, Status: newStatus, Timestamp: int(time.Now().Unix())}
		_ = w.updateStatus(statusChange, lastStatusChange, confirmedStatus)
	}
	if newStatus != lib.StatusDenied {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].ModelAdded, tplData{"model": modelID})
	}
	if newStatus != lib.StatusExists {
		w.notifyOfStatus(endpoint, chatID, modelID, newStatus)
	}
	if subscriptionsNumber >= maxModels-w.cfg.HeavyUserRemainder {
		w.subscriptionUsage(endpoint, chatID, true)
	}
	return true
}

func (w *worker) subscriptionUsage(endpoint string, chatID int64, ad bool) {
	subscriptionsNumber := w.subscriptionsNumber(endpoint, chatID)
	maxModels := w.maxModels(chatID)
	tr := w.tr[endpoint].SubscriptionUsage
	if ad {
		tr = w.tr[endpoint].SubscriptionUsageAd
	}
	w.sendTr(endpoint, chatID, false, tr, tplData{
		"subscriptions_used":  subscriptionsNumber,
		"total_subscriptions": maxModels})
}

func (w *worker) wantMore(endpoint string, chatID int64) {
	w.subscriptionUsage(endpoint, chatID, false)
	w.showReferral(endpoint, chatID)

	if w.cfg.CoinPayments == nil || w.cfg.Mail == nil {
		return
	}

	text := fmt.Sprintf(w.tr[endpoint].BuyAd.Str,
		w.cfg.CoinPayments.subscriptionPacketPrice,
		w.cfg.CoinPayments.subscriptionPacketModelNumber)

	buttonText := fmt.Sprintf(w.tr[endpoint].BuyButton.Str, w.cfg.CoinPayments.subscriptionPacketModelNumber)
	buttons := [][]tg.InlineKeyboardButton{{tg.NewInlineKeyboardButtonData(buttonText, "buy")}}
	keyboard := tg.NewInlineKeyboardMarkup(buttons...)
	msg := tg.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	w.sendMessage(endpoint, &messageConfig{msg})
}

func (w *worker) removeModel(endpoint string, chatID int64, modelID string) {
	if modelID == "" {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].SyntaxRemove, nil)
		return
	}
	modelID = strings.ToLower(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, tplData{"model": modelID})
		return
	}
	if !w.subscriptionExists(endpoint, chatID, modelID) {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].ModelNotInList, tplData{"model": modelID})
		return
	}
	w.mustExec("delete from signals where chat_id=? and model_id=? and endpoint=?", chatID, modelID, endpoint)
	w.cleanStatuses()
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].ModelRemoved, tplData{"model": modelID})
}

func (w *worker) sureRemoveAll(endpoint string, chatID int64) {
	w.mustExec("delete from signals where chat_id=? and endpoint=?", chatID, endpoint)
	w.cleanStatuses()
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].AllModelsRemoved, nil)
}

func (w *worker) buy(endpoint string, chatID int64) {
	var buttons [][]tg.InlineKeyboardButton
	for _, c := range w.cfg.CoinPayments.Currencies {
		buttons = append(buttons, []tg.InlineKeyboardButton{tg.NewInlineKeyboardButtonData(c, "buy_with "+c)})
	}

	keyboard := tg.NewInlineKeyboardMarkup(buttons...)
	current := w.maxModels(chatID)
	additional := w.cfg.CoinPayments.subscriptionPacketModelNumber
	overall := current + additional
	usd := w.cfg.CoinPayments.subscriptionPacketPrice
	text := fmt.Sprintf(w.tr[endpoint].SelectCurrency.Str, additional, overall, usd)
	msg := tg.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	w.sendMessage(endpoint, &messageConfig{msg})
}

func (w *worker) email(endpoint string, chatID int64) string {
	row := w.db.QueryRow("select email from emails where endpoint=? and chat_id=?", endpoint, chatID)
	var result string
	checkErr(row.Scan(&result))
	return result + "@" + w.cfg.Mail.Host
}

func (w *worker) transaction(uuid string) (status payments.StatusKind, chatID int64, endpoint string) {
	query, err := w.db.Query("select status, chat_id, endpoint from transactions where local_id=?", uuid)
	checkErr(err)
	defer func() { checkErr(query.Close()) }()
	if !query.Next() {
		return
	}
	checkErr(query.Scan(&status, &chatID, &endpoint))
	return
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
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].UnknownCurrency, nil)
		return
	}

	w.addUser(endpoint, chatID)
	email := w.email(endpoint, chatID)
	localID := uuid.New()
	transaction, err := w.coinPaymentsAPI.CreateTransaction(w.cfg.CoinPayments.subscriptionPacketPrice, currency, email, localID.String())
	if err != nil {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].TryToBuyLater, nil)
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

	w.sendTr(endpoint, chatID, false, w.tr[endpoint].PayThis, tplData{
		"price":    transaction.Amount,
		"currency": currency,
		"link":     transaction.CheckoutURL,
	})
}

// calcTimeDiff calculates time difference ignoring summer time and leap seconds
func calcTimeDiff(t1, t2 time.Time) timeDiff {
	var diff timeDiff
	day := int64(time.Hour * 24)
	d := t2.Sub(t1).Nanoseconds()
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
		Model string
		Begin *timeDiff
		End   *timeDiff
	}
	statuses := w.statusesForChat(endpoint, chatID)
	var online, offline, denied []data
	for _, s := range statuses {
		data := data{
			Model: s.ModelID,
		}
		begin, end, prevStatus := w.lastSeenInfo(s.ModelID, now)
		if begin != 0 && prevStatus != lib.StatusUnknown {
			timeDiff := calcTimeDiff(time.Unix(int64(begin), 0), time.Unix(int64(now), 0))
			data.Begin = &timeDiff
		}
		if end != 0 {
			timeDiff := calcTimeDiff(time.Unix(int64(end), 0), time.Unix(int64(now), 0))
			data.End = &timeDiff
		}
		switch s.Status {
		case lib.StatusOnline:
			online = append(online, data)
		case lib.StatusDenied:
			denied = append(denied, data)
		default:
			offline = append(offline, data)
		}
	}
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].List, tplData{"online": online, "offline": offline, "denied": denied})
}

func (w *worker) listOnlineModels(endpoint string, chatID int64) {
	statuses := w.statusesForChat(endpoint, chatID)
	online := 0
	for _, s := range statuses {
		if s.Status == lib.StatusOnline {
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].Online, tplData{"model": s.ModelID})
			online++
		}
	}
	if online == 0 {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].NoOnlineModels, nil)
	}
}

func (w *worker) feedback(endpoint string, chatID int64, text string) {
	if text == "" {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].SyntaxFeedback, nil)
		return
	}

	w.mustExec("insert into feedback (endpoint, chat_id, text) values (?, ?, ?)", endpoint, chatID, text)
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].Feedback, nil)
	w.sendText(endpoint, w.cfg.AdminID, true, true, lib.ParseRaw, fmt.Sprintf("Feedback from %d: %s", chatID, text))
}

func (w *worker) setLimit(chatID int64, maxModels int) {
	w.mustExec(`
		insert into users (chat_id, max_models) values (?, ?)
		on conflict(chat_id) do update set max_models=excluded.max_models`,
		chatID,
		maxModels)
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

func (w *worker) userReferralsCount() int {
	query := w.db.QueryRow("select coalesce(sum(referred_users), 0) from referrals")
	return singleInt(query)
}

func (w *worker) modelReferralsCount() int {
	query := w.db.QueryRow("select coalesce(sum(referred_users), 0) from models")
	return singleInt(query)
}

func (w *worker) reports() int {
	return singleInt(w.db.QueryRow("select coalesce(sum(reports), 0) from users"))
}

func (w *worker) usersCount(endpoint string) int {
	query := w.db.QueryRow("select count(distinct chat_id) from signals where endpoint=?", endpoint)
	return singleInt(query)
}

func (w *worker) groupsCount(endpoint string) int {
	query := w.db.QueryRow("select count(distinct chat_id) from signals where endpoint=? and chat_id < 0", endpoint)
	return singleInt(query)
}

func (w *worker) activeUsersOnEndpointCount(endpoint string) int {
	query := w.db.QueryRow(`
		select count(distinct signals.chat_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0) and signals.endpoint=?`, endpoint)
	return singleInt(query)
}

func (w *worker) activeUsersTotalCount() int {
	query := w.db.QueryRow(`
		select count(distinct signals.chat_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0)`)
	return singleInt(query)
}

func (w *worker) modelsCount(endpoint string) int {
	query := w.db.QueryRow("select count(distinct model_id) from signals where endpoint=?", endpoint)
	return singleInt(query)
}

func (w *worker) modelsToPollOnEndpointCount(endpoint string) int {
	query := w.db.QueryRow(`
		select count(distinct signals.model_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < ?) and signals.endpoint=?`,
		w.cfg.BlockThreshold,
		endpoint)
	return singleInt(query)
}

func (w *worker) modelsToPollTotalCount() int {
	query := w.db.QueryRow(`
		select count(distinct signals.model_id)
		from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < ?)`,
		w.cfg.BlockThreshold)
	return singleInt(query)
}

func (w *worker) onlineModelsCount() int {
	query := w.db.QueryRow("select count(*) from models where models.status=?", lib.StatusOnline)
	return singleInt(query)
}

func (w *worker) confirmedStatus(modelID string) lib.StatusKind {
	query, err := w.db.Query(`select status from models where model_id=?`, modelID)
	checkErr(err)
	defer func() { checkErr(query.Close()) }()
	if !query.Next() {
		return lib.StatusUnknown
	}
	var status lib.StatusKind
	checkErr(query.Scan(&status))
	return status
}

func (w *worker) heavyUsersCount(endpoint string) int {
	query := w.db.QueryRow(`
		select count(*) from (
			select 1 from signals
			left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
			where (block.block is null or block.block = 0) and signals.endpoint=?
			group by signals.chat_id
			having count(*) >= ?);`,
		endpoint,
		w.cfg.MaxModels-w.cfg.HeavyUserRemainder)
	return singleInt(query)
}

func (w *worker) transactionsOnEndpoint(endpoint string) int {
	query := w.db.QueryRow("select count(*) from transactions where endpoint=?", endpoint)
	return singleInt(query)
}

func (w *worker) transactionsOnEndpointFinished(endpoint string) int {
	query := w.db.QueryRow("select count(*) from transactions where endpoint=? and status=?", endpoint, payments.StatusFinished)
	return singleInt(query)
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
		fmt.Sprintf("Queries duration: %d ms", stat.QueriesDurationMilliseconds),
		fmt.Sprintf("Error rate: %d/%d", stat.ErrorRate[0], stat.ErrorRate[1]),
		fmt.Sprintf("Memory usage: %d KiB", stat.Rss),
		fmt.Sprintf("Transactions: %d/%d", stat.TransactionsOnEndpointFinished, stat.TransactionsOnEndpointCount),
		fmt.Sprintf("Reports: %d", stat.ReportsCount),
		fmt.Sprintf("User referrals: %d", stat.UserReferralsCount),
		fmt.Sprintf("Model referrals: %d", stat.ModelReferralsCount),
	}
}

func (w *worker) stat(endpoint string) {
	w.sendText(endpoint, w.cfg.AdminID, true, true, lib.ParseRaw, strings.Join(w.statStrings(endpoint), "\n"))
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
		w.sendText(endpoint, chatID, true, false, lib.ParseRaw, text)
	}
	w.sendText(endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK")
}

func (w *worker) direct(endpoint string, arguments string) {
	parts := strings.SplitN(arguments, " ", 2)
	if len(parts) < 2 {
		w.sendText(endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "usage: /direct chatID text")
		return
	}
	whom, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		w.sendText(endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "first argument is invalid")
		return
	}
	text := parts[1]
	if text == "" {
		return
	}
	w.sendText(endpoint, whom, true, false, lib.ParseRaw, text)
	w.sendText(endpoint, w.cfg.AdminID, false, true, lib.ParseRaw, "OK")
}

func (w *worker) serveEndpoint(n string, p endpoint) {
	linf("serving endpoint %s...", n)
	var err error
	if p.CertificatePath != "" && p.CertificateKeyPath != "" {
		err = http.ListenAndServeTLS(p.ListenAddress, p.CertificatePath, p.CertificateKeyPath, nil)
	} else {
		err = http.ListenAndServe(p.ListenAddress, nil)
	}
	checkErr(err)
}

func (w *worker) serveEndpoints() {
	for n, p := range w.cfg.Endpoints {
		go w.serveEndpoint(n, p)
	}
}

func (w *worker) serveIPN() {
	go func() {
		err := http.ListenAndServe(w.cfg.CoinPayments.IPNListenAddress, w.ipnServeMux)
		checkErr(err)
	}()
}

func (w *worker) logConfig() {
	cfgString, err := json.MarshalIndent(w.cfg, "", "    ")
	checkErr(err)
	linf("config: " + string(cfgString))
}

func (w *worker) myEmail(endpoint string) {
	w.addUser(endpoint, w.cfg.AdminID)
	email := w.email(endpoint, w.cfg.AdminID)
	w.sendText(endpoint, w.cfg.AdminID, true, true, lib.ParseRaw, email)
}

func (w *worker) processAdminMessage(endpoint string, chatID int64, command, arguments string) bool {
	switch command {
	case "stat":
		w.stat(endpoint)
		return true
	case "email":
		w.myEmail(endpoint)
		return true
	case "broadcast":
		w.broadcast(endpoint, arguments)
		return true
	case "direct":
		w.direct(endpoint, arguments)
		return true
	case "set_max_models":
		parts := strings.Fields(arguments)
		if len(parts) != 2 {
			w.sendText(endpoint, chatID, false, true, lib.ParseRaw, "expecting two arguments")
			return true
		}
		who, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			w.sendText(endpoint, chatID, false, true, lib.ParseRaw, "first argument is invalid")
			return true
		}
		maxModels, err := strconv.Atoi(parts[1])
		if err != nil {
			w.sendText(endpoint, chatID, false, true, lib.ParseRaw, "second argument is invalid")
			return true
		}
		w.setLimit(who, maxModels)
		w.sendText(endpoint, chatID, false, true, lib.ParseRaw, "OK")
		return true
	}
	return false
}

func splitAddress(a string) (string, string) {
	a = strings.ToLower(a)
	parts := strings.Split(a, "@")
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func (w *worker) recordForEmail(username string) *email {
	modelsQuery, err := w.db.Query(`select chat_id, endpoint from emails where email=?`, username)
	checkErr(err)
	defer func() { checkErr(modelsQuery.Close()) }()
	if modelsQuery.Next() {
		email := email{email: username}
		checkErr(modelsQuery.Scan(&email.chatID, &email.endpoint))
		return &email
	}
	return nil
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
		w.sendTr(email.endpoint, email.chatID, true, w.tr[email.endpoint].MailReceived, tplData{
			"subject": e.mime.GetHeader("Subject"),
			"from":    e.mime.GetHeader("From"),
			"text":    e.mime.Text})
		for _, inline := range e.mime.Inlines {
			b := tg.FileBytes{Name: inline.FileName, Bytes: inline.Content}
			switch {
			case strings.HasPrefix(inline.ContentType, "image/"):
				msg := tg.NewPhotoUpload(email.chatID, b)
				w.sendMessage(email.endpoint, &photoConfig{msg})
			default:
				msg := tg.NewDocumentUpload(email.chatID, b)
				w.sendMessage(email.endpoint, &documentConfig{msg})
			}
		}
		for _, inline := range e.mime.Attachments {
			b := tg.FileBytes{Name: inline.FileName, Bytes: inline.Content}
			msg := tg.NewDocumentUpload(email.chatID, b)
			w.sendMessage(email.endpoint, &documentConfig{msg})
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
		exists := w.db.QueryRow("select count(*) from referrals where referral_id=?", id)
		if singleInt(exists) == 0 {
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
	if w.userExists(followerChatID) {
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
	referralLink := fmt.Sprintf("https://t.me/%s?start=%s", w.cfg.Endpoints[endpoint].BotName, *referralID)
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].ReferralLink, tplData{
		"link":           referralLink,
		"referral_bonus": w.cfg.ReferralBonus,
		"follower_bonus": w.cfg.FollowerBonus,
	})
}

func (w *worker) start(endpoint string, chatID int64, referrer string) {
	modelID := ""
	switch {
	case strings.HasPrefix(referrer, "m-"):
		modelID = referrer[2:]
		referrer = ""
	case referrer != "":
		referralID := w.referralID(chatID)
		if referralID != nil && *referralID == referrer {
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].OwnReferralLinkHit, nil)
			return
		}
	}
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].Help, nil)
	if chatID > 0 && referrer != "" {
		applied := w.refer(chatID, referrer)
		switch applied {
		case referralApplied:
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].ReferralApplied, nil)
		case invalidReferral:
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].InvalidReferralLink, nil)
		case followerExists:
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].FollowerExists, nil)
		}
	}
	w.addUser(endpoint, chatID)
	if modelID != "" {
		if w.addModel(endpoint, chatID, modelID) {
			w.mustExec("insert or ignore into models (model_id) values (?)", modelID)
			w.mustExec("update models set referred_users=referred_users+1 where model_id=?", modelID)
		}
	}
}

func (w *worker) processIncomingCommand(endpoint string, chatID int64, command, arguments string) {
	w.resetBlock(endpoint, chatID)
	command = strings.ToLower(command)
	linf("chat: %d, command: %s %s", chatID, command, arguments)

	if chatID == w.cfg.AdminID && w.processAdminMessage(endpoint, chatID, command, arguments) {
		return
	}

	switch command {
	case "add":
		arguments = strings.Replace(arguments, "—", "--", -1)
		_ = w.addModel(endpoint, chatID, arguments)
	case "remove":
		arguments = strings.Replace(arguments, "—", "--", -1)
		w.removeModel(endpoint, chatID, arguments)
	case "list":
		now := int(time.Now().Unix())
		w.listModels(endpoint, chatID, now)
	case "online":
		w.listOnlineModels(endpoint, chatID)
	case "start", "help":
		w.start(endpoint, chatID, arguments)
	case "feedback":
		w.feedback(endpoint, chatID, arguments)
	case "social":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Social, nil)
	case "language":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Languages, nil)
	case "version":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Version, tplData{"version": version})
	case "remove_all":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].RemoveAll, nil)
	case "sure_remove_all":
		w.sureRemoveAll(endpoint, chatID)
	case "want_more":
		w.wantMore(endpoint, chatID)
	case "buy":
		if w.cfg.CoinPayments == nil || w.cfg.Mail == nil {
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].UnknownCommand, nil)
			return
		}
		w.buy(endpoint, chatID)
	case "buy_with":
		if w.cfg.CoinPayments == nil || w.cfg.Mail == nil {
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].UnknownCommand, nil)
			return
		}
		w.buyWith(endpoint, chatID, arguments)
	case "max_models":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].YourMaxModels, tplData{"max_models": w.maxModels(chatID)})
	case "referral":
		w.showReferral(endpoint, chatID)
	default:
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].UnknownCommand, nil)
	}
}

func (w *worker) processPeriodic(statusRequests chan lib.StatusRequest) {
	unsuccessfulRequestsCount := w.unsuccessfulRequestsCount()
	now := time.Now()
	if w.nextErrorReport.Before(now) && unsuccessfulRequestsCount > w.cfg.errorThreshold {
		text := fmt.Sprintf("Dangerous error rate reached: %d/%d", unsuccessfulRequestsCount, w.cfg.errorDenominator)
		w.sendText(w.cfg.AdminEndpoint, w.cfg.AdminID, true, true, lib.ParseRaw, text)
		w.nextErrorReport = now.Add(time.Minute * time.Duration(w.cfg.ErrorReportingPeriodMinutes))
	}

	select {
	case statusRequests <- lib.StatusRequest{KnownModels: w.knownModels(), ModelsToPoll: w.modelsToPoll()}:
	default:
		linf("the queue is full")
	}
}

func (w *worker) lastStatusChanges() map[string]statusChange {
	query, err := w.db.Query(`
		select status_changes.model_id, status_changes.status, last.max_timestamp
		from (select model_id, max(timestamp) as max_timestamp from status_changes group by model_id) last
		join status_changes
		on status_changes.model_id = last.model_id and timestamp = last.max_timestamp`)
	checkErr(err)
	defer query.Close()
	statusChanges := map[string]statusChange{}
	for query.Next() {
		var statusChange statusChange
		checkErr(query.Scan(&statusChange.ModelID, &statusChange.Status, &statusChange.Timestamp))
		statusChanges[statusChange.ModelID] = statusChange
	}
	return statusChanges
}

func (w *worker) confirmedStatuses() map[string]lib.StatusKind {
	query, err := w.db.Query("select model_id, status from models")
	checkErr(err)
	defer query.Close()
	statuses := map[string]lib.StatusKind{}
	for query.Next() {
		var modelID string
		var status lib.StatusKind
		checkErr(query.Scan(&modelID, &status))
		statuses[modelID] = status
	}
	return statuses
}

func (w *worker) processStatusUpdates(
	statusUpdates []lib.StatusUpdate,
	now int,
) (
	confirmedChangesCount int,
	notifications []notification,
) {
	lastStatusChanges := w.lastStatusChanges()
	confirmedStatuses := w.confirmedStatuses()
	chatsForModels, endpointsForModels := w.chatsForModels()
	w.mustExec("begin")
	for _, u := range statusUpdates {
		if u.Status != lib.StatusUnknown {
			lastStatusChange := lastStatusChanges[u.ModelID]
			confirmedStatus := confirmedStatuses[u.ModelID]
			statusChange := statusChange{ModelID: u.ModelID, Status: u.Status, Timestamp: now}
			changeConfirmed := w.updateStatus(statusChange, lastStatusChange, confirmedStatus)
			if changeConfirmed {
				confirmedChangesCount++
			}
			if changeConfirmed && (w.cfg.OfflineNotifications || u.Status != lib.StatusOffline) {
				chats := chatsForModels[u.ModelID]
				endpoints := endpointsForModels[u.ModelID]
				for i, chatID := range chats {
					notifications = append(notifications, notification{
						endpoint: endpoints[i],
						chatID:   chatID,
						modelID:  u.ModelID,
						status:   u.Status,
					})
				}
			}
		}
	}
	w.mustExec("end")
	return
}

func (w *worker) processTGUpdate(p packet) {
	u := p.message
	if u.Message != nil && u.Message.Chat != nil {
		if newMembers := u.Message.NewChatMembers; newMembers != nil && len(*newMembers) > 0 {
			ourIDs := w.ourIDs()
		addedToChat:
			for _, m := range *newMembers {
				for _, ourID := range ourIDs {
					if int64(m.ID) == ourID {
						w.sendTr(p.endpoint, u.Message.Chat.ID, false, w.tr[p.endpoint].Help, nil)
						break addedToChat
					}
				}
			}
		} else if u.Message.IsCommand() {
			w.processIncomingCommand(p.endpoint, u.Message.Chat.ID, u.Message.Command(), strings.TrimSpace(u.Message.CommandArguments()))
		} else {
			if u.Message.Text == "" {
				return
			}
			parts := strings.SplitN(u.Message.Text, " ", 2)
			if parts[0] == "" {
				return
			}
			for len(parts) < 2 {
				parts = append(parts, "")
			}
			w.processIncomingCommand(p.endpoint, u.Message.Chat.ID, parts[0], strings.TrimSpace(parts[1]))
		}
	}
	if u.CallbackQuery != nil {
		callback := tg.CallbackConfig{CallbackQueryID: u.CallbackQuery.ID}
		_, err := w.bots[p.endpoint].AnswerCallbackQuery(callback)
		if err != nil {
			lerr("cannot answer callback query, %v", err)
		}
		data := strings.SplitN(u.CallbackQuery.Data, " ", 2)
		chatID := int64(u.CallbackQuery.From.ID)
		if len(data) < 2 {
			data = append(data, "")
		}
		w.processIncomingCommand(p.endpoint, chatID, data[0], data[1])
	}
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
		OnlineModelsCount:              w.onlineModelsCount(),
		TransactionsOnEndpointCount:    w.transactionsOnEndpoint(endpoint),
		TransactionsOnEndpointFinished: w.transactionsOnEndpointFinished(endpoint),
		QueriesDurationMilliseconds:    int(w.elapsed.Milliseconds()),
		ErrorRate:                      [2]int{w.unsuccessfulRequestsCount(), w.cfg.errorDenominator},
		Rss:                            rss / 1024,
		MaxRss:                         rusage.Maxrss,
		UserReferralsCount:             w.userReferralsCount(),
		ModelReferralsCount:            w.modelReferralsCount(),
		ReportsCount:                   w.reports(),
	}
}

func (w *worker) handleStat(endpoint string) func(writer http.ResponseWriter, r *http.Request) {
	return func(writer http.ResponseWriter, r *http.Request) {
		passwords, ok := r.URL.Query()["password"]
		if !ok || len(passwords[0]) < 1 {
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
		checkErr(err)
	}
}

func (w *worker) handleIPN(writer http.ResponseWriter, r *http.Request) {
	linf("got IPN data")

	newStatus, custom, err := payments.ParseIPN(r, w.cfg.CoinPayments.IPNSecret, w.cfg.Debug)
	if err != nil {
		lerr("error on processing IPN, %v", err)
		return
	}

	switch newStatus {
	case payments.StatusFinished:
		oldStatus, chatID, endpoint := w.transaction(custom)
		if oldStatus == payments.StatusFinished {
			lerr("transaction is already finished")
			return
		}
		w.mustExec("update transactions set status=? where local_id=?", payments.StatusFinished, custom)
		w.mustExec("update users set max_models = max_models + (select coalesce(sum(model_number), 0) from transactions where local_id=?)", custom)
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].PaymentComplete, tplData{"max_models": w.maxModels(chatID)})
		linf("payment %s is finished", custom)
	case payments.StatusCanceled:
		w.mustExec("update transactions set status=? where local_id=?", payments.StatusCanceled, custom)
		linf("payment %s is canceled", custom)
	default:
		linf("payment %s is still pending", custom)
	}
}

func (w *worker) handleStatEndpoints() {
	for n, p := range w.cfg.Endpoints {
		http.HandleFunc(p.WebhookDomain+"/stat", w.handleStat(n))
	}
}

func (w *worker) handleIPNEndpoint() {
	w.ipnServeMux.HandleFunc(w.cfg.CoinPayments.IPNListenURL, w.handleIPN)
}

func (w *worker) incoming() chan packet {
	result := make(chan packet)
	for n, p := range w.cfg.Endpoints {
		linf("listening for a webhook for endpoint %s", n)
		incoming := w.bots[n].ListenForWebhook(p.WebhookDomain + p.ListenPath)
		go func(n string, incoming tg.UpdatesChannel) {
			for i := range incoming {
				result <- packet{message: i, endpoint: n}
			}
		}(n, incoming)
	}
	return result
}

func (w *worker) ourIDs() []int64 {
	var ids []int64
	for _, e := range w.cfg.Endpoints {
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

func (w *worker) referralID(chatID int64) *string {
	query, err := w.db.Query("select referral_id from referrals where chat_id=?", chatID)
	checkErr(err)
	defer func() { checkErr(query.Close()) }()
	if !query.Next() {
		return nil
	}
	var referralID string
	checkErr(query.Scan(&referralID))
	return &referralID
}

func (w *worker) chatForReferralID(referralID string) *int64 {
	query, err := w.db.Query("select chat_id from referrals where referral_id=?", referralID)
	checkErr(err)
	defer func() { checkErr(query.Close()) }()
	if !query.Next() {
		return nil
	}
	var chatID int64
	checkErr(query.Scan(&chatID))
	return &chatID
}

func main() {
	rand.Seed(time.Now().UnixNano())

	w := newWorker()
	w.logConfig()
	w.setWebhook()
	w.createDatabase()

	incoming := w.incoming()
	w.handleStatEndpoints()
	w.serveEndpoints()

	if w.cfg.CoinPayments != nil {
		w.handleIPNEndpoint()
		w.serveIPN()
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

	var periodicTimer = time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second)
	statusRequestsChan, statusUpdatesChan, elapsed := w.startChecker(w.cfg.UsersOnlineEndpoint, w.clients, w.cfg.Headers, w.cfg.IntervalMs, w.cfg.Debug)
	statusRequestsChan <- lib.StatusRequest{KnownModels: w.knownModels(), ModelsToPoll: w.modelsToPoll()}
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	for {
		select {
		case e := <-elapsed:
			w.elapsed = e
		case <-periodicTimer.C:
			runtime.GC()
			w.processPeriodic(statusRequestsChan)
		case statusUpdates := <-statusUpdatesChan:
			w.unsuccessfulRequests[w.successfulRequestsPos] = statusUpdates == nil
			w.successfulRequestsPos = (w.successfulRequestsPos + 1) % w.cfg.errorDenominator
			now := int(time.Now().Unix())
			_, notifications := w.processStatusUpdates(statusUpdates, now)
			for _, n := range notifications {
				w.notifyOfStatus(n.endpoint, n.chatID, n.modelID, n.status)
			}
		case u := <-incoming:
			w.processTGUpdate(u)
		case m := <-mail:
			w.mailReceived(m)
		case s := <-signals:
			linf("got signal %v", s)
			w.removeWebhook()
			return
		}
	}
}
