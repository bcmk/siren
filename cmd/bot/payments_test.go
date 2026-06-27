package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestRejectForRedeliveryWhileMigrating(t *testing.T) {
	const paymentBody = `{"message":{"chat":{"id":123},"successful_payment":{"telegram_payment_charge_id":"ch_1"}}}`
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

// A malformed payment must create no user:
// handleSuccessfulPayment resolves the user only through GrantStarPaymentSubs,
// after the payload validates.
// The DB-level test can't catch a stray user made here, above that call.
func TestHandleSuccessfulPaymentMalformedCreatesNoUser(t *testing.T) {
	w := newTestWorker()
	defer w.terminate()
	w.createDatabase()

	const chatID = int64(555)
	w.handleSuccessfulPayment("test", chatID, &models.SuccessfulPayment{
		InvoicePayload:          "garbage",
		TelegramPaymentChargeID: "charge-malformed",
		TotalAmount:             100,
	}, 1000)

	if _, found := w.db.User(chatID); found {
		t.Error("malformed successful payment created a stray user")
	}
}

func TestParseStarsPayload(t *testing.T) {
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
