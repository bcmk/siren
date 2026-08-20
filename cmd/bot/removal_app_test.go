package main

import (
	htmltemplate "html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/bcmk/siren/v4/internal/db"
	"github.com/bcmk/siren/v4/lib/cmdlib"
)

// TestHandleWebAppRemoveValidates holds the removal submit to the shape the add has:
// a POST alone, a named streamer, and the loop's verdict deciding the status.
func TestHandleWebAppRemoveValidates(t *testing.T) {
	t.Parallel()
	const botToken = "123:test-token"
	w := &worker{
		cfg:                  searchConfig(t, botToken, nil),
		webAppRemoveRequests: make(chan webAppRemoveRequest, 1),
		shutdownCh:           make(chan struct{}),
	}

	send := func(method, streamer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method,
			"/apps/remove/api/submit?endpoint=test&streamer="+streamer, nil)
		r.Header.Set("X-Init-Data", initDataFor(botToken, 1))
		rw := httptest.NewRecorder()
		w.handleWebAppRemove(rw, r)
		return rw
	}
	post := func(streamer string) *httptest.ResponseRecorder { return send(http.MethodPost, streamer) }

	if rw := send(http.MethodGet, "some_model"); rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("a GET got %d, want %d", rw.Code, http.StatusMethodNotAllowed)
	}
	if rw := post(""); rw.Code != http.StatusBadRequest {
		t.Errorf("an empty streamer got %d, want %d", rw.Code, http.StatusBadRequest)
	}
	if queued := len(w.webAppRemoveRequests); queued != 0 {
		t.Errorf("a refused request still queued %d removals", queued)
	}
	// No main loop is running, so the test answers in its place.
	serve := func(streamer string, admit bool) (*httptest.ResponseRecorder, string) {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- post(streamer) }()
		var got string
		select {
		case req := <-w.webAppRemoveRequests:
			got = req.nickname
			if req.chatID != 1 {
				t.Errorf("queued chat %d, want 1", req.chatID)
			}
			req.admittedCh <- admit
		case <-time.After(time.Second):
			t.Fatal("a valid removal never reached the loop")
		}
		select {
		case rw := <-done:
			return rw, got
		case <-time.After(time.Second):
			t.Fatal("the handler never answered")
			return nil, ""
		}
	}

	rw, nickname := serve("Some_Model", true)
	if rw.Code != http.StatusOK {
		t.Errorf("an admitted removal got %d, want %d", rw.Code, http.StatusOK)
	}
	if nickname != "some_model" {
		t.Errorf("queued nickname = %q, want some_model", nickname)
	}
	if rw, _ := serve("some_model", false); rw.Code != http.StatusForbidden {
		t.Errorf("an unvetted chat got %d, want %d", rw.Code, http.StatusForbidden)
	}
}

// TestHandleWebAppRemovalListAnswersFromTheLoop: the list is one chat's subscriptions,
// which the URL alone does not name, so the endpoint takes a POST alone
// and answers with what the loop serves, a refusal reading as forbidden.
func TestHandleWebAppRemovalListAnswersFromTheLoop(t *testing.T) {
	t.Parallel()
	const botToken = "123:test-token"
	w := &worker{
		cfg:                       searchConfig(t, botToken, nil),
		webAppRemovalListRequests: make(chan webAppRemovalListRequest, 1),
		shutdownCh:                make(chan struct{}),
	}

	send := func(method string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/apps/remove/api/list?endpoint=test", nil)
		r.Header.Set("X-Init-Data", initDataFor(botToken, 1))
		rw := httptest.NewRecorder()
		w.handleWebAppRemovalList(rw, r)
		return rw
	}

	if rw := send(http.MethodGet); rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("a GET got %d, want %d", rw.Code, http.StatusMethodNotAllowed)
	}
	if queued := len(w.webAppRemovalListRequests); queued != 0 {
		t.Errorf("a GET still queued %d list requests", queued)
	}
	// No main loop is running, so the test answers in its place.
	serve := func(answer webAppRemovalListResult) *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- send(http.MethodPost) }()
		select {
		case req := <-w.webAppRemovalListRequests:
			if req.chatID != 1 {
				t.Errorf("queued chat %d, want 1", req.chatID)
			}
			req.resultCh <- answer
		case <-time.After(time.Second):
			t.Fatal("a valid request never reached the loop")
		}
		select {
		case rw := <-done:
			return rw
		case <-time.After(time.Second):
			t.Fatal("the handler never answered")
			return nil
		}
	}

	rw := serve(webAppRemovalListResult{nicknames: []string{"a_model", "b_model"}, allowed: true})
	if rw.Code != http.StatusOK {
		t.Errorf("a served list got %d, want %d", rw.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(rw.Body.String()); got != `["a_model","b_model"]` {
		t.Errorf("list body = %q", got)
	}
	// A chat with no row answers nil, which the page must read as an empty list, not null.
	rw = serve(webAppRemovalListResult{allowed: true})
	if got := strings.TrimSpace(rw.Body.String()); got != `[]` {
		t.Errorf("empty list body = %q", got)
	}
	if rw := serve(webAppRemovalListResult{}); rw.Code != http.StatusForbidden {
		t.Errorf("an unvetted chat got %d, want %d", rw.Code, http.StatusForbidden)
	}
}

// The main loop is the only reader of the removal app's requests,
// so a request arriving after it stops must be answered, not parked.
// The channels are unbuffered here, as newWorker builds them:
// a slot free would let the send arm win and leave the shutdown arm unrun.
func TestWebAppRemoveGivesUpOnShutdown(t *testing.T) {
	t.Parallel()
	gone := &worker{
		webAppRemovalListRequests: make(chan webAppRemovalListRequest),
		webAppRemoveRequests:      make(chan webAppRemoveRequest),
		shutdownCh:                make(chan struct{}),
	}
	close(gone.shutdownCh)
	// The verdicts come back on a channel, never through t:
	// a parked submit outlives the test, and touching t then panics
	// over the very failure the timeout is reporting.
	type outcome struct{ admitted, alive bool }
	got := make(chan outcome, 1)
	go func() {
		admitted, alive := gone.submitWebAppRemove(webAppRemoveRequest{
			endpoint:   "test",
			chatID:     1,
			nickname:   "some_model",
			admittedCh: make(chan bool, 1),
		})
		got <- outcome{admitted: admitted, alive: alive}
	}()
	select {
	case o := <-got:
		if o.alive || o.admitted {
			t.Errorf("a removal after shutdown reported alive=%v admitted=%v", o.alive, o.admitted)
		}
	case <-time.After(time.Second):
		t.Fatal("the removal parked instead of giving up")
	}
	listGot := make(chan bool, 1)
	go func() {
		_, alive := gone.submitWebAppRemovalList(webAppRemovalListRequest{
			endpoint: "test",
			chatID:   1,
			resultCh: make(chan webAppRemovalListResult, 1),
		})
		listGot <- alive
	}()
	select {
	case alive := <-listGot:
		if alive {
			t.Error("a list request after shutdown reported the loop alive")
		}
	case <-time.After(time.Second):
		t.Fatal("the list request parked instead of giving up")
	}
}

// TestRemovalAppPageRenders: the page is served bare, its list fetched by the page itself,
// so an endpoint the bot does not serve is the page's one refusal.
func TestRemovalAppPageRenders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		endpoint string
		wantCode int
	}{
		{"the served page", "test", http.StatusOK},
		{"an unknown endpoint", "nosuch", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := searchConfig(t, "123:test-token", nil)
			w := &worker{
				cfg: cfg,
				tr:  map[string]*cmdlib.Translations{"test": &testTranslations},
			}
			var err error
			w.removalHTML, err = htmltemplate.ParseFiles(
				filepath.Join("..", "..", "res", "webapp", "removal.html"))
			if err != nil {
				t.Fatalf("cannot parse the removal page: %v", err)
			}
			r := httptest.NewRequest(http.MethodGet, "/apps/remove?endpoint="+tc.endpoint, nil)
			rw := httptest.NewRecorder()
			w.handleRemovalApp(rw, r)
			if rw.Code != tc.wantCode {
				t.Fatalf("the page got %d, want %d", rw.Code, tc.wantCode)
			}
			if tc.wantCode != http.StatusOK {
				return
			}
			body := rw.Body.String()
			for _, want := range []string{
				testTranslations.RemovalAppHeader.Str,
				testTranslations.RemovalAppNoSubscriptions.Str,
				testTranslations.RemovalAppFailedToRemove.Str,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("the page misses %q", want)
				}
			}
		})
	}
}

// TestPerformWebAppRemovalListIsWhitelistGated holds the list to the gate every inbound path has,
// and to its nature as a read: no user row is materialized and no command is logged.
func TestPerformWebAppRemovalListIsWhitelistGated(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()
	w.initCache()
	// Every worker shares &testConfig, so copy before changing it.
	cfg := testConfig
	cfg.WhitelistChats = []int64{5}
	w.cfg = &cfg

	ask := func(chatID int64) webAppRemovalListResult {
		resultCh := make(chan webAppRemovalListResult, 1)
		w.performWebAppRemovalList(webAppRemovalListRequest{
			endpoint: "test",
			chatID:   chatID,
			resultCh: resultCh,
		})
		return <-resultCh
	}

	if res := ask(999); res.allowed {
		t.Error("an outsider was served the list")
	}

	// A vetted chat the bot never met: an empty list, and still no row behind it.
	res := ask(5)
	if !res.allowed || len(res.nicknames) != 0 {
		t.Errorf("an unknown chat got allowed=%v, nicknames=%q", res.allowed, res.nicknames)
	}
	if users := w.db.MustInt("select count(*) from users"); users != 0 {
		t.Errorf("a list read materialized %d users", users)
	}

	// The removable set is what /remove accepts: subscriptions and pending alike.
	insertTestStreamer(&w.db, db.Streamer{Nickname: "b_model"})
	insertSubscription(&w.db, "test", 5, "b_model")
	insertPendingSubscription(&w.db, "test", 5, "a_model", false)
	res = ask(5)
	if !slices.Equal(res.nicknames, []string{"a_model", "b_model"}) {
		t.Errorf("nicknames = %q, want [a_model b_model]", res.nicknames)
	}
	received := commandsInLog(w, "select command from received_message_log", nil)
	if len(received) != 0 {
		t.Errorf("a list read logged %q", received)
	}
}

// TestPerformWebAppRemoveIsWhitelistGated holds the removal to the gate every inbound path has:
// an unvetted chat leaves no trace, a vetted one loses the subscription and answers in the chat.
func TestPerformWebAppRemoveIsWhitelistGated(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		whitelist []int64
		chatID    int64
		admitted  bool
	}{
		{"outsider is refused", []int64{1}, 999, false},
		{"empty whitelist admits", nil, 5, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.initCache()
			// Every worker shares &testConfig, so copy before changing it.
			cfg := testConfig
			cfg.WhitelistChats = c.whitelist
			w.cfg = &cfg

			insertTestStreamer(&w.db, db.Streamer{Nickname: "known_model"})
			insertSubscription(&w.db, "test", c.chatID, "known_model")

			admitted := make(chan bool, 1)
			w.performWebAppRemove(webAppRemoveRequest{
				endpoint:   "test",
				chatID:     c.chatID,
				nickname:   "known_model",
				admittedCh: admitted,
			})
			if got := <-admitted; got != c.admitted {
				t.Errorf("admitted = %v, want %v", got, c.admitted)
			}

			received := commandsInLog(w, "select command from received_message_log", nil)
			subs := w.db.MustInt("select count(*) from subscriptions")
			if !c.admitted {
				sent := drainSendQueueToLog(t, w)
				if len(received) != 0 || len(sent) != 0 || subs != 1 {
					t.Errorf("an unvetted removal left a trace: "+
						"received %q, sent %q, subscriptions %d", received, sent, subs)
				}
				return
			}
			if !slices.Equal(received, []string{webAppRemoveCommand}) || subs != 0 {
				t.Errorf("an admitted removal left the wrong trace: "+
					"received %q, subscriptions %d", received, subs)
			}
			if sent := drainSendQueueToLog(t, w); !slices.Equal(sent, []string{webAppRemoveCommand}) {
				t.Errorf("the removal did not answer in the chat, got %q", sent)
			}
		})
	}
}

// TestSyntaxRemoveOffersTheRemovalApp: the app is a way to remove,
// so it rides with the bare command's syntax help,
// and only in a private chat, the one place a web app button opens.
// The URL is asserted whole, as the timezone picker's is.
func TestSyntaxRemoveOffersTheRemovalApp(t *testing.T) {
	t.Parallel()
	const domain = "bot.example.invalid"
	tests := []struct {
		name    string
		chatID  int64
		wantURL string
	}{
		{"a private chat", 10, "https://" + domain + "/apps/remove?endpoint=test"},
		{"a group asking", -10, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.initCache()
			// A real Endpoints map: its value type is unexported, so it cannot be built by hand,
			// and a nil one would assert the button against the very URL this guards against.
			cfg := searchConfig(t, "123:test-token", nil)
			endpoint := cfg.Endpoints["test"]
			endpoint.WebhookDomain = domain
			cfg.Endpoints["test"] = endpoint
			w.cfg = cfg

			m := testMessage(w, tc.chatID, "remove", 100)
			w.removeStreamer(m, "")
			queued := w.sendQueue.pop()
			markup, _ := queued.message.(*messageParams).ReplyMarkup.(*models.InlineKeyboardMarkup)
			if tc.wantURL == "" {
				if markup != nil {
					t.Errorf("got a removal button that cannot open: %+v", markup)
				}
				return
			}
			if markup == nil {
				t.Fatal("got no removal button")
			}
			webApp := markup.InlineKeyboard[0][0].WebApp
			if webApp == nil {
				t.Fatal("the removal button opens no web app")
			}
			if webApp.URL != tc.wantURL {
				t.Errorf("button URL = %q, want %q", webApp.URL, tc.wantURL)
			}
		})
	}
}
