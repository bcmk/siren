package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bcmk/siren"
	tg "github.com/bcmk/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
)

var modelIDRegexp = regexp.MustCompile(`^[a-z0-9\-_]+$`)
var version = "1.0"

var (
	checkErr = siren.CheckErr
	lerr     = siren.Lerr
	linf     = siren.Linf
	ldbg     = siren.Ldbg
)

type worker struct {
	client     *http.Client
	bot        *tg.BotAPI
	db         *sql.DB
	cfg        *config
	mu         *sync.Mutex
	elapsed    time.Duration
	tr         translations
	checkModel func(clien *http.Client, modelID string, dbg bool) siren.StatusKind
}

type statusUpdate struct {
	modelID string
	status  siren.StatusKind
}

func newWorker() *worker {
	if len(os.Args) != 2 {
		panic("usage: siren <config>")
	}
	cfg := readConfig(os.Args[1])
	client := &http.Client{
		CheckRedirect: noRedirect,
		Timeout:       time.Second * time.Duration(cfg.TimeoutSeconds),
	}
	bot, err := tg.NewBotAPIWithClient(cfg.BotToken, client)
	checkErr(err)
	db, err := sql.Open("sqlite3", cfg.DBPath)
	checkErr(err)
	w := &worker{
		bot:    bot,
		db:     db,
		cfg:    cfg,
		client: client,
		mu:     &sync.Mutex{},
		tr:     loadTranslations(cfg.Translation),
	}
	switch cfg.Website {
	case "bongacams":
		w.checkModel = siren.CheckModelBongacams
	case "chaturbate":
		w.checkModel = siren.CheckModelChaturbate
	default:
		panic("wrong website")
	}
	return w
}

func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func (w *worker) send(chatID int64, notify bool, parse parseKind, text string) {
	msg := tg.NewMessage(chatID, text)
	msg.DisableNotification = !notify
	switch parse {
	case parseHTML:
		msg.ParseMode = "html"
	case parseMarkdown:
		msg.ParseMode = "markdown"
	}
	if _, err := w.bot.Send(msg); err != nil {
		lerr("cannot send a message, %v", err)
	}
}

func (w *worker) sendTr(chatID int64, notify bool, translation *translation, args ...interface{}) {
	msg := tg.NewMessage(chatID, fmt.Sprintf(translation.Str, args...))
	msg.DisableNotification = !notify

	switch translation.Parse {
	case parseHTML:
		msg.ParseMode = "html"
	case parseMarkdown:
		msg.ParseMode = "markdown"
	}

	if _, err := w.bot.Send(msg); err != nil {
		lerr("cannot send a message, %v", err)
	}
}

func (w *worker) createDatabase() {
	stmt, err := w.db.Prepare("create table if not exists signals (chat_id integer, model_id text);")
	checkErr(err)
	_, err = stmt.Exec()
	checkErr(err)
	stmt, err = w.db.Prepare("create table if not exists statuses (model_id text, status integer, not_found integer not null default 0);")
	checkErr(err)
	_, err = stmt.Exec()
	checkErr(err)
	stmt, err = w.db.Prepare("create table if not exists feedback (chat_id integer, text text);")
	checkErr(err)
	_, err = stmt.Exec()
	checkErr(err)
}

func (w *worker) updateStatus(modelID string, newStatus siren.StatusKind) bool {
	if newStatus != siren.StatusNotFound {
		stmt, err := w.db.Prepare("update statuses set not_found=0 where model_id=?")
		checkErr(err)
		_, err = stmt.Exec(modelID)
		checkErr(err)
	} else {
		newStatus = siren.StatusOffline
	}

	signalsQuery := w.db.QueryRow("select count(*) from signals where model_id=?", modelID)
	if singleInt(signalsQuery) == 0 {
		return false
	}
	oldStatusQuery, err := w.db.Query("select status from statuses where model_id=?", modelID)
	checkErr(err)
	if !oldStatusQuery.Next() {
		var stmt *sql.Stmt
		stmt, err = w.db.Prepare("insert into statuses (model_id, status) values (?,?)")
		checkErr(err)
		_, err = stmt.Exec(modelID, newStatus)
		checkErr(err)
		return true
	}
	var oldStatus siren.StatusKind
	checkErr(oldStatusQuery.Scan(&oldStatus))
	checkErr(oldStatusQuery.Close())
	stmt, err := w.db.Prepare("update statuses set status=? where model_id=?")
	checkErr(err)
	_, err = stmt.Exec(newStatus, modelID)
	checkErr(err)
	return oldStatus != newStatus
}

func (w *worker) notFound(modelID string) {
	linf("model %s not found", modelID)
	exists := w.db.QueryRow("select count(*) from statuses where model_id=?", modelID)
	if singleInt(exists) == 0 {
		return
	}
	stmt, err := w.db.Prepare("update statuses set not_found=not_found+1 where model_id=?")
	checkErr(err)
	_, err = stmt.Exec(modelID)
	checkErr(err)
	notFound := w.db.QueryRow("select not_found from statuses where model_id=?", modelID)
	if singleInt(notFound) > w.cfg.NotFoundThreshold {
		chats := w.chatsForModel(modelID)
		stmt, err := w.db.Prepare("delete from signals where model_id=?")
		checkErr(err)
		_, err = stmt.Exec(modelID)
		checkErr(err)
		w.cleanStatuses()
		for _, chatID := range chats {
			w.sendTr(chatID, false, w.tr.Removed, modelID)
		}
	}
}

func (w *worker) models() (models []string) {
	modelsQuery, err := w.db.Query("select distinct model_id from signals")
	checkErr(err)
	for modelsQuery.Next() {
		var modelID string
		checkErr(modelsQuery.Scan(&modelID))
		models = append(models, modelID)
	}
	return
}

func (w *worker) chatsForModel(modelID string) (chats []int64) {
	chatsQuery, err := w.db.Query("select chat_id from signals where model_id=?", modelID)
	checkErr(err)
	for chatsQuery.Next() {
		var chatID int64
		checkErr(chatsQuery.Scan(&chatID))
		chats = append(chats, chatID)
	}
	return
}

func (w *worker) chats() (chats []int64) {
	chatsQuery, err := w.db.Query("select distinct chat_id from signals")
	checkErr(err)
	for chatsQuery.Next() {
		var chatID int64
		checkErr(chatsQuery.Scan(&chatID))
		chats = append(chats, chatID)
	}
	return
}

func (w *worker) statusKey(status siren.StatusKind) *translation {
	if status == siren.StatusOnline {
		return w.tr.Online
	}
	return w.tr.Offline
}

func (w *worker) reportStatus(chatID int64, modelID string, status siren.StatusKind) {
	switch status {
	case siren.StatusOnline:
		w.sendTr(chatID, true, w.tr.Online, modelID)
	case siren.StatusOffline:
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
				if newStatus != siren.StatusUnknown {
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
	if !modelIDRegexp.MatchString(modelID) {
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
	if status == siren.StatusUnknown || status == siren.StatusNotFound {
		w.sendTr(chatID, false, w.tr.AddError, modelID)
		return
	}
	stmt, err := w.db.Prepare("insert into signals (chat_id, model_id) values (?,?)")
	checkErr(err)
	_, err = stmt.Exec(chatID, modelID)
	checkErr(err)
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
	if !modelIDRegexp.MatchString(modelID) {
		w.sendTr(chatID, false, w.tr.InvalidSymbols, modelID)
		return
	}
	if !w.checkExists(chatID, modelID) {
		w.sendTr(chatID, false, w.tr.ModelNotInList, modelID)
		return
	}
	stmt, err := w.db.Prepare("delete from signals where chat_id=? and model_id=?")
	checkErr(err)
	_, err = stmt.Exec(chatID, modelID)
	checkErr(err)
	w.cleanStatuses()
	w.sendTr(chatID, false, w.tr.ModelRemoved, modelID)
}

func (w *worker) cleanStatuses() {
	stmt, err := w.db.Prepare("delete from statuses where not exists(select * from signals where signals.model_id=statuses.model_id);")
	checkErr(err)
	_, err = stmt.Exec()
	checkErr(err)
}

func (w *worker) listModels(chatID int64) {
	models, err := w.db.Query(`select statuses.model_id, statuses.status
		from statuses inner join signals
		where statuses.model_id=signals.model_id and signals.chat_id=?`, chatID)
	checkErr(err)
	var lines []string
	for models.Next() {
		var modelID string
		var status siren.StatusKind
		checkErr(models.Scan(&modelID, &status))
		lines = append(lines, fmt.Sprintf(w.statusKey(status).Str, modelID))
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

	stmt, err := w.db.Prepare("insert into feedback (chat_id, text) values (?, ?)")
	checkErr(err)
	_, err = stmt.Exec(chatID, text)
	checkErr(err)
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
	chats := w.chats()
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

// nolint: gocyclo
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
				lerr("queue is full")
			}
		case statusUpdate := <-statusUpdates:
			if statusUpdate.status == siren.StatusNotFound {
				w.notFound(statusUpdate.modelID)
			}
			if w.updateStatus(statusUpdate.modelID, statusUpdate.status) {
				for _, chatID := range w.chatsForModel(statusUpdate.modelID) {
					w.reportStatus(chatID, statusUpdate.modelID, statusUpdate.status)
				}
			}
		case u := <-incoming:
			if u.Message != nil && u.Message.Chat != nil {
				linf("command: %s", u.Message.Command())
				switch u.Message.Command() {
				case "add":
					w.addModel(u.Message.Chat.ID, u.Message.CommandArguments())
				case "remove":
					w.removeModel(u.Message.Chat.ID, u.Message.CommandArguments())
				case "list":
					w.listModels(u.Message.Chat.ID)
				case "start", "help":
					w.sendTr(u.Message.Chat.ID, false, w.tr.Help)
				case "donate":
					w.sendTr(u.Message.Chat.ID, false, w.tr.Donation)
				case "feedback":
					w.feedback(u.Message.Chat.ID, u.Message.CommandArguments())
				case "stat":
					if u.Message.Chat.ID != w.cfg.AdminID {
						w.sendTr(u.Message.Chat.ID, false, w.tr.UnknownCommand)
						break
					}
					w.stat(u.Message.Chat.ID)
				case "source":
					w.sendTr(u.Message.Chat.ID, false, w.tr.SourceCode)
				case "language":
					w.sendTr(u.Message.Chat.ID, false, w.tr.Languages)
				case "broadcast":
					if u.Message.Chat.ID != w.cfg.AdminID {
						w.sendTr(u.Message.Chat.ID, false, w.tr.UnknownCommand)
						break
					}
					w.broadcast(u.Message.CommandArguments())
				case "version":
					w.sendTr(u.Message.Chat.ID, false, w.tr.Version, version)
				case "":
					w.sendTr(u.Message.Chat.ID, false, w.tr.Slash)
				default:
					w.sendTr(u.Message.Chat.ID, false, w.tr.UnknownCommand)
				}
			}
		}
	}
}
