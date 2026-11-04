package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-telegram/bot/models"
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
