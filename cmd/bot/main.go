package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bcmk/siren/lib"
	"github.com/bcmk/siren/payments"
	tg "github.com/bcmk/telegram-bot-api"
	"github.com/bradfitz/go-smtpd/smtpd"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

var version = "5.0"

var (
	checkErr = lib.CheckErr
	lerr     = lib.Lerr
	linf     = lib.Linf
	ldbg     = lib.Ldbg
)

type statusUpdate struct {
	modelID string
	status  lib.StatusKind
}

type worker struct {
	clients         []*lib.Client
	bots            map[string]*tg.BotAPI
	db              *sql.DB
	cfg             *config
	mu              *sync.Mutex
	elapsed         time.Duration
	tr              map[string]translations
	checkModel      func(client *lib.Client, modelID string, headers [][2]string, dbg bool) lib.StatusKind
	senders         map[string]func(msg tg.Chattable) (tg.Message, error)
	unknowns        []bool
	unknownsPos     int
	nextErrorReport time.Time
	coinPaymentsAPI *payments.CoinPaymentsAPI
	ipnServeMux     *http.ServeMux
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

func newWorker() *worker {
	if len(os.Args) != 2 {
		panic("usage: siren <config>")
	}
	cfg := readConfig(os.Args[1])

	var clients []*lib.Client
	for _, address := range cfg.SourceIPAddresses {
		clients = append(clients, lib.HTTPClientWithTimeoutAndAddress(cfg.TimeoutSeconds, address, cfg.EnableCookies))
	}

	bots := make(map[string]*tg.BotAPI)
	senders := make(map[string]func(msg tg.Chattable) (tg.Message, error))
	for n, p := range cfg.Endpoints {
		bot, err := tg.NewBotAPIWithClient(p.BotToken, clients[0].Client)
		checkErr(err)
		bots[n] = bot
		senders[n] = bot.Send
	}
	db, err := sql.Open("sqlite3", cfg.DBPath)
	checkErr(err)
	w := &worker{
		bots:        bots,
		db:          db,
		cfg:         cfg,
		clients:     clients,
		mu:          &sync.Mutex{},
		tr:          loadAllTranslations(cfg),
		senders:     senders,
		unknowns:    make([]bool, cfg.errorDenominator),
		ipnServeMux: http.NewServeMux(),
	}

	if cp := cfg.CoinPayments; cp != nil {
		w.coinPaymentsAPI = payments.NewCoinPaymentsAPI(cp.PublicKey, cp.PrivateKey, "https://"+cp.IPNListenURL, cfg.TimeoutSeconds, cfg.Debug)
	}

	switch cfg.Website {
	case "bongacams":
		w.checkModel = lib.CheckModelBongaCams
	case "chaturbate":
		w.checkModel = lib.CheckModelChaturbate
	case "stripchat":
		w.checkModel = lib.CheckModelStripchat
	default:
		panic("wrong website")
	}

	return w
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
	stmt.Close()
}

func (w *worker) incrementBlock(endpoint string, chatID int64) {
	w.mustExec("insert or ignore into block (endpoint, chat_id, block) values (?,?,?)", endpoint, chatID, 0)
	w.mustExec("update block set block=block+1 where chat_id=? and endpoint=?", chatID, endpoint)
}

func (w *worker) resetBlock(endpoint string, chatID int64) {
	w.mustExec("update block set block=0 where endpoint=? and chat_id=?", endpoint, chatID)
}

func (w *worker) sendText(endpoint string, chatID int64, notify bool, parse parseKind, text string) {
	msg := tg.NewMessage(chatID, text)
	msg.DisableNotification = !notify
	switch parse {
	case parseHTML, parseMarkdown:
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

func (w *worker) sendTr(endpoint string, chatID int64, notify bool, translation *translation, args ...interface{}) {
	text := fmt.Sprintf(translation.Str, args...)
	w.sendText(endpoint, chatID, notify, translation.Parse, text)
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
		create table if not exists statuses (
			model_id text primary key,
			status integer,
			not_found integer not null default 0);`)
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
		create table if not exists users (
			chat_id integer primary key,
			max_models integer not null default 0);`)
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
			endpoint text not null default ''
		);`)
}

func (w *worker) updateStatus(modelID string, newStatus lib.StatusKind) bool {
	if newStatus != lib.StatusNotFound {
		w.mustExec("update statuses set not_found=0 where model_id=?", modelID)
	} else {
		newStatus = lib.StatusOffline
	}

	signalsQuery := w.db.QueryRow("select count(*) from signals where model_id=?", modelID)
	if singleInt(signalsQuery) == 0 {
		return false
	}
	oldStatusQuery, err := w.db.Query("select status from statuses where model_id=?", modelID)
	checkErr(err)
	defer oldStatusQuery.Close()
	if !oldStatusQuery.Next() {
		w.mustExec("insert into statuses (model_id, status) values (?,?)", modelID, newStatus)
		return true
	}
	var oldStatus lib.StatusKind
	checkErr(oldStatusQuery.Scan(&oldStatus))
	checkErr(oldStatusQuery.Close())
	w.mustExec("update statuses set status=? where model_id=?", newStatus, modelID)
	return oldStatus != newStatus
}

func (w *worker) notFound(modelID string) bool {
	linf("model %s not found", modelID)
	exists := w.db.QueryRow("select count(*) from statuses where model_id=?", modelID)
	if singleInt(exists) == 0 {
		return false
	}
	w.mustExec("update statuses set not_found=not_found+1 where model_id=?", modelID)
	notFound := w.db.QueryRow("select not_found from statuses where model_id=?", modelID)
	return singleInt(notFound) > w.cfg.NotFoundThreshold
}

func (w *worker) reportNotFound(modelID string) {
	chats, endpoints := w.chatsForModel(modelID)
	for i, chatID := range chats {
		endpoint := endpoints[i]
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].ProfileRemoved, modelID)
	}
}

func (w *worker) removeNotFound(modelID string) {
	w.mustExec("delete from signals where model_id=?", modelID)
	w.cleanStatuses()
}

func (w *worker) models() (models []string) {
	modelsQuery, err := w.db.Query(
		`select distinct model_id from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where block.block is null or block.block<?
		order by model_id`,
		w.cfg.BlockThreshold)
	checkErr(err)
	defer modelsQuery.Close()
	for modelsQuery.Next() {
		var modelID string
		checkErr(modelsQuery.Scan(&modelID))
		models = append(models, modelID)
	}
	return
}

func (w *worker) chatsForModel(modelID string) (chats []int64, endpoints []string) {
	chatsQuery, err := w.db.Query(
		`select signals.chat_id, signals.endpoint from signals left join block
		on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where signals.model_id=? and (block.block is null or block.block<?)
		order by signals.chat_id`,
		modelID,
		w.cfg.BlockThreshold)
	checkErr(err)
	defer chatsQuery.Close()
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
	chatsQuery, err := w.db.Query(
		`select distinct signals.chat_id from signals left join block
		on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block<?) and signals.endpoint=?
		order by signals.chat_id`,
		w.cfg.BlockThreshold,
		endpoint)
	checkErr(err)
	defer chatsQuery.Close()
	for chatsQuery.Next() {
		var chatID int64
		checkErr(chatsQuery.Scan(&chatID))
		chats = append(chats, chatID)
	}
	return
}

func (w *worker) statusesForChat(endpoint string, chatID int64) []statusUpdate {
	statusesQuery, err := w.db.Query(`select statuses.model_id, statuses.status
		from statuses inner join signals
		on statuses.model_id=signals.model_id
		where signals.chat_id=? and signals.endpoint=?
		order by statuses.model_id`, chatID, endpoint)
	checkErr(err)
	defer statusesQuery.Close()
	var statuses []statusUpdate
	for statusesQuery.Next() {
		var modelID string
		var status lib.StatusKind
		checkErr(statusesQuery.Scan(&modelID, &status))
		statuses = append(statuses, statusUpdate{modelID: modelID, status: status})
	}
	return statuses
}

func (w *worker) statusKey(endpoint string, status lib.StatusKind) *translation {
	switch status {
	case lib.StatusOnline:
		return w.tr[endpoint].OnlineList
	case lib.StatusDenied:
		return w.tr[endpoint].DeniedList
	default:
		return w.tr[endpoint].OfflineList
	}
}

func (w *worker) reportStatus(endpoint string, chatID int64, modelID string, status lib.StatusKind) {
	switch status {
	case lib.StatusOnline:
		w.sendTr(endpoint, chatID, true, w.tr[endpoint].Online, modelID)
	case lib.StatusOffline:
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Offline, modelID)
	case lib.StatusDenied:
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Denied, modelID)
	}
}

func (w *worker) startChecker() (input chan []string, output chan statusUpdate) {
	input = make(chan []string)
	output = make(chan statusUpdate)
	clientIdx := 0
	clientsNum := len(w.clients)
	go func() {
		for models := range input {
			start := time.Now()
			for _, modelID := range models {
				queryStart := time.Now()
				newStatus := w.checkModel(w.clients[clientIdx], modelID, w.cfg.Headers, w.cfg.Debug)
				output <- statusUpdate{modelID: modelID, status: newStatus}
				queryElapsed := time.Since(queryStart) / time.Millisecond
				if w.cfg.IntervalMs != 0 {
					sleep := w.cfg.IntervalMs/len(w.clients) - int(queryElapsed)
					if sleep > 0 {
						time.Sleep(time.Duration(sleep) * time.Millisecond)
					}
				}
				clientIdx++
				if clientIdx == clientsNum {
					clientIdx = 0
				}
			}
			elapsed := time.Since(start)
			w.mu.Lock()
			w.elapsed = elapsed
			w.mu.Unlock()
		}
	}()
	return
}

func singleInt(row *sql.Row) (result int) {
	checkErr(row.Scan(&result))
	return result
}

func (w *worker) checkExists(endpoint string, chatID int64, modelID string) bool {
	duplicate := w.db.QueryRow("select count(*) from signals where chat_id=? and model_id=? and endpoint=?", chatID, modelID, endpoint)
	count := singleInt(duplicate)
	return count != 0
}

func (w *worker) subscriptionsNumber(endpoint string, chatID int64) int {
	limit := w.db.QueryRow("select count(*) from signals where chat_id=? and endpoint=?", chatID, endpoint)
	count := singleInt(limit)
	return count
}

func (w *worker) maxModels(chatID int64) int {
	query, err := w.db.Query("select max_models from users where chat_id=?", chatID)
	checkErr(err)
	defer query.Close()
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

func (w *worker) addModel(endpoint string, chatID int64, modelID string) {
	if modelID == "" {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].SyntaxAdd)
		return
	}
	modelID = strings.ToLower(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, modelID)
		return
	}

	w.addUser(endpoint, chatID)

	if w.checkExists(endpoint, chatID, modelID) {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].AlreadyAdded, modelID)
		return
	}
	subscriptionsNumber := w.subscriptionsNumber(endpoint, chatID)
	maxModels := w.maxModels(chatID)
	if subscriptionsNumber >= maxModels {
		if w.cfg.CoinPayments != nil {
			w.sendTr(
				endpoint,
				chatID,
				false,
				w.tr[endpoint].ModelsRemain,
				0,
				maxModels,
				maxModels+w.cfg.CoinPayments.subscriptionPacketModelNumber,
				w.cfg.CoinPayments.subscriptionPacketPrice)
		} else {
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].MaxModels, maxModels)
		}
		return
	}
	status := w.checkModel(w.clients[0], modelID, w.cfg.Headers, w.cfg.Debug)
	if status == lib.StatusUnknown || status == lib.StatusNotFound {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].AddError, modelID)
		return
	}
	w.mustExec("insert into signals (chat_id, model_id, endpoint) values (?,?,?)", chatID, modelID, endpoint)
	subscriptionsNumber++
	w.updateStatus(modelID, status)
	if status != lib.StatusDenied {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].ModelAdded, modelID)
	}
	w.reportStatus(endpoint, chatID, modelID, status)
	modelsRemain := maxModels - subscriptionsNumber
	if modelsRemain <= w.cfg.HeavyUserRemainder && w.cfg.CoinPayments != nil {
		w.sendTr(
			endpoint,
			chatID,
			false,
			w.tr[endpoint].ModelsRemain,
			modelsRemain,
			maxModels,
			maxModels+w.cfg.CoinPayments.subscriptionPacketModelNumber,
			w.cfg.CoinPayments.subscriptionPacketPrice)
	}
}

func (w *worker) removeModel(endpoint string, chatID int64, modelID string) {
	if modelID == "" {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].SyntaxRemove)
		return
	}
	modelID = strings.ToLower(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].InvalidSymbols, modelID)
		return
	}
	if !w.checkExists(endpoint, chatID, modelID) {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].ModelNotInList, modelID)
		return
	}
	w.mustExec("delete from signals where chat_id=? and model_id=? and endpoint=?", chatID, modelID, endpoint)
	w.cleanStatuses()
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].ModelRemoved, modelID)
}

func (w *worker) sureRemoveAll(endpoint string, chatID int64) {
	w.mustExec("delete from signals where chat_id=? and endpoint=?", chatID, endpoint)
	w.cleanStatuses()
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].AllModelsRemoved)
}

func (w *worker) buy(endpoint string, chatID int64) {
	buttons := [][]tg.InlineKeyboardButton{}
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
	return result + w.cfg.MailHost
}

func (w *worker) transaction(uuid string) (status payments.StatusKind, chatID int64, endpoint string) {
	query, err := w.db.Query("select status, chat_id, endpoint from transactions where local_id=?", uuid)
	checkErr(err)
	defer query.Close()
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
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].UnknownCurrency)
		return
	}

	w.addUser(endpoint, chatID)
	email := w.email(endpoint, chatID)
	localID := uuid.New()
	transaction, err := w.coinPaymentsAPI.CreateTransaction(w.cfg.CoinPayments.subscriptionPacketPrice, currency, email, localID.String())
	if err != nil {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].TryToBuyLater)
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

	w.sendTr(endpoint, chatID, false, w.tr[endpoint].PayThis, transaction.Amount, currency, transaction.CheckoutURL)
}

func (w *worker) cleanStatuses() {
	w.mustExec("delete from statuses where not exists(select * from signals where signals.model_id=statuses.model_id);")
}

func (w *worker) listModels(endpoint string, chatID int64) {
	statuses := w.statusesForChat(endpoint, chatID)
	var lines []string
	for _, s := range statuses {
		lines = append(lines, fmt.Sprintf(w.statusKey(endpoint, s.status).Str, s.modelID))
	}
	if len(lines) == 0 {
		lines = append(lines, w.tr[endpoint].NoModels.Str)
	}
	w.sendText(endpoint, chatID, false, w.tr[endpoint].NoModels.Parse, strings.Join(lines, "\n"))
}

func (w *worker) listOnlineModels(endpoint string, chatID int64) {
	statuses := w.statusesForChat(endpoint, chatID)
	online := 0
	for _, s := range statuses {
		if s.status == lib.StatusOnline {
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].Online, s.modelID)
			online++
		}
	}
	if online == 0 {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].NoOnlineModels)
	}
}

func (w *worker) feedback(endpoint string, chatID int64, text string) {
	if text == "" {
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].SyntaxFeedback)
		return
	}

	w.mustExec("insert into feedback (endpoint, chat_id, text) values (?, ?, ?)", endpoint, chatID, text)
	w.sendTr(endpoint, chatID, false, w.tr[endpoint].Feedback)
	w.sendText(endpoint, w.cfg.AdminID, true, parseRaw, fmt.Sprintf("Feedback: %s", text))
}

func (w *worker) setLimit(chatID int64, max_models int) {
	w.mustExec(`
		insert or replace into users (chat_id, max_models) values (?, ?)
		on conflict(chat_id) do update set max_models=excluded.max_models`,
		chatID,
		max_models)
}

func (w *worker) unknownsNumber() int {
	var errors = 0
	for _, s := range w.unknowns {
		if s {
			errors += 1
		}
	}
	return errors
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
	query := w.db.QueryRow(
		`select count(distinct signals.chat_id) from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0) and signals.endpoint=?`, endpoint)
	return singleInt(query)
}

func (w *worker) activeUsersTotalCount() int {
	query := w.db.QueryRow(
		`select count(distinct signals.chat_id) from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block = 0)`)
	return singleInt(query)
}

func (w *worker) modelsCount(endpoint string) int {
	query := w.db.QueryRow("select count(distinct model_id) from signals where endpoint=?", endpoint)
	return singleInt(query)
}

func (w *worker) modelsToQueryOnEndpointCount(endpoint string) int {
	query := w.db.QueryRow(
		`select count(distinct signals.model_id) from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < ?) and signals.endpoint=?`,
		w.cfg.BlockThreshold,
		endpoint)
	return singleInt(query)
}

func (w *worker) modelsToQueryTotalCount() int {
	query := w.db.QueryRow(
		`select count(distinct signals.model_id) from signals
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where (block.block is null or block.block < ?)`,
		w.cfg.BlockThreshold)
	return singleInt(query)
}
func (w *worker) onlineModelsCount(endpoint string) int {
	query := w.db.QueryRow(`
		select count(distinct signals.model_id) from signals
		join statuses on signals.model_id=statuses.model_id
		left join block on signals.chat_id=block.chat_id and signals.endpoint=block.endpoint
		where statuses.status=2 and (block.block is null or block.block < ?) and signals.endpoint=?`,
		w.cfg.BlockThreshold,
		endpoint)
	return singleInt(query)
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
		fmt.Sprintf("Models to query: %d", stat.ModelsToQueryOnEndpointCount),
		fmt.Sprintf("Queries duration: %d s", stat.QueriesDurationSeconds),
		fmt.Sprintf("Error rate: %d/%d", stat.ErrorRate[0], stat.ErrorRate[1]),
		fmt.Sprintf("Memory usage: %d KiB", stat.Rss),
		fmt.Sprintf("Transactions: %d/%d", stat.TransactionsOnEndpointFinished, stat.TransactionsOnEndpointCount),
	}
}

func (w *worker) stat(endpoint string) {
	w.sendText(endpoint, w.cfg.AdminID, true, parseRaw, strings.Join(w.statStrings(endpoint), "\n"))
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
		w.sendText(endpoint, chatID, true, parseRaw, text)
	}
}

func (w *worker) direct(endpoint string, arguments string) {
	parts := strings.SplitN(arguments, " ", 2)
	if len(parts) < 2 {
		w.sendText(endpoint, w.cfg.AdminID, false, parseRaw, "usage: /direct chatID text")
		return
	}
	whom, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		w.sendText(endpoint, w.cfg.AdminID, false, parseRaw, "first argument is invalid")
		return
	}
	text := parts[1]
	if text == "" {
		return
	}
	w.sendText(endpoint, whom, true, parseRaw, text)
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

func (w *worker) processAdminMessage(endpoint string, chatID int64, command, arguments string) bool {
	switch command {
	case "stat":
		w.stat(endpoint)
		return true
	case "broadcast":
		w.broadcast(endpoint, arguments)
		return true
	case "direct":
		w.direct(endpoint, arguments)
		return true
	case "set_max_models":
		parts := strings.Split(arguments, " ")
		if len(parts) != 2 {
			w.sendText(endpoint, chatID, false, parseRaw, "expecting two arguments")
			return true
		}
		who, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			w.sendText(endpoint, chatID, false, parseRaw, "first argument is invalid")
			return true
		}
		max_models, err := strconv.Atoi(parts[1])
		if err != nil {
			w.sendText(endpoint, chatID, false, parseRaw, "second argument is invalid")
			return true
		}
		w.setLimit(who, max_models)
		w.sendText(endpoint, chatID, false, parseRaw, "OK")
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
	defer modelsQuery.Close()
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
		if host != w.cfg.MailHost {
			continue
		}
		email := w.recordForEmail(username)
		if email != nil {
			emails[*email] = true
		}
	}

	for email := range emails {
		w.sendTr(email.endpoint, email.chatID, true, w.tr[email.endpoint].MailReceived,
			e.mime.GetHeader("Subject"),
			e.mime.GetHeader("From"),
			e.mime.Text)
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

func envelopeFactory(ch chan *env) func(smtpd.Connection, smtpd.MailAddress) (smtpd.Envelope, error) {
	return func(c smtpd.Connection, from smtpd.MailAddress) (smtpd.Envelope, error) {
		return &env{BasicEnvelope: &smtpd.BasicEnvelope{}, from: from, ch: ch}, nil
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
		w.addModel(endpoint, chatID, arguments)
	case "remove":
		arguments = strings.Replace(arguments, "—", "--", -1)
		w.removeModel(endpoint, chatID, arguments)
	case "list":
		w.listModels(endpoint, chatID)
	case "online":
		w.listOnlineModels(endpoint, chatID)
	case "start", "help":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Help)
	case "donate":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Donation)
	case "feedback":
		w.feedback(endpoint, chatID, arguments)
	case "source":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].SourceCode)
	case "language":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Languages)
	case "version":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Version, version)
	case "remove_all":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].RemoveAll)
	case "sure_remove_all":
		w.sureRemoveAll(endpoint, chatID)
	case "buy":
		if w.cfg.CoinPayments == nil {
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].UnknownCommand)
			return
		}
		w.buy(endpoint, chatID)
	case "buy_with":
		if w.cfg.CoinPayments == nil {
			w.sendTr(endpoint, chatID, false, w.tr[endpoint].UnknownCommand)
			return
		}
		w.buyWith(endpoint, chatID, arguments)
	case "max_models":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].YourMaxModels, w.maxModels(chatID))
	case "":
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].Slash)
	default:
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].UnknownCommand)
	}
}

func (w *worker) processPeriodic(statusRequests chan []string) {
	unknownsNumber := w.unknownsNumber()
	now := time.Now()
	if w.nextErrorReport.Before(now) && unknownsNumber > w.cfg.errorThreshold {
		w.sendText(w.cfg.AdminEndpoint, w.cfg.AdminID, true, parseRaw, fmt.Sprintf("Dangerous error rate reached: %d/%d", unknownsNumber, w.cfg.errorDenominator))
		w.nextErrorReport = now.Add(time.Minute * time.Duration(w.cfg.ErrorReportingPeriodMinutes))
	}

	select {
	case statusRequests <- w.models():
	default:
		linf("the queue is full")
	}
}

func (w *worker) processStatusUpdate(statusUpdate statusUpdate) {
	if statusUpdate.status == lib.StatusNotFound {
		if w.notFound(statusUpdate.modelID) {
			w.reportNotFound(statusUpdate.modelID)
			w.removeNotFound(statusUpdate.modelID)
		}
	}
	if statusUpdate.status != lib.StatusUnknown && w.updateStatus(statusUpdate.modelID, statusUpdate.status) {
		if w.cfg.Debug {
			ldbg("reporting status of the model %s", statusUpdate.modelID)
		}
		chats, endpoints := w.chatsForModel(statusUpdate.modelID)
		for i, chatID := range chats {
			w.reportStatus(endpoints[i], chatID, statusUpdate.modelID, statusUpdate.status)
		}
	}
	w.unknowns[w.unknownsPos] = statusUpdate.status == lib.StatusUnknown
	w.unknownsPos = (w.unknownsPos + 1) % w.cfg.errorDenominator
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
						w.sendTr(p.endpoint, u.Message.Chat.ID, false, w.tr[p.endpoint].Help)
						break addedToChat
					}
				}
			}
		} else if u.Message.IsCommand() {
			w.processIncomingCommand(p.endpoint, u.Message.Chat.ID, u.Message.Command(), u.Message.CommandArguments())
		} else {
			if u.Message.Text == "" {
				return
			}
			parts := strings.SplitN(u.Message.Text, " ", 2)
			for len(parts) < 2 {
				parts = append(parts, "")
			}
			w.processIncomingCommand(p.endpoint, u.Message.Chat.ID, parts[0], parts[1])
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
		if len(data) != 2 {
			w.sendTr(p.endpoint, chatID, false, w.tr[p.endpoint].InvalidCommand)
			return
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
		return 0, errors.New("Cannot parse statm")
	}

	rss, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}

	return rss * int64(os.Getpagesize()), err
}

func (w *worker) getStat(endpoint string) statistics {
	w.mu.Lock()
	elapsed := w.elapsed
	w.mu.Unlock()

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
		ModelsToQueryOnEndpointCount:   w.modelsToQueryOnEndpointCount(endpoint),
		ModelsToQueryTotalCount:        w.modelsToQueryTotalCount(),
		OnlineModelsCount:              w.onlineModelsCount(endpoint),
		TransactionsOnEndpointCount:    w.transactionsOnEndpoint(endpoint),
		TransactionsOnEndpointFinished: w.transactionsOnEndpointFinished(endpoint),
		QueriesDurationSeconds:         int(elapsed.Seconds()),
		ErrorRate:                      [2]int{w.unknownsNumber(), w.cfg.errorDenominator},
		Rss:                            rss / 1024,
		MaxRss:                         rusage.Maxrss}
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
		statJson, err := json.MarshalIndent(w.getStat(endpoint), "", "    ")
		checkErr(err)
		_, err = writer.Write(statJson)
		checkErr(err)
	}
}

func (w *worker) handleIPN(writer http.ResponseWriter, r *http.Request) {
	linf("got IPN data")

	newStatus, custom, err := payments.ProcessIPN(r, w.cfg.CoinPayments.IPNSecret, w.cfg.Debug)
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
		w.mustExec("update users set max_models = max_models + (select sum(model_number) from transactions where local_id=?)", custom)
		w.sendTr(endpoint, chatID, false, w.tr[endpoint].PaymentComplete, w.maxModels(chatID))
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
	ids := []int64{}
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

func main() {
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
	smtp := &smtpd.Server{
		Hostname:  w.cfg.MailHost,
		Addr:      w.cfg.MailListenAddress,
		OnNewMail: envelopeFactory(mail),
	}
	go func() {
		err := smtp.ListenAndServe()
		checkErr(err)
	}()

	var periodicTimer = time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second)
	statusRequests, statusUpdates := w.startChecker()
	statusRequests <- w.models()
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	for {
		select {
		case <-periodicTimer.C:
			runtime.GC()
			w.processPeriodic(statusRequests)
		case statusUpdate := <-statusUpdates:
			w.processStatusUpdate(statusUpdate)
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
