package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	tgbotapi "github.com/bcmk/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
)

type statusKind int

const (
	statusUnknown = iota
	statusOffline
	statusOnline
)

type worker struct {
	client *http.Client
	bot    *tgbotapi.BotAPI
	db     *sql.DB
	cfg    *config
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
	cfg := readConfig()
	client := &http.Client{
		CheckRedirect: noRedirect,
		Timeout:       time.Second * time.Duration(cfg.TimeoutSeconds),
	}
	bot, err := tgbotapi.NewBotAPIWithClient(cfg.BotToken, client)
	checkErr(err)
	db, err := sql.Open("sqlite3", "./bonga.db")
	checkErr(err)
	return &worker{
		bot:    bot,
		db:     db,
		cfg:    cfg,
		client: client,
	}
}

func noRedirect(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

func (w *worker) send(chatID int64, text string, notify bool) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableNotification = !notify
	msg.ParseMode = "html"
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

func (w *worker) statusMap() map[string]statusKind {
	statuses, err := w.db.Query("select model_id, status from statuses")
	checkErr(err)
	statusMap := make(map[string]statusKind)
	for statuses.Next() {
		var modelID string
		var status statusKind
		checkErr(statuses.Scan(&modelID, &status))
		statusMap[modelID] = status
	}
	return statusMap
}

func (w *worker) models() (models []string) {
	modelsQuery, err := w.db.Query("select distinct(model_id) from signals")
	checkErr(err)
	for modelsQuery.Next() {
		var modelID string
		checkErr(modelsQuery.Scan(&modelID))
		models = append(models, modelID)
	}
	return
}

func (w *worker) chats(modelID string) (chats []int64) {
	chatsQuery, err := w.db.Query("select chat_id from signals where model_id=?", modelID)
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
		w.send(chatID, fmt.Sprintf("%s в сети", modelID), true)
	case statusOffline:
		w.send(chatID, fmt.Sprintf("%s не в сети", modelID), false)
	}
}

func (w *worker) startChecker() (input chan []string, output chan statusUpdate) {
	input = make(chan []string)
	output = make(chan statusUpdate)
	go func() {
		for models := range input {
			for _, modelID := range models {
				newStatus := w.checkModel(modelID)
				if newStatus == statusUnknown {
					continue
				}
				output <- statusUpdate{modelID: modelID, status: newStatus}
			}
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
		w.send(chatID, "Формат команды: /add <i>идентификатор модели</i>", true)
		w.send(chatID, "Идентификатор модели можно посмотреть в адресной строке браузера", true)
		return
	}

	re := regexp.MustCompile(`^[A-Za-z0-9\-_]+$`)
	if !re.MatchString(modelID) {
		w.send(chatID, fmt.Sprintf("Идентификатор модели %s содержит неподдерживаемые символы", modelID), true)
		return
	}

	exists := w.checkExists(chatID, modelID)
	if exists {
		w.send(chatID, fmt.Sprintf("Модель %s уже в вашем списке", modelID), true)
		return
	}
	count := w.checkMaximum(chatID)
	if count > w.cfg.MaxModels-1 {
		w.send(chatID, fmt.Sprintf("Можно добавить не более %d моделей", w.cfg.MaxModels), true)
		return
	}
	status := w.checkModel(modelID)
	if status == statusUnknown {
		w.send(chatID, fmt.Sprintf("Не получилось добавить модель %s", modelID), true)
		w.send(chatID, "Проверьте ID модели или попробуйте позже", true)
		w.send(chatID, "Формат команды: /add <i>идентификатор модели</i>", true)
		w.send(chatID, "Идентификатор модели можно посмотреть в адресной строке браузера", true)
		return
	}
	w.updateStatus(modelID, status)
	stmt, err := w.db.Prepare("insert into signals (chat_id, model_id) values (?,?)")
	checkErr(err)
	_, err = stmt.Exec(chatID, modelID)
	checkErr(err)
	w.send(chatID, fmt.Sprintf("Модель %s добавлена", modelID), true)
	w.reportStatus(chatID, modelID, status)
}

func (w *worker) removeModel(chatID int64, modelID string) {
	if modelID == "" {
		w.send(chatID, "Формат команды: /remove <i>идентификатор модели</i>", true)
		w.send(chatID, "Идентификатор модели можно посмотреть в адресной строке браузера", true)
		return
	}

	re := regexp.MustCompile(`^[A-Za-z0-9\-_]+$`)
	if !re.MatchString(modelID) {
		w.send(chatID, fmt.Sprintf("Идентификатор модели %s содержит неподдерживаемые символы", modelID), true)
		return
	}

	if !w.checkExists(chatID, modelID) {
		w.send(chatID, fmt.Sprintf("Модель %s не в вашем списке", modelID), true)
		return
	}
	stmt, err := w.db.Prepare("delete from signals where chat_id=? and model_id=?")
	checkErr(err)
	_, err = stmt.Exec(chatID, modelID)
	checkErr(err)
	w.cleanStatuses()
	w.send(chatID, fmt.Sprintf("Модель %s удалена", modelID), true)
}

func (w *worker) cleanStatuses() {
	stmt, err := w.db.Prepare("delete from statuses where not exists(select * from signals where signals.model_id=statuses.model_id);")
	checkErr(err)
	_, err = stmt.Exec()
	checkErr(err)
}

func (w *worker) listModels(chatID int64) {
	models, err := w.db.Query("select model_id from signals where chat_id=?", chatID)
	checkErr(err)
	statusMap := w.statusMap()
	for models.Next() {
		var modelID string
		checkErr(models.Scan(&modelID))
		w.reportStatus(chatID, modelID, statusMap[modelID])
	}
}

func (w *worker) donate(chatID int64) {
	w.send(chatID,
		`Хотите поддержать проект?
Bitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE
Ethereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc
Если вы не знаете, что это такое, просто подарите моей любимой модели BBWebb 77тк`, true)
}

func (w *worker) feedback(chatID int64, text string) {
	if text == "" {
		w.send(chatID, "Формат команды: /feedback <i>сообщение</i>", true)
		return
	}

	stmt, err := w.db.Prepare("insert into feedback (chat_id, text) values (?, ?)")
	checkErr(err)
	_, err = stmt.Exec(chatID, text)
	checkErr(err)
	w.send(chatID, "Спасибо за обратную связь!", true)
}

func (w *worker) stat(chatID int64) {
	chats := w.db.QueryRow("select count(distinct chat_id) from signals;")
	count := singleInt(chats)
	w.send(chatID, fmt.Sprintf("Пользователей %v", count), true)
}

// nolint: gocyclo
func main() {
	w := newWorker()
	scfg, err := json.MarshalIndent(w.cfg, "", "    ")
	checkErr(err)
	linf("config: " + string(scfg))
	w.createDatabase()

	updates := w.bot.ListenForWebhook("/" + w.bot.Token)
	go func() {
		err := http.ListenAndServeTLS(":443", "server.pem", "server.key", nil)
		checkErr(err)
	}()

	var periodicTimer = time.NewTicker(time.Duration(w.cfg.PeriodSeconds) * time.Second)
	statusRequests, statusUpdates := w.startChecker()
	statusRequests <- w.models()
	for {
		select {
		case <-periodicTimer.C:
			statusRequests <- w.models()
		case statusUpdate := <-statusUpdates:
			if w.updateStatus(statusUpdate.modelID, statusUpdate.status) {
				for _, chatID := range w.chats(statusUpdate.modelID) {
					w.reportStatus(chatID, statusUpdate.modelID, statusUpdate.status)
				}
			}
		case u := <-updates:
			if u.Message != nil && u.Message.Chat != nil {
				linf("command: %s", u.Message.Command())
				switch u.Message.Command() {
				case "add":
					w.addModel(u.Message.Chat.ID, u.Message.CommandArguments())
				case "remove":
					w.removeModel(u.Message.Chat.ID, u.Message.CommandArguments())
				case "list":
					w.listModels(u.Message.Chat.ID)
				case "donate", "start":
					w.donate(u.Message.Chat.ID)
				case "feedback":
					w.feedback(u.Message.Chat.ID, u.Message.CommandArguments())
				case "stat":
					w.stat(u.Message.Chat.ID)
				case "source":
					w.send(u.Message.Chat.ID, "Исходный код: https://github.com/bcmk/bcb", true)
				default:
					w.send(u.Message.Chat.ID, "Такой команде не обучен", true)
				}
			}
		}
	}
}
