package checkers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A model asked for by performer id gets what the first feed listing them tells,
// and one no feed lists gets nothing.
func TestLiveJasminQueryFeedInfo(t *testing.T) {
	t.Parallel()
	const image = "https://galleryn2.vcmdiawe.com/a/b_glamour_896x504.jpg"
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		models := ""
		if r.URL.Path == "/boys" && r.URL.Query().Get("performerId") == "alice" {
			models = `{"performerId": "Alice", "status": "free_chat", "roomTopic": "hi",` +
				` "profilePictureUrl": {"size896x504": "` + image + `"}}`
		}
		_, _ = rw.Write([]byte(`{"status": "OK", "data": {"models": [` + models + `]}}`))
	}))
	t.Cleanup(srv.Close)
	c := &LiveJasminChecker{}
	c.Cfg = &LiveJasminCheckerConfig{UsersOnlineEndpoints: []string{srv.URL + "/girls?a=1", srv.URL + "/boys?a=1"}}
	c.Client = srv.Client()
	cases := []struct {
		name      string
		modelID   string
		wantImage string
		wantTopic string
	}{
		{"listed by the second feed", "alice", image, "hi"},
		{"listed by none", "bob", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := c.queryFeedInfo(tc.modelID)
			if info.ImageURL != tc.wantImage || info.Subject != tc.wantTopic {
				t.Errorf("info = %+v, want image %q and subject %q", info, tc.wantImage, tc.wantTopic)
			}
		})
	}
}
