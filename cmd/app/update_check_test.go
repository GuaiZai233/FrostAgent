package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/updatecheck"
)

func TestVersionAndUpdatesWithoutInstanceRemainAuthenticated(t *testing.T) {
	gateway, err := newStorageGateway(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	manager, _, _ := gateway.snapshot()
	for key, value := range map[string]string{"MCP_CONTROL_TOKEN": "synthetic-control-token", "ALLOW_REMOTE_MCP_MANAGEMENT": "true"} {
		if err := manager.GlobalConfig().Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("forwarded management token")
		}
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{{"sha": strings.Repeat("a", 40)}})
	}))
	defer github.Close()
	mux := managementMuxWithUpdateSource(gateway, updatecheck.Source{Client: github.Client(), APIBaseURL: github.URL, Current: updatecheck.Build{Display: "1.2.3", Version: "1.2.3", Commit: strings.Repeat("a", 40)}})
	for _, path := range []string{"/api/version", "/api/update-check"} {
		for _, token := range []string{"", "Bearer incorrect"} {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.RemoteAddr = "198.51.100.10:12345"
			r.Header.Set("Authorization", token)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized || calls.Load() != 0 {
				t.Fatalf("unauthorized %s: %d, calls %d", path, w.Code, calls.Load())
			}
		}
	}
	for _, path := range []string{"/api/version", "/api/update-check"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = "198.51.100.10:12345"
		r.Header.Set("Authorization", "Bearer synthetic-control-token")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "1.2.3") {
			t.Fatalf("authorized %s: %d %s", path, w.Code, w.Body.String())
		}
		if path == "/api/version" && calls.Load() != 0 {
			t.Fatal("reading version contacted GitHub")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
	if err := manager.GlobalConfig().Update("MCP_CONTROL_TOKEN", "rotated-synthetic-token", false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/version", "/api/update-check"} {
		for _, tc := range []struct {
			token  string
			status int
		}{
			{"synthetic-control-token", http.StatusUnauthorized}, {"rotated-synthetic-token", http.StatusOK},
		} {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.RemoteAddr = "198.51.100.10:12345"
			r.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("rotated auth %s: %d", path, w.Code)
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatal("cached check unexpectedly contacted GitHub")
	}
}
