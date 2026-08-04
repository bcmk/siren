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

	"github.com/bcmk/siren/v4/lib/cmdlib"
)

// testZoneNames stands in for what initTimezones loads,
// so a worker built without a database behind it can still resolve a name.
// The zones are loaded here as setZoneNames loads them, once.
var testZoneNames = map[string]*time.Location{
	"europe/berlin": mustLoadZone("Europe/Berlin"),
	"utc":           time.UTC,
}

func mustLoadZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// TestHandleWebAppTimezoneValidates holds the page to a proposal:
// the bot resolves what it submits, so a name it cannot load never reaches the loop.
// The endpoint answers a POST alone.
func TestHandleWebAppTimezoneValidates(t *testing.T) {
	t.Parallel()
	const botToken = "123:test-token"
	w := &worker{
		cfg:                    searchConfig(t, botToken, nil),
		zoneNames:              testZoneNames,
		webAppTimezoneRequests: make(chan webAppTimezoneRequest, 1),
		shutdownCh:             make(chan struct{}),
	}

	send := func(method, zone string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method,
			"/apps/timezone/api/submit?endpoint=test&timezone="+zone, nil)
		r.Header.Set("X-Init-Data", initDataFor(botToken, 1))
		rw := httptest.NewRecorder()
		w.handleWebAppTimezone(rw, r)
		return rw
	}
	post := func(zone string) *httptest.ResponseRecorder { return send(http.MethodPost, zone) }

	if rw := send(http.MethodGet, "Europe/Berlin"); rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("a GET got %d, want %d", rw.Code, http.StatusMethodNotAllowed)
	}
	if rw := post("Europe/Atlantis"); rw.Code != http.StatusBadRequest {
		t.Errorf("an unknown zone got %d, want %d", rw.Code, http.StatusBadRequest)
	}
	if queued := len(w.webAppTimezoneRequests); queued != 0 {
		t.Errorf("an unknown zone still queued %d requests", queued)
	}
	// No main loop is running, so the test answers in its place.
	serve := func(zone string, admit bool) (*httptest.ResponseRecorder, string) {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- post(zone) }()
		var got string
		select {
		case req := <-w.webAppTimezoneRequests:
			got = req.zone
			if req.chatID != 1 {
				t.Errorf("queued chat %d, want 1", req.chatID)
			}
			req.admittedCh <- admit
		case <-time.After(time.Second):
			t.Fatal("a valid zone never reached the loop")
		}
		select {
		case rw := <-done:
			return rw, got
		case <-time.After(time.Second):
			t.Fatal("the handler never answered")
			return nil, ""
		}
	}

	rw, zone := serve("europe%2Fberlin", true)
	if rw.Code != http.StatusOK {
		t.Errorf("a saved zone got %d, want %d", rw.Code, http.StatusOK)
	}
	if zone != "Europe/Berlin" {
		t.Errorf("queued zone = %q, want Europe/Berlin", zone)
	}
	if rw, _ := serve("Europe%2FBerlin", false); rw.Code != http.StatusForbidden {
		t.Errorf("an unvetted chat got %d, want %d", rw.Code, http.StatusForbidden)
	}
}

// The main loop is the only reader of the picker's requests,
// so a submit arriving after it stops must be answered, not parked.
// The channel is unbuffered here, as newWorker builds it:
// a slot free would let the send arm win and leave the shutdown arm unrun.
func TestWebAppTimezoneGivesUpOnShutdown(t *testing.T) {
	t.Parallel()
	newReq := func() webAppTimezoneRequest {
		return webAppTimezoneRequest{
			endpoint:   "test",
			chatID:     1,
			zone:       "Europe/Berlin",
			admittedCh: make(chan bool, 1),
		}
	}

	gone := &worker{
		webAppTimezoneRequests: make(chan webAppTimezoneRequest),
		shutdownCh:             make(chan struct{}),
	}
	close(gone.shutdownCh)
	// The verdict comes back on a channel, never through t:
	// a parked submit outlives the test, and touching t then panics
	// over the very failure the timeout is reporting.
	type outcome struct{ admitted, alive bool }
	got := make(chan outcome, 1)
	go func() {
		admitted, alive := gone.submitWebAppTimezone(newReq())
		got <- outcome{admitted: admitted, alive: alive}
	}()
	select {
	case o := <-got:
		if o.alive || o.admitted {
			t.Errorf("a submit after shutdown reported alive=%v admitted=%v", o.alive, o.admitted)
		}
	case <-time.After(time.Second):
		t.Fatal("the submit parked instead of giving up")
	}

	running := &worker{
		webAppTimezoneRequests: make(chan webAppTimezoneRequest),
		shutdownCh:             make(chan struct{}),
	}
	go func() {
		req := <-running.webAppTimezoneRequests
		req.admittedCh <- true
	}()
	if admitted, alive := running.submitWebAppTimezone(newReq()); !alive || !admitted {
		t.Errorf("a live loop reported alive=%v admitted=%v", alive, admitted)
	}
}

// TestTimezoneAppPageRenders pins what the page is told about the chat.
// The zone rides in on the query, which anyone may write,
// so one the bot cannot load reads as the default.
func TestTimezoneAppPageRenders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		current  string
		want     string
		wantCode int
	}{
		{"a zone the bot can load", "Europe/Berlin", "Europe/Berlin", http.StatusOK},
		{"a recased zone", "europe%2Fberlin", "Europe/Berlin", http.StatusOK},
		{"a zone it cannot", "Europe/Atlantis", utcZone, http.StatusOK},
		{"nothing at all", "", utcZone, http.StatusOK},
		// An endpoint the bot does not serve, which is the page's one refusal.
		{"an unknown endpoint", "Europe/Berlin", "", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := searchConfig(t, "123:test-token", nil)
			w := &worker{
				cfg:       cfg,
				zoneNames: testZoneNames,
				tr:        map[string]*cmdlib.Translations{"test": &testTranslations},
			}
			var err error
			w.timezoneHTML, err = htmltemplate.ParseFiles(
				filepath.Join("..", "..", "res", "webapp", "timezone.html"))
			if err != nil {
				t.Fatalf("cannot parse the timezone page: %v", err)
			}
			endpointName := "test"
			if tc.wantCode != http.StatusOK {
				endpointName = "nosuch"
			}
			r := httptest.NewRequest(http.MethodGet,
				"/apps/timezone?endpoint="+endpointName+"&current="+tc.current, nil)
			rw := httptest.NewRecorder()
			w.handleTimezoneApp(rw, r)
			if rw.Code != tc.wantCode {
				t.Fatalf("the page got %d, want %d", rw.Code, tc.wantCode)
			}
			if tc.want == "" {
				return
			}
			body := rw.Body.String()
			// The rendered line, not the bare name: the served zone array holds every name,
			// so a bare one is in the page whatever the label says.
			current := testTranslations.TimezoneAppCurrent.Str + ": " + tc.want
			if !strings.Contains(body, current) {
				t.Errorf("the page misses %q", current)
			}
			// The page offers what the bot accepts, so the list rides in with it
			// rather than being asked of the browser, which knows a different set.
			if !strings.Contains(body, `var zones = ["Europe/Berlin","UTC"];`) {
				t.Error("the page was served without the bot's own zone list")
			}
		})
	}
}

// TestPerformWebAppTimezoneIsWhitelistGated holds the picker to the gate every inbound path has:
// an unvetted chat leaves no row behind, a vetted one keeps its pick.
func TestPerformWebAppTimezoneIsWhitelistGated(t *testing.T) {
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

			admitted := make(chan bool, 1)
			w.performWebAppTimezone(webAppTimezoneRequest{
				endpoint:   "test",
				chatID:     c.chatID,
				zone:       "Europe/Berlin",
				admittedCh: admitted,
			})
			if got := <-admitted; got != c.admitted {
				t.Errorf("admitted = %v, want %v", got, c.admitted)
			}

			received := commandsInLog(w, "select command from received_message_log", nil)
			users := w.db.MustInt("select count(*) from users")
			if !c.admitted {
				sent := drainSendQueueToLog(t, w)
				if len(received) != 0 || len(sent) != 0 || users != 0 {
					t.Errorf("an unvetted pick reached the database: "+
						"received %q, sent %q, users %d", received, sent, users)
				}
				return
			}
			if !slices.Equal(received, []string{webAppTimezoneCommand}) || users != 1 {
				t.Errorf("an admitted pick left no trace: received %q, users %d", received, users)
			}
			stored := w.db.MustInt(
				"select count(*) from users where timezone = 'Europe/Berlin'")
			if stored != 1 {
				t.Errorf("the picked zone was not stored")
			}
		})
	}
}

// TestReplyTimezoneOffersThePicker: the picker is a way to change the zone,
// so it rides with the help, on the bare command alone,
// and only in a private chat, the one place a web app button opens.
// The URL is asserted whole: checkConfig requires the domain it is built on,
// and a malformed one fails the message rather than just the button.
func TestReplyTimezoneOffersThePicker(t *testing.T) {
	t.Parallel()
	const domain = "bot.example.invalid"
	tests := []struct {
		name    string
		chatID  int64
		help    bool
		wantURL string
	}{
		{
			"the bare command", 10, true,
			"https://" + domain + "/apps/timezone?endpoint=test&current=UTC",
		},
		{"a group asking", -10, true, ""},
		// Just set or cleared, so there is nothing to offer: the zone alone answers it.
		{"after a change", 10, false, ""},
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

			m := testMessage(w, tc.chatID, "timezone", 100)
			w.replyTimezone(m, utcZone, tc.help)
			queued := w.sendQueue.pop()
			markup, _ := queued.message.(*messageParams).ReplyMarkup.(*models.InlineKeyboardMarkup)
			if tc.wantURL == "" {
				if markup != nil {
					t.Errorf("got a picker button that cannot open: %+v", markup)
				}
				return
			}
			if markup == nil {
				t.Fatal("got no picker button")
			}
			webApp := markup.InlineKeyboard[0][0].WebApp
			if webApp == nil {
				t.Fatal("the picker button opens no web app")
			}
			if webApp.URL != tc.wantURL {
				t.Errorf("button URL = %q, want %q", webApp.URL, tc.wantURL)
			}
		})
	}
}

// TestHandleWebAppAddRefusesAGet: the add is a write, so it answers a POST alone.
// Its twin was hardened first; this holds the pair together,
// a GET being what a shared cache may answer from another chat's copy.
func TestHandleWebAppAddRefusesAGet(t *testing.T) {
	t.Parallel()
	const botToken = "123:test-token"
	w := &worker{
		cfg:               searchConfig(t, botToken, nil),
		webAppAddRequests: make(chan webAppAddRequest, 1),
		shutdownCh:        make(chan struct{}),
	}
	r := httptest.NewRequest(http.MethodGet, "/apps/add/api/submit?endpoint=test&streamer=alica", nil)
	r.Header.Set("X-Init-Data", initDataFor(botToken, 1))
	rw := httptest.NewRecorder()
	w.handleWebAppAdd(rw, r)

	if rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("a GET got %d, want %d", rw.Code, http.StatusMethodNotAllowed)
	}
	if queued := len(w.webAppAddRequests); queued != 0 {
		t.Errorf("a GET still queued %d adds", queued)
	}
}
