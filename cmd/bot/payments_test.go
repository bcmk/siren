package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/bcmk/siren/v7/internal/botconfig"
)

func TestRejectForRedeliveryWhileMigrating(t *testing.T) {
	t.Parallel()
	const paymentBody = `{"message":{"chat":{"id":123},"successful_payment":{"telegram_payment_charge_id":"ch_1"}}}`
	const channelPaymentBody = `{"channel_post":{"chat":{"id":-1001234},` +
		`"successful_payment":{"telegram_payment_charge_id":"ch_2"}}}`
	const plainBody = `{"message":{"chat":{"id":123},"text":"/start"}}`
	const migrateToBody = `{"message":{"chat":{"id":123},"migrate_to_chat_id":-1001234}}`
	const migrateFromBody = `{"message":{"chat":{"id":-1001234},"migrate_from_chat_id":123}}`

	tests := []struct {
		name       string
		ready      bool
		body       string
		wantStatus int
		wantInner  bool
	}{
		{name: "payment while migrating rejected", ready: false, body: paymentBody, wantStatus: http.StatusServiceUnavailable, wantInner: false},
		{
			name:       "channel payment while migrating rejected",
			ready:      false,
			body:       channelPaymentBody,
			wantStatus: http.StatusServiceUnavailable,
			wantInner:  false,
		},
		{name: "migrate_to while migrating rejected", ready: false, body: migrateToBody, wantStatus: http.StatusServiceUnavailable, wantInner: false},
		{name: "migrate_from while migrating rejected", ready: false, body: migrateFromBody, wantStatus: http.StatusServiceUnavailable, wantInner: false},
		{name: "migration when ready passes", ready: true, body: migrateToBody, wantStatus: http.StatusOK, wantInner: true},
		{name: "plain update while migrating passes", ready: false, body: plainBody, wantStatus: http.StatusOK, wantInner: true},
		{name: "payment when ready passes", ready: true, body: paymentBody, wantStatus: http.StatusOK, wantInner: true},
		{name: "unparsable while migrating fails closed", ready: false, body: `{`, wantStatus: http.StatusServiceUnavailable, wantInner: false},
		{name: "unparsable when ready passes", ready: true, body: `{`, wantStatus: http.StatusOK, wantInner: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &worker{}
			w.maintenance.Store(!tt.ready)
			var innerCalled bool
			var innerBody string
			inner := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				innerCalled = true
				b, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("inner cannot read body: %v", err)
				}
				innerBody = string(b)
				rw.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(tt.body)))
			w.rejectForRedeliveryWhileMigrating(inner).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if innerCalled != tt.wantInner {
				t.Errorf("inner called = %v, want %v", innerCalled, tt.wantInner)
			}
			// The inner handler must see the full original body (rewind).
			if tt.wantInner && innerBody != tt.body {
				t.Errorf("inner body = %q, want %q (rewind failed)", innerBody, tt.body)
			}
		})
	}
}

// A rejected payment must create no user:
// handleSuccessfulPayment resolves the user only through GrantStarPaymentSubs,
// after the payment passes every check.
// The DB-level test can't catch a stray user made here, above that call.
func TestHandleSuccessfulPaymentRejectedCreatesNoUser(t *testing.T) {
	t.Parallel()
	w := newTestWorker()

	const chatID = int64(555)
	receipt := &models.Message{
		Chat: models.Chat{ID: chatID},
		SuccessfulPayment: &models.SuccessfulPayment{
			InvoicePayload:          "stars:other:555:5",
			TelegramPaymentChargeID: "charge-rejected",
			TotalAmount:             100,
		},
	}
	w.handleSuccessfulPayment("test", receipt, 1000)

	if _, found := w.db.UserByChatID(chatID); found {
		t.Error("a rejected successful payment created a stray user")
	}
}

// A receipt credits the chat the invoice is for, wherever it arrives,
// and the whitelist is checked on that chat, as at pre-checkout.
func TestPaymentCreditsThePayloadChat(t *testing.T) {
	t.Parallel()
	const group, channel, payer = int64(-10), int64(-1001234567890), int64(77)
	tests := []struct {
		name        string
		receiptChat int64
		channelPost bool
		payloadChat int64
		whitelist   []int64
		credited    bool
	}{
		{name: "receipt in the group", receiptChat: group, payloadChat: group, credited: true},
		{name: "receipt in the payer's chat", receiptChat: payer, payloadChat: group, credited: true},
		{
			name:        "receipt as a channel post",
			receiptChat: channel,
			channelPost: true,
			payloadChat: channel,
			credited:    true,
		},
		{
			name:        "payload chat whitelisted, receipt chat not",
			receiptChat: payer,
			payloadChat: group,
			whitelist:   []int64{group},
			credited:    true,
		},
		{
			name:        "receipt chat whitelisted, payload chat not",
			receiptChat: payer,
			payloadChat: group,
			whitelist:   []int64{payer},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			w.initCache()
			cfg := *w.cfg
			cfg.WhitelistChats = tc.whitelist
			w.cfg = &cfg
			receipt := &models.Message{
				Chat: models.Chat{ID: tc.receiptChat},
				SuccessfulPayment: &models.SuccessfulPayment{
					Currency:                "XTR",
					TotalAmount:             100,
					InvoicePayload:          fmt.Sprintf("stars:subs:%d:5", tc.payloadChat),
					TelegramPaymentChargeID: "charge-1",
				},
			}
			u := &models.Update{Message: receipt}
			if tc.channelPost {
				u = &models.Update{ChannelPost: receipt}
			}
			w.processTGUpdate(incomingPacket{endpoint: "test", message: u})
			got, want := 0, 0
			if user, found := w.db.UserByChatID(tc.payloadChat); found {
				chat, _ := w.db.Chat("test", user.UserID)
				got = chat.MaxSubs
			}
			if tc.credited {
				want = w.cfg.MaxSubs + 5
			}
			if got != want {
				t.Errorf("the payload chat's max_subs = %d, want %d", got, want)
			}
			if _, found := w.db.UserByChatID(payer); found {
				t.Error("the payer's chat was given a row")
			}
		})
	}
}

func TestParseStarsPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		payload     string
		wantProduct string
		wantChat    int64
		wantCount   int
		wantOK      bool
	}{
		{name: "valid", payload: "stars:subs:123:5", wantProduct: "subs", wantChat: 123, wantCount: 5, wantOK: true},
		{name: "negative chat id", payload: "stars:subs:-1001234:10", wantProduct: "subs", wantChat: -1001234, wantCount: 10, wantOK: true},
		{name: "other product", payload: "stars:boost:1:2", wantProduct: "boost", wantChat: 1, wantCount: 2, wantOK: true},
		{name: "empty product", payload: "stars::1:2"},
		{name: "old format without product rejected", payload: "stars:1:2"},
		{name: "old subs prefix rejected", payload: "subs:1:2:3"},
		{name: "other prefix", payload: "foo:subs:1:2"},
		{name: "too few parts", payload: "stars:subs:1"},
		{name: "too many parts", payload: "stars:subs:1:2:3"},
		{name: "non-numeric chat", payload: "stars:subs:abc:2"},
		{name: "non-numeric count", payload: "stars:subs:1:abc"},
		{name: "zero count", payload: "stars:subs:1:0"},
		{name: "negative count", payload: "stars:subs:1:-3"},
		{name: "empty", payload: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product, chat, count, ok := parseStarsPayload(tt.payload)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if product != tt.wantProduct || chat != tt.wantChat || count != tt.wantCount {
				t.Errorf("got (%q, %d, %d), want (%q, %d, %d)",
					product, chat, count, tt.wantProduct, tt.wantChat, tt.wantCount)
			}
		})
	}
}

// TestPackageButtonsSendTheirInvoices reads the buttons /more_subs attaches,
// then taps each one and checks the invoice it sends.
func TestPackageButtonsSendTheirInvoices(t *testing.T) {
	t.Parallel()
	w := newTestWorker()
	w.loadTranslations(siteTranslations("chaturbate", "en"))
	// Every worker shares &testConfig, so copy before setting the tiers.
	cfg := *w.cfg
	cfg.SubsTiers = []botconfig.SubsTier{{Count: 5, Cost: 100}, {Count: 10, Cost: 150}}
	w.cfg = &cfg
	invoices := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/sendInvoice") {
			invoices <- req.FormValue("payload") + " " + req.FormValue("prices")
			_, _ = rw.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":10,"type":"private"}}}`))
			return
		}
		_, _ = rw.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()
	stub, err := bot.New("token", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("cannot build the stub bot: %v", err)
	}
	w.bots = map[string]*bot.Bot{"test": stub}

	m := testMessage(w, 10, "more_subs", 0)
	w.processIncomingCommand(m, sender{from: &models.User{ID: 10}}, "more_subs", "", false)
	q := w.sendQueue.pop()
	if q == nil {
		t.Fatal("no reply queued")
	}
	keyboard, ok := q.message.(*messageParams).ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || len(keyboard.InlineKeyboard) != 2 {
		t.Fatalf("keyboard = %#v, want two rows", q.message.(*messageParams).ReplyMarkup)
	}
	tests := []struct{ label, invoice string }{
		{"5 subs · 100 ⭐", `stars:subs:10:5 [{"label":"5 subs","amount":100}]`},
		{"10 subs · 150 ⭐ (-25%)", `stars:subs:10:10 [{"label":"10 subs","amount":150}]`},
	}
	for i, tc := range tests {
		button := keyboard.InlineKeyboard[i][0]
		if button.Text != tc.label {
			t.Errorf("button %d label = %q, want %q", i, button.Text, tc.label)
		}
		w.processTGUpdate(incomingPacket{endpoint: "test", message: &models.Update{
			CallbackQuery: &models.CallbackQuery{
				Data: button.CallbackData,
				From: models.User{ID: 10},
				Message: models.MaybeInaccessibleMessage{
					Type:    models.MaybeInaccessibleMessageTypeMessage,
					Message: &models.Message{Chat: models.Chat{ID: 10}},
				},
			},
		}})
		select {
		case got := <-invoices:
			if got != tc.invoice {
				t.Errorf("button %d invoice = %q, want %q", i, got, tc.invoice)
			}
		default:
			t.Errorf("button %d sent no invoice", i)
		}
	}
	// A sent invoice is logged and needs no reply, which goes out only when sending fails.
	if n := w.sendQueue.Len(); n != 0 {
		t.Errorf("replies queued = %d, want 0", n)
	}
	if got := w.mockDB.receivedMessageChats(invoiceCommand); !slices.Equal(got, []string{"test 10", "test 10"}) {
		t.Errorf("invoices logged for %q, want two for test 10", got)
	}
}
