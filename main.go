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

	tg "github.com/bcmk/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
)

type statusKind int

const (
	statusUnknown statusKind = iota
	statusOffline
	statusOnline
)

type parseKind int

const (
	raw parseKind = iota
	html
	markdown
)

var modelIDRegexp = regexp.MustCompile(`^[a-z0-9\-_]+$`)

type worker struct {
	client  *http.Client
	bot     *tg.BotAPI
	db      *sql.DB
	cfg     *config
	mu      *sync.Mutex
	elapsed time.Duration
}

type statusUpdate struct {
	modelID string
	status  statusKind
}

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}

func newWorker() *worker {
	if len(os.Args) != 2 {
		panic("usage: bonga <config>")
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
	return &worker{
		bot:    bot,
		db:     db,
		cfg:    cfg,
		client: client,
		mu:     &sync.Mutex{},
	}
}

func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func (w *worker) send(chatID int64, text string, notify bool, parse parseKind) {
	msg := tg.NewMessage(chatID, text)
	msg.DisableNotification = !notify
	switch parse {
	case html:
		msg.ParseMode = "html"
	case markdown:
		msg.ParseMode = "markdown"
	}
	if _, err := w.bot.Send(msg); err != nil {
		lerr("cannot send a message, %v", err)
	}
}

func (w *worker) checkModel(modelID string) statusKind {
	resp, err := w.client.Get(fmt.Sprintf("https://bongacams.com/%s", modelID))
	if err != nil {
		lerr("cannot send a query, %v", err)
		return statusUnknown
	}
	checkErr(resp.Body.Close())
	switch resp.StatusCode {
	case 200:
		return statusOnline
	case 302:
		return statusOffline
	}
	return statusUnknown
}

func (w *worker) createDatabase() {
	stmt, err := w.db.Prepare("create table if not exists signals (chat_id integer, model_id text);")
	checkErr(err)
	_, err = stmt.Exec()
	checkErr(err)
	stmt, err = w.db.Prepare("create table if not exists statuses (model_id text, status integer);")
	checkErr(err)
	_, err = stmt.Exec()
	checkErr(err)
	stmt, err = w.db.Prepare("create table if not exists feedback (chat_id integer, text text);")
	checkErr(err)
	_, err = stmt.Exec()
	checkErr(err)
}

func (w *worker) updateStatus(modelID string, newStatus statusKind) bool {
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
	var oldStatus statusKind
	checkErr(oldStatusQuery.Scan(&oldStatus))
	checkErr(oldStatusQuery.Close())
	stmt, err := w.db.Prepare("update statuses set status=? where model_id=?")
	checkErr(err)
	_, err = stmt.Exec(newStatus, modelID)
	checkErr(err)
	return oldStatus != newStatus
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

func (w *worker) reportStatus(chatID int64, modelID string, status statusKind) {
	switch status {
	case statusOnline:
		w.send(chatID, w.tr(online, modelID), true, raw)
	case statusOffline:
		w.send(chatID, w.tr(offline, modelID), false, raw)
	}
}

func (w *worker) startChecker() (input chan []string, output chan statusUpdate) {
	input = make(chan []string)
	output = make(chan statusUpdate)
	go func() {
		for models := range input {
			start := time.Now()
			for _, modelID := range models {
				newStatus := w.checkModel(modelID)
				if newStatus != statusUnknown {
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
		w.send(chatID, w.tr(syntaxAdd), true, html)
		return
	}
	modelID = strings.ToLower(modelID)
	if !modelIDRegexp.MatchString(modelID) {
		w.send(chatID, w.tr(invalidSymbols, modelID), true, raw)
		return
	}
	if w.checkExists(chatID, modelID) {
		w.send(chatID, w.tr(alreadyAdded, modelID), true, raw)
		return
	}
	count := w.checkMaximum(chatID)
	if count > w.cfg.MaxModels-1 {
		w.send(chatID, w.tr(maxModels, w.cfg.MaxModels), true, raw)
		return
	}
	status := w.checkModel(modelID)
	if status == statusUnknown {
		w.send(chatID, w.tr(addError, modelID), true, html)
		return
	}
	w.updateStatus(modelID, status)
	stmt, err := w.db.Prepare("insert into signals (chat_id, model_id) values (?,?)")
	checkErr(err)
	_, err = stmt.Exec(chatID, modelID)
	checkErr(err)
	w.send(chatID, w.tr(modelAdded, modelID), true, raw)
	w.reportStatus(chatID, modelID, status)
}

func (w *worker) removeModel(chatID int64, modelID string) {
	if modelID == "" {
		w.send(chatID, w.tr(syntaxRemove), true, html)
		return
	}
	modelID = strings.ToLower(modelID)
	if !modelIDRegexp.MatchString(modelID) {
		w.send(chatID, w.tr(invalidSymbols, modelID), true, raw)
		return
	}
	if !w.checkExists(chatID, modelID) {
		w.send(chatID, w.tr(modelNotInList, modelID), true, raw)
		return
	}
	stmt, err := w.db.Prepare("delete from signals where chat_id=? and model_id=?")
	checkErr(err)
	_, err = stmt.Exec(chatID, modelID)
	checkErr(err)
	w.cleanStatuses()
	w.send(chatID, w.tr(modelRemoved, modelID), true, raw)
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
	for models.Next() {
		var modelID string
		var status statusKind
		checkErr(models.Scan(&modelID, &status))
		w.reportStatus(chatID, modelID, status)
	}
}

func (w *worker) feedback(chatID int64, text string) {
	if text == "" {
		w.send(chatID, w.tr(syntaxFeedback), true, html)
		return
	}

	stmt, err := w.db.Prepare("insert into feedback (chat_id, text) values (?, ?)")
	checkErr(err)
	_, err = stmt.Exec(chatID, text)
	checkErr(err)
	w.send(chatID, w.tr(feedbackThankYou), true, raw)
	w.send(w.cfg.AdminID, fmt.Sprintf("Feedback: %s", text), true, raw)
}

func (w *worker) stat(chatID int64) {
	query := w.db.QueryRow("select count(distinct chat_id) from signals")
	usersCount := singleInt(query)
	query = w.db.QueryRow("select count(distinct model_id) from signals")
	modelsCount := singleInt(query)
	w.mu.Lock()
	elapsed := w.elapsed
	w.mu.Unlock()
	w.send(chatID, fmt.Sprintf("Users: %d\nModels: %d\nQueries duration: %v", usersCount, modelsCount, elapsed), true, raw)
}

func (w *worker) broadcast(text string) {
	if text == "" {
		return
	}
	chats := w.chats()
	for _, chatID := range chats {
		w.send(chatID, text, true, raw)
	}
}

func (w *worker) tr(key translationKey, args ...interface{}) string {
	var str string
	switch w.cfg.LanguageCode {
	case "ru":
		str = langRu[key]
	case "en":
		str = langEn[key]
	default:
		panic("wrong language code")
	}
	return fmt.Sprintf(str, args...)
}

// nolint: gocyclo
func main() {
	w := newWorker()
	cfgString, err := json.MarshalIndent(w.cfg, "", "    ")
	checkErr(err)
	linf("config: " + string(cfgString))
	w.createDatabase()

	incoming := w.bot.ListenForWebhook(w.cfg.ListenPath)
	go func() {
		var err error
		if w.cfg.Certificate != "" && w.cfg.Key != "" {
			err = http.ListenAndServeTLS(w.cfg.ListenAddress, w.cfg.Certificate, w.cfg.Key, nil)
		} else {
			err = http.ListenAndServe(w.cfg.ListenAddress, nil)
		}
		checkErr(err)
	}()

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
					w.send(u.Message.Chat.ID, w.tr(help), false, html)
				case "donate":
					w.send(u.Message.Chat.ID, w.tr(donation), false, raw)
				case "feedback":
					w.feedback(u.Message.Chat.ID, u.Message.CommandArguments())
				case "stat":
					if u.Message.Chat.ID != w.cfg.AdminID {
						w.send(u.Message.Chat.ID, w.tr(unknownCommand), false, raw)
						break
					}
					w.stat(u.Message.Chat.ID)
				case "source":
					w.send(u.Message.Chat.ID, w.tr(sourceCode), false, raw)
				case "language":
					w.send(u.Message.Chat.ID, w.tr(languages), false, raw)
				case "broadcast":
					if u.Message.Chat.ID != w.cfg.AdminID {
						w.send(u.Message.Chat.ID, w.tr(unknownCommand), false, raw)
						break
					}
					w.broadcast(u.Message.CommandArguments())
				default:
					w.send(u.Message.Chat.ID, w.tr(unknownCommand), false, raw)
				}
			}
		}
	}
}
