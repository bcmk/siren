package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bcmk/siren/lib"
	tg "github.com/bcmk/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
)

var version = "2.1"

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
	client        *http.Client
	bot           *tg.BotAPI
	db            *sql.DB
	cfg           *config
	mu            *sync.Mutex
	elapsed       time.Duration
	tr            translations
	checkModel    func(client *http.Client, modelID string, dbg bool) lib.StatusKind
	sendTGMessage func(msg tg.Chattable) (tg.Message, error)
}

func newWorker() *worker {
	if len(os.Args) != 2 {
		panic("usage: siren <config>")
	}
	cfg := readConfig(os.Args[1])
	client := &http.Client{
		CheckRedirect: lib.NoRedirect,
		Timeout:       time.Second * time.Duration(cfg.TimeoutSeconds),
	}
	bot, err := tg.NewBotAPIWithClient(cfg.BotToken, client)
	checkErr(err)
	db, err := sql.Open("sqlite3", cfg.DBPath)
	checkErr(err)
	w := &worker{
		bot:           bot,
		db:            db,
		cfg:           cfg,
		client:        client,
		mu:            &sync.Mutex{},
		tr:            loadTranslations(cfg.Translation),
		sendTGMessage: bot.Send,
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

func (w *worker) mustExec(query string, args ...interface{}) {
	stmt, err := w.db.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
}

func (w *worker) incrementBlock(chatID int64) {
	w.mustExec("insert or ignore into users (chat_id, block) values (?,?)", chatID, 0)
	w.mustExec("update users set block=block+1 where chat_id=?", chatID)
}

func (w *worker) resetBlock(chatID int64) {
	w.mustExec("update users set block=0 where chat_id=?", chatID)
}

func (w *worker) send(chatID int64, notify bool, parse parseKind, text string) {
	msg := tg.NewMessage(chatID, text)
	msg.DisableNotification = !notify
	switch parse {
	case parseHTML, parseMarkdown:
		msg.ParseMode = parse.String()
	}
	if _, err := w.sendTGMessage(msg); err != nil {
		switch err := err.(type) {
		case tg.Error:
			if err.Code == 403 {
				lerr("bot is blocked by the user %d, %v", chatID, err)
				w.incrementBlock(chatID)
			} else {
				lerr("cannot send a message to %d, code %d, %v", chatID, err.Code, err)
			}
		default:
			lerr("cannot send a message to %d, %v", chatID, err)
		}
	} else {
		if w.cfg.Debug {
			ldbg("message sent to %d", chatID)
		}
		w.resetBlock(chatID)
	}
}

func (w *worker) sendTr(chatID int64, notify bool, translation *translation, args ...interface{}) {
	text := fmt.Sprintf(translation.Str, args...)
	w.send(chatID, notify, translation.Parse, text)
}

func (w *worker) createDatabase() {
	w.mustExec("create table if not exists signals (chat_id integer, model_id text);")
	w.mustExec("create table if not exists statuses (model_id text, status integer, not_found integer not null default 0);")
	w.mustExec("create table if not exists feedback (chat_id integer, text text);")
	w.mustExec("create table if not exists users (chat_id integer primary key, block integer not null default 0);")
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

func (w *worker) notFound(modelID string) {
	linf("model %s not found", modelID)
	exists := w.db.QueryRow("select count(*) from statuses where model_id=?", modelID)
	if singleInt(exists) == 0 {
		return
	}
	w.mustExec("update statuses set not_found=not_found+1 where model_id=?", modelID)
	notFound := w.db.QueryRow("select not_found from statuses where model_id=?", modelID)
	if singleInt(notFound) > w.cfg.NotFoundThreshold {
		chats := w.chatsForModel(modelID)
		w.mustExec("delete from signals where model_id=?", modelID)
		w.cleanStatuses()
		for _, chatID := range chats {
			w.sendTr(chatID, false, w.tr.ProfileRemoved, modelID)
		}
	}
}

func (w *worker) models() (models []string) {
	modelsQuery, err := w.db.Query(
		`select distinct model_id from signals left join users
		on signals.chat_id=users.chat_id
		where users.block is null or users.block<?
		order by model_id`,
		w.cfg.BlockThreshold)
	checkErr(err)
	for modelsQuery.Next() {
		var modelID string
		checkErr(modelsQuery.Scan(&modelID))
		models = append(models, modelID)
	}
	return
}

func (w *worker) chatsForModel(modelID string) (chats []int64) {
	chatsQuery, err := w.db.Query(
		`select signals.chat_id from signals left join users
		on signals.chat_id=users.chat_id
		where signals.model_id=? and (users.block is null or users.block<?)
		order by signals.chat_id`,
		modelID,
		w.cfg.BlockThreshold)
	checkErr(err)
	for chatsQuery.Next() {
		var chatID int64
		checkErr(chatsQuery.Scan(&chatID))
		chats = append(chats, chatID)
	}
	return
}

func (w *worker) broadcastChats() (chats []int64) {
	chatsQuery, err := w.db.Query(
		`select distinct signals.chat_id from signals left join users
		on signals.chat_id=users.chat_id
		where users.block is null or users.block<?
		order by signals.chat_id`,
		w.cfg.BlockThreshold)
	checkErr(err)
	for chatsQuery.Next() {
		var chatID int64
		checkErr(chatsQuery.Scan(&chatID))
		chats = append(chats, chatID)
	}
	return
}

func (w *worker) statusesForChat(chatID int64) []statusUpdate {
	models, err := w.db.Query(`select statuses.model_id, statuses.status
		from statuses inner join signals
		on statuses.model_id=signals.model_id
		where signals.chat_id=?
		order by statuses.model_id`, chatID)
	checkErr(err)
	var statuses []statusUpdate
	for models.Next() {
		var modelID string
		var status lib.StatusKind
		checkErr(models.Scan(&modelID, &status))
		statuses = append(statuses, statusUpdate{modelID: modelID, status: status})
	}
	return statuses
}

func (w *worker) statusKey(status lib.StatusKind) *translation {
	if status == lib.StatusOnline {
		return w.tr.Online
	}
	return w.tr.Offline
}

func (w *worker) reportStatus(chatID int64, modelID string, status lib.StatusKind) {
	switch status {
	case lib.StatusOnline:
		w.sendTr(chatID, true, w.tr.Online, modelID)
	case lib.StatusOffline:
		w.sendTr(chatID, false, w.tr.Offline, modelID)
	}
}

func (w *worker) startChecker() (input chan []string, output chan statusUpdate) {
	input = make(chan []string)
	output = make(chan statusUpdate)
	go func() {
		for models := range input {
			start := time.Now()
			for _, modelID := range models {
				newStatus := w.checkModel(w.client, modelID, w.cfg.Debug)
				if newStatus != lib.StatusUnknown {
					output <- statusUpdate{modelID: modelID, status: newStatus}
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
	err := row.Scan(&result)
	checkErr(err)
	return result
}

func (w *worker) checkExists(chatID int64, modelID string) bool {
	duplicate := w.db.QueryRow("select count(*) from signals where chat_id=? and model_id=?", chatID, modelID)
	count := singleInt(duplicate)
	return count != 0
}

func (w *worker) checkMaximum(chatID int64) int {
	limit := w.db.QueryRow("select count(*) from signals where chat_id=?", chatID)
	count := singleInt(limit)
	return count
}

func (w *worker) addModel(chatID int64, modelID string) {
	if modelID == "" {
		w.sendTr(chatID, false, w.tr.SyntaxAdd)
		return
	}
	modelID = strings.ToLower(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(chatID, false, w.tr.InvalidSymbols, modelID)
		return
	}
	if w.checkExists(chatID, modelID) {
		w.sendTr(chatID, false, w.tr.AlreadyAdded, modelID)
		return
	}
	count := w.checkMaximum(chatID)
	if count > w.cfg.MaxModels-1 {
		w.sendTr(chatID, false, w.tr.MaxModels, w.cfg.MaxModels)
		return
	}
	status := w.checkModel(w.client, modelID, w.cfg.Debug)
	if status == lib.StatusUnknown || status == lib.StatusNotFound {
		w.sendTr(chatID, false, w.tr.AddError, modelID)
		return
	}
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", chatID, modelID)
	w.updateStatus(modelID, status)
	w.sendTr(chatID, false, w.tr.ModelAdded, modelID)
	w.reportStatus(chatID, modelID, status)
}

func (w *worker) removeModel(chatID int64, modelID string) {
	if modelID == "" {
		w.sendTr(chatID, false, w.tr.SyntaxRemove)
		return
	}
	modelID = strings.ToLower(modelID)
	if !lib.ModelIDRegexp.MatchString(modelID) {
		w.sendTr(chatID, false, w.tr.InvalidSymbols, modelID)
		return
	}
	if !w.checkExists(chatID, modelID) {
		w.sendTr(chatID, false, w.tr.ModelNotInList, modelID)
		return
	}
	w.mustExec("delete from signals where chat_id=? and model_id=?", chatID, modelID)
	w.cleanStatuses()
	w.sendTr(chatID, false, w.tr.ModelRemoved, modelID)
}

func (w *worker) sureRemoveAll(chatID int64) {
	w.mustExec("delete from signals where chat_id=?", chatID)
	w.cleanStatuses()
	w.sendTr(chatID, false, w.tr.AllModelsRemoved)
}

func (w *worker) cleanStatuses() {
	w.mustExec("delete from statuses where not exists(select * from signals where signals.model_id=statuses.model_id);")
}

func (w *worker) listModels(chatID int64) {
	statuses := w.statusesForChat(chatID)
	var lines []string
	for _, s := range statuses {
		lines = append(lines, fmt.Sprintf(w.statusKey(s.status).Str, s.modelID))
	}
	if len(lines) == 0 {
		lines = append(lines, w.tr.NoModels.Str)
	}
	w.send(chatID, false, parseRaw, strings.Join(lines, "\n"))
}

func (w *worker) feedback(chatID int64, text string) {
	if text == "" {
		w.sendTr(chatID, false, w.tr.SyntaxFeedback)
		return
	}

	w.mustExec("insert into feedback (chat_id, text) values (?, ?)", chatID, text)
	w.sendTr(chatID, false, w.tr.Feedback)
	w.send(w.cfg.AdminID, true, parseRaw, fmt.Sprintf("Feedback: %s", text))
}

func (w *worker) stat(chatID int64) {
	query := w.db.QueryRow("select count(distinct chat_id) from signals")
	usersCount := singleInt(query)
	query = w.db.QueryRow("select count(distinct model_id) from signals")
	modelsCount := singleInt(query)
	w.mu.Lock()
	elapsed := w.elapsed
	w.mu.Unlock()
	w.send(chatID, true, parseRaw, fmt.Sprintf("Users: %d\nModels: %d\nQueries duration: %v", usersCount, modelsCount, elapsed))
}

func (w *worker) broadcast(text string) {
	if text == "" {
		return
	}
	if w.cfg.Debug {
		ldbg("broadcasting")
	}
	chats := w.broadcastChats()
	for _, chatID := range chats {
		w.send(chatID, true, parseRaw, text)
	}
}

func (w *worker) serve() {
	var err error
	if w.cfg.Certificate != "" && w.cfg.Key != "" {
		err = http.ListenAndServeTLS(w.cfg.ListenAddress, w.cfg.Certificate, w.cfg.Key, nil)
	} else {
		err = http.ListenAndServe(w.cfg.ListenAddress, nil)
	}
	checkErr(err)
}

func (w *worker) logConfig() {
	cfgString, err := json.MarshalIndent(w.cfg, "", "    ")
	checkErr(err)
	linf("config: " + string(cfgString))
}

func (w *worker) processAdminMessage(chatID int64, command, arguments string) bool {
	switch command {
	case "stat":
		w.stat(chatID)
		return true
	case "broadcast":
		w.broadcast(arguments)
		return true
	}
	return false
}

func (w *worker) processIncomingMessage(chatID int64, command, arguments string) {
	w.resetBlock(chatID)

	linf("command: %s %s", command, arguments)
	if chatID == w.cfg.AdminID && w.processAdminMessage(chatID, command, arguments) {
		return
	}

	switch command {
	case "add":
		w.addModel(chatID, arguments)
	case "remove":
		w.removeModel(chatID, arguments)
	case "list":
		w.listModels(chatID)
	case "start", "help":
		w.sendTr(chatID, false, w.tr.Help)
	case "donate":
		w.sendTr(chatID, false, w.tr.Donation)
	case "feedback":
		w.feedback(chatID, arguments)
	case "source":
		w.sendTr(chatID, false, w.tr.SourceCode)
	case "language":
		w.sendTr(chatID, false, w.tr.Languages)
	case "version":
		w.sendTr(chatID, false, w.tr.Version, version)
	case "remove_all":
		w.sendTr(chatID, false, w.tr.RemoveAll)
	case "sure_remove_all":
		w.sureRemoveAll(chatID)
	case "":
		w.sendTr(chatID, false, w.tr.Slash)
	default:
		w.sendTr(chatID, false, w.tr.UnknownCommand)
	}
}

func main() {
	w := newWorker()
	w.logConfig()
	w.createDatabase()

	incoming := w.bot.ListenForWebhook(w.cfg.ListenPath)
	go w.serve()
	var periodicTimer = time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second)
	statusRequests, statusUpdates := w.startChecker()
	statusRequests <- w.models()
	for {
		select {
		case <-periodicTimer.C:
			select {
			case statusRequests <- w.models():
			default:
				lerr("the queue is full")
			}
		case statusUpdate := <-statusUpdates:
			if statusUpdate.status == lib.StatusNotFound {
				w.notFound(statusUpdate.modelID)
			}
			if w.updateStatus(statusUpdate.modelID, statusUpdate.status) {
				if w.cfg.Debug {
					ldbg("reporting status of the model %s", statusUpdate.modelID)
				}
				for _, chatID := range w.chatsForModel(statusUpdate.modelID) {
					w.reportStatus(chatID, statusUpdate.modelID, statusUpdate.status)
				}
			}
		case u := <-incoming:
			if u.Message != nil && u.Message.Chat != nil {
				w.processIncomingMessage(u.Message.Chat.ID, u.Message.Command(), u.Message.CommandArguments())
			}
		}
	}
}
