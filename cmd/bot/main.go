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
	clients       []*lib.Client
	bot           *tg.BotAPI
	db            *sql.DB
	cfg           *config
	mu            *sync.Mutex
	elapsed       time.Duration
	tr            translations
	checkModel    func(client *lib.Client, modelID string, headers [][2]string, dbg bool) lib.StatusKind
	sendTGMessage func(msg tg.Chattable) (tg.Message, error)
	unknowns      []bool
	unknownsPos   int
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

	bot, err := tg.NewBotAPIWithClient(cfg.BotToken, clients[0].Client)
	checkErr(err)
	db, err := sql.Open("sqlite3", cfg.DBPath)
	checkErr(err)
	w := &worker{
		bot:           bot,
		db:            db,
		cfg:           cfg,
		clients:       clients,
		mu:            &sync.Mutex{},
		tr:            loadTranslations(cfg.Translation),
		sendTGMessage: bot.Send,
		unknowns:      make([]bool, cfg.errorDenominator),
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
	if w.cfg.WebhookDomain == "" {
		return
	}

	linf("setting webhook...")
	if w.cfg.CertificatePath == "" {
		var _, err = w.bot.SetWebhook(tg.NewWebhook(path.Join(w.cfg.WebhookDomain, w.cfg.ListenPath)))
		checkErr(err)
	} else {
		var _, err = w.bot.SetWebhook(tg.NewWebhookWithCert(path.Join(w.cfg.WebhookDomain, w.cfg.ListenPath), w.cfg.CertificatePath))
		checkErr(err)
	}
	linf("OK")

	info, err := w.bot.GetWebhookInfo()
	checkErr(err)
	if info.LastErrorDate != 0 {
		linf("last webhook error time: %v", time.Unix(int64(info.LastErrorDate), 0))
	}
	if info.LastErrorMessage != "" {
		linf("last webhook error message: %s", info.LastErrorMessage)
	}
}

func (w *worker) mustExec(query string, args ...interface{}) {
	stmt, err := w.db.Prepare(query)
	checkErr(err)
	_, err = stmt.Exec(args...)
	checkErr(err)
	stmt.Close()
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
				linf("bot is blocked by the user %d, %v", chatID, err)
				w.incrementBlock(chatID)
			} else {
				lerr("cannot send a message to %d, code %d, %v", chatID, err.Code, err)
			}
		default:
			lerr("unexpected error type while sending a message to %d, %v", chatID, err)
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
	defer modelsQuery.Close()
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
	defer chatsQuery.Close()
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
	defer chatsQuery.Close()
	for chatsQuery.Next() {
		var chatID int64
		checkErr(chatsQuery.Scan(&chatID))
		chats = append(chats, chatID)
	}
	return
}

func (w *worker) statusesForChat(chatID int64) []statusUpdate {
	statusesQuery, err := w.db.Query(`select statuses.model_id, statuses.status
		from statuses inner join signals
		on statuses.model_id=signals.model_id
		where signals.chat_id=?
		order by statuses.model_id`, chatID)
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

func (w *worker) statusKey(status lib.StatusKind) *translation {
	switch status {
	case lib.StatusOnline:
		return w.tr.OnlineList
	case lib.StatusDenied:
		return w.tr.DeniedList
	default:
		return w.tr.OfflineList
	}
}

func (w *worker) reportStatus(chatID int64, modelID string, status lib.StatusKind) {
	switch status {
	case lib.StatusOnline:
		w.sendTr(chatID, true, w.tr.Online, modelID)
	case lib.StatusOffline:
		w.sendTr(chatID, false, w.tr.Offline, modelID)
	case lib.StatusDenied:
		w.sendTr(chatID, false, w.tr.Denied, modelID)
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
	status := w.checkModel(w.clients[0], modelID, w.cfg.Headers, w.cfg.Debug)
	if status == lib.StatusUnknown || status == lib.StatusNotFound {
		w.sendTr(chatID, false, w.tr.AddError, modelID)
		return
	}
	w.mustExec("insert into signals (chat_id, model_id) values (?,?)", chatID, modelID)
	w.updateStatus(modelID, status)
	if status != lib.StatusDenied {
		w.sendTr(chatID, false, w.tr.ModelAdded, modelID)
	}
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
	w.send(chatID, false, w.tr.NoModels.Parse, strings.Join(lines, "\n"))
}

func (w *worker) listOnlineModels(chatID int64) {
	statuses := w.statusesForChat(chatID)
	online := 0
	for _, s := range statuses {
		if s.status == lib.StatusOnline {
			w.sendTr(chatID, false, w.tr.Online, s.modelID)
			online++
		}
	}
	if online == 0 {
		w.sendTr(chatID, false, w.tr.NoOnlineModels)
	}
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

func (w *worker) unknownsNumber() int {
	var errors = 0
	for _, s := range w.unknowns {
		if s {
			errors += 1
		}
	}
	return errors
}

func (w *worker) usersCount() int {
	query := w.db.QueryRow("select count(distinct chat_id) from signals")
	return singleInt(query)
}

func (w *worker) activeUsersCount() int {
	query := w.db.QueryRow(
		`select count(distinct signals.chat_id) from signals
		left join users on signals.chat_id=users.chat_id
		where users.block is null or users.block = 0`)
	return singleInt(query)
}

func (w *worker) modelsCount() int {
	query := w.db.QueryRow("select count(distinct model_id) from signals")
	return singleInt(query)
}

func (w *worker) modelsToQueryCount() int {
	query := w.db.QueryRow(
		`select count(distinct signals.model_id) from signals
		left join users on signals.chat_id=users.chat_id
		where users.block is null or users.block < ?`,
		w.cfg.BlockThreshold)
	return singleInt(query)
}

func (w *worker) onlineModelsCount() int {
	query := w.db.QueryRow(`
		select count(distinct signals.model_id) from signals
		join statuses on signals.model_id=statuses.model_id
		left join users on signals.chat_id=users.chat_id
		where statuses.status=2 and (users.block is null or users.block < ?)`,
		w.cfg.BlockThreshold)
	return singleInt(query)
}

func (w *worker) heavyUsersCount() int {
	query := w.db.QueryRow(`
		select count(*) from (
			select 1
			from signals left join users
			on signals.chat_id=users.chat_id
			where users.block is null or users.block = 0
			group by signals.chat_id
			having count(*) >= 7);`)
	return singleInt(query)
}

func (w *worker) statStrings() []string {
	stat := w.getStat()
	return []string{
		fmt.Sprintf("Users: %d", stat.UsersCount),
		fmt.Sprintf("Active users: %d", stat.ActiveUsersCount),
		fmt.Sprintf("Heavy: %d", stat.HeavyUsersCount),
		fmt.Sprintf("Models: %d", stat.ModelsCount),
		fmt.Sprintf("Models to query: %d", stat.ModelsToQueryCount),
		fmt.Sprintf("Queries duration: %d s", stat.QueriesDurationSeconds),
		fmt.Sprintf("Error rate: %d/%d", stat.ErrorRate[0], stat.ErrorRate[1]),
		fmt.Sprintf("Memory usage: %d KiB", stat.Rss),
	}
}

func (w *worker) stat(chatID int64) {
	w.send(chatID, true, parseRaw, strings.Join(w.statStrings(), "\n"))
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
	if w.cfg.CertificatePath != "" && w.cfg.CertificateKeyPath != "" {
		err = http.ListenAndServeTLS(w.cfg.ListenAddress, w.cfg.CertificatePath, w.cfg.CertificateKeyPath, nil)
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

func (w *worker) processIncomingCommand(chatID int64, command, arguments string) {
	w.resetBlock(chatID)
	command = strings.ToLower(command)
	linf("chat: %d, command: %s %s", chatID, command, arguments)

	if chatID == w.cfg.AdminID && w.processAdminMessage(chatID, command, arguments) {
		return
	}

	switch command {
	case "add":
		arguments = strings.Replace(arguments, "—", "--", -1)
		w.addModel(chatID, arguments)
	case "remove":
		arguments = strings.Replace(arguments, "—", "--", -1)
		w.removeModel(chatID, arguments)
	case "list":
		w.listModels(chatID)
	case "online":
		w.listOnlineModels(chatID)
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

func (w *worker) processPeriodic(statusRequests chan []string) {
	var unknownsNumber = w.unknownsNumber()
	if unknownsNumber > w.cfg.errorThreshold {
		w.send(w.cfg.AdminID, true, parseRaw, fmt.Sprintf("Dangerous error rate reached: %d/%d", unknownsNumber, w.cfg.errorDenominator))
	}

	select {
	case statusRequests <- w.models():
	default:
		linf("the queue is full")
	}
}

func (w *worker) processStatusUpdate(statusUpdate statusUpdate) {
	if statusUpdate.status == lib.StatusNotFound {
		w.notFound(statusUpdate.modelID)
	}
	if statusUpdate.status != lib.StatusUnknown && w.updateStatus(statusUpdate.modelID, statusUpdate.status) {
		if w.cfg.Debug {
			ldbg("reporting status of the model %s", statusUpdate.modelID)
		}
		for _, chatID := range w.chatsForModel(statusUpdate.modelID) {
			w.reportStatus(chatID, statusUpdate.modelID, statusUpdate.status)
		}
	}
	w.unknowns[w.unknownsPos] = statusUpdate.status == lib.StatusUnknown
	w.unknownsPos = (w.unknownsPos + 1) % w.cfg.errorDenominator
}

func (w *worker) processTGUpdate(u tg.Update) {
	if u.Message != nil && u.Message.Chat != nil {
		if u.Message.IsCommand() {
			w.processIncomingCommand(u.Message.Chat.ID, u.Message.Command(), u.Message.CommandArguments())
		} else {
			parts := strings.SplitN(u.Message.Text, " ", 2)
			for len(parts) < 2 {
				parts = append(parts, "")
			}
			w.processIncomingCommand(u.Message.Chat.ID, parts[0], parts[1])
		}
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

func (w *worker) getStat() statistics {
	w.mu.Lock()
	elapsed := w.elapsed
	w.mu.Unlock()

	rss, err := getRss()
	checkErr(err)
	var rusage syscall.Rusage
	checkErr(syscall.Getrusage(syscall.RUSAGE_SELF, &rusage))

	return statistics{
		UsersCount:             w.usersCount(),
		ActiveUsersCount:       w.activeUsersCount(),
		HeavyUsersCount:        w.heavyUsersCount(),
		ModelsCount:            w.modelsCount(),
		ModelsToQueryCount:     w.modelsToQueryCount(),
		OnlineModelsCount:      w.onlineModelsCount(),
		QueriesDurationSeconds: int(elapsed.Seconds()),
		ErrorRate:              [2]int{w.unknownsNumber(), w.cfg.errorDenominator},
		Rss:                    rss / 1024,
		MaxRss:                 rusage.Maxrss}
}

func (w *worker) handleStat(writer http.ResponseWriter, r *http.Request) {
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
	statJson, err := json.MarshalIndent(w.getStat(), "", "    ")
	checkErr(err)
	writer.Write(statJson)
}

func (w *worker) logStat() {
	linf("stat, %s", strings.Join(w.statStrings(), ", "))
}

func main() {
	w := newWorker()
	w.logConfig()
	w.setWebhook()
	w.createDatabase()

	incoming := w.bot.ListenForWebhook(w.cfg.ListenPath)
	http.HandleFunc("/stat", w.handleStat)
	go w.serve()
	var periodicTimer = time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second)
	var statTimer = time.NewTicker(time.Duration(w.cfg.StatLogPeriodSeconds) * time.Second)
	statusRequests, statusUpdates := w.startChecker()
	statusRequests <- w.models()
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT, syscall.SIGKILL)
	for {
		select {
		case <-periodicTimer.C:
			runtime.GC()
			w.processPeriodic(statusRequests)
		case <-statTimer.C:
			w.logStat()
		case statusUpdate := <-statusUpdates:
			w.processStatusUpdate(statusUpdate)
		case u := <-incoming:
			w.processTGUpdate(u)
		case s := <-signals:
			linf("got signal %v", s)
			return
		}
	}
}
