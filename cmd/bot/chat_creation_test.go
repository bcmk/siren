package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/signal"
	"slices"
	"syscall"
	"testing"

	"github.com/bcmk/siren/v7/internal/botconfig"
	"github.com/bcmk/siren/v7/lib/cmdlib"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestEnsureOwnerAddsAChatOnEveryEndpoint(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	cfg := searchConfig(t, "123:test-token", nil)
	// A copy of the one endpoint, as its type is unexported
	cfg.Endpoints["other"] = cfg.Endpoints["test"]
	w.cfg = cfg

	w.ensureOwner()

	if user, found := w.db.UserByChatID(cfg.OwnerID); !found || user.UserID != w.ownerUserID {
		t.Errorf("owner user = (%+v, %v), want id %d", user, found, w.ownerUserID)
	}
	for endpoint := range cfg.Endpoints {
		if _, found := w.db.Chat(endpoint, w.ownerUserID); !found {
			t.Errorf("the owner has no chat on %s", endpoint)
		}
	}
}

func TestPreCheckoutLogsOnAChatNewToTheBot(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	cfg := testConfig
	cfg.SubsTiers = []botconfig.SubsTier{{Count: 1, Cost: 10}}
	w.cfg = &cfg
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		_, _ = rw.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()
	stub, err := bot.New("token", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("cannot build the stub bot: %v", err)
	}
	w.bots = map[string]*bot.Bot{"test": stub}
	const chatID = int64(7)
	w.db.EnsureUserAndChat("other", chatID)

	w.handlePreCheckoutQuery("test", &models.PreCheckoutQuery{
		ID:             "query",
		Currency:       "XTR",
		TotalAmount:    10,
		InvoicePayload: fmt.Sprintf("stars:subs:%d:1", chatID),
	})

	logged := w.mockDB.receivedMessageChats(preCheckoutCommand)
	if want := []string{fmt.Sprintf("test %d", chatID)}; !slices.Equal(logged, want) {
		t.Errorf("logged on %q, want %q", logged, want)
	}
}

func TestFinishStartupRepliesOnAChatNewToTheBot(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	cfg := testConfig
	// The owner's notice parks in the test queue
	cfg.OwnerEndpoint = "test"
	cfg.PeriodSeconds = 60
	cfg.SubsConfirmationPeriodSeconds = 60
	cfg.NotificationsReadyPeriodSeconds = 60
	w.cfg = &cfg
	tr := testTranslations
	tr.WeAreUp = &cmdlib.Translation{Key: "we_are_up", Str: "WeAreUp", Parse: cmdlib.ParseRaw}
	w.tr = map[string]*cmdlib.Translations{"test": &tr}
	// Buffered, so the checker daemon hands in its result and stops with the context
	w.checkerResults = make(chan cmdlib.CheckerResults, 1)
	const chatID = int64(7)
	userID := w.db.EnsureUserAndChat("other", chatID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// finishStartup takes these signals over from the test binary
	defer signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)

	w.finishStartup(ctx, cancel, map[waitingUser]bool{{chatID: chatID, endpoint: "test"}: true})

	if _, found := w.db.Chat("test", userID); !found {
		t.Error("the user has no chat on test to log the reply on")
	}
	replies := 0
	for q := w.sendQueue.pop(); q != nil; q = w.sendQueue.pop() {
		p, ok := q.message.(*messageParams)
		if ok && q.userID == userID && p.renderParams != nil && p.renderParams.key == "we_are_up" {
			replies++
		}
	}
	if replies != 1 {
		t.Errorf("we-are-up replies to the user = %d, want 1", replies)
	}
}
