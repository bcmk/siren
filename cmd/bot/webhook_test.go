package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSecretToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		header     string
		wantStatus int
		wantInner  bool
	}{
		{name: "matching token passes", header: "tok", wantStatus: http.StatusOK, wantInner: true},
		{name: "wrong token rejected", header: "bad", wantStatus: http.StatusForbidden, wantInner: false},
		{name: "missing header rejected", header: "", wantStatus: http.StatusForbidden, wantInner: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var innerCalled bool
			inner := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
				innerCalled = true
				rw.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.header != "" {
				req.Header.Set("X-Telegram-Bot-Api-Secret-Token", tt.header)
			}
			requireSecretToken("test", "tok", inner).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if innerCalled != tt.wantInner {
				t.Errorf("inner called = %v, want %v", innerCalled, tt.wantInner)
			}
		})
	}
}
