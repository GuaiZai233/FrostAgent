package actionscat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	actclient "FrostAgent/internal/actionscat"
)

func TestDisabledServiceReportsStatusAndRejectsProxy(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer backend.Close()
	client := actclient.New(func(key string) string {
		if key == "ACTIONSCAT_ENDPOINT" {
			return backend.URL
		}
		if key == "ACTIONSCAT_ENABLED" {
			return "false"
		}
		return ""
	})
	svc := NewScoped(client, "test-instance", func(string) string { return "" })
	for _, prefix := range []string{"/api/actionscat", "/api/v1/actionscat"} {
		for _, call := range []struct{ method, path string }{
			{http.MethodGet, "/status"},
			{http.MethodGet, "/actions"},
			{http.MethodPost, "/actions"},
			{http.MethodPost, "/dispatch"},
			{http.MethodPost, "/actions/test-action/runs"},
			{http.MethodDelete, "/schedules/test-schedule"},
		} {
			req := httptest.NewRequest(call.method, prefix+call.path, strings.NewReader("invalid-json"))
			req.RemoteAddr = "127.0.0.1:1234"
			recorder := httptest.NewRecorder()
			svc.ServeHTTP(recorder, req)
			if call.path == "/status" {
				var status actclient.StatusResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil || recorder.Code != http.StatusOK || !status.Disabled {
					t.Fatalf("status response = %d %s, error=%v", recorder.Code, recorder.Body, err)
				}
			} else {
				var body struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != http.StatusForbidden || body.Error != actclient.ErrDisabled.Error() {
					t.Fatalf("proxy response = %d %s, error=%v", recorder.Code, recorder.Body, err)
				}
			}
		}
	}
	// Control-plane authentication still takes precedence for remote callers.
	req := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	recorder := httptest.NewRecorder()
	svc.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized || requests.Load() != 0 {
		t.Fatalf("auth/disable boundary failed: status=%d, requests=%d", recorder.Code, requests.Load())
	}
}

func TestServiceDisabledAfterPreflightRejectsWithoutBackendRequest(t *testing.T) {
	var backendRequests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendRequests.Add(1)
	}))
	defer backend.Close()

	for _, prefix := range []string{"/api/actionscat", "/api/v1/actionscat"} {
		for _, call := range []struct{ method, path, body string }{
			{http.MethodGet, "/actions", ""},
			{http.MethodPost, "/actions", `{"name":"test-action"}`},
			{http.MethodPost, "/dispatch", `{"text":"test-event"}`},
			{http.MethodPost, "/actions/test-action/versions/test-version/builds", ""},
		} {
			t.Run(prefix+"/"+call.method+call.path, func(t *testing.T) {
				var enabledReads atomic.Int32
				client := actclient.New(func(key string) string {
					switch key {
					case "ACTIONSCAT_ENDPOINT":
						return backend.URL
					case "ACTIONSCAT_ENABLED":
						// Pass the proxy preflight, then disable before the client's request guard.
						if enabledReads.Add(1) == 1 {
							return "true"
						}
						return "false"
					}
					return ""
				})
				svc := NewScoped(client, "test-instance", func(string) string { return "" })
				req := httptest.NewRequest(call.method, prefix+call.path, strings.NewReader(call.body))
				req.RemoteAddr = "127.0.0.1:1234"
				recorder := httptest.NewRecorder()
				svc.ServeHTTP(recorder, req)

				var body struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatalf("invalid response: %s, error=%v", recorder.Body, err)
				}
				if enabledReads.Load() != 2 {
					t.Fatalf("expected proxy and client guards to both run, got %d checks", enabledReads.Load())
				}
				if recorder.Code != http.StatusForbidden || body.Error != "ActionsCat已被FrostAgent管理员禁用。" {
					t.Fatalf("disabled after preflight = %d %s", recorder.Code, recorder.Body)
				}
				if backendRequests.Load() != 0 {
					t.Fatalf("disabled request contacted backend %d times", backendRequests.Load())
				}
			})
		}
	}
}
