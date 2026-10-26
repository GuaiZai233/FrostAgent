package actionscat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	actclient "github.com/GuaiZai233/FrostAgent/internal/actionscat"
)

func TestMatcherProxyPreservesPatternWhitespace(t *testing.T) {
	for _, matchType := range []string{"exact", "contains", "regex"} {
		t.Run(matchType, func(t *testing.T) {
			captured := make(chan actclient.CreateMatcherReq, 1)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req actclient.CreateMatcherReq
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				captured <- req
				_ = json.NewEncoder(w).Encode(actclient.Matcher{ID: "fixture-matcher", Pattern: req.Pattern})
			}))
			defer backend.Close()
			client := actclient.New(func(key string) string {
				if key == "ACTIONSCAT_ENDPOINT" {
					return backend.URL
				}
				return ""
			})
			service := NewScoped(client, "fixture-instance", func(string) string { return "" })
			const pattern = " cat \n"
			for _, prefix := range []string{"/api/actionscat", "/api/v1/actionscat"} {
				body, _ := json.Marshal(actclient.CreateMatcherReq{Name: "fixture matcher", MatchType: matchType, Pattern: pattern})
				req := httptest.NewRequest(http.MethodPost, prefix+"/actions/fixture-action/matchers", bytes.NewReader(body))
				req.RemoteAddr = "127.0.0.1:1234"
				rec := httptest.NewRecorder()
				service.ServeHTTP(rec, req)
				if rec.Code != http.StatusCreated {
					t.Fatalf("create matcher: %d %s", rec.Code, rec.Body.String())
				}
				if got := (<-captured).Pattern; got != pattern {
					t.Errorf("pattern changed: got %q want %q", got, pattern)
				}
			}
		})
	}
}
