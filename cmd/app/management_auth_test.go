package main

import (
	"FrostAgent/internal/instance"
	"FrostAgent/internal/instanceconfig"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func localManagementRequest(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, path, body)
	request.RemoteAddr, request.Host = "127.0.0.1:12345", "localhost:8080"
	return request
}

func TestContainerGatewayKeepsLoopbackAuthenticationBoundary(t *testing.T) {
	previous := instanceconfig.ManagementLoopbackOnly
	instanceconfig.ManagementLoopbackOnly = "true"
	t.Cleanup(func() { instanceconfig.ManagementLoopbackOnly = previous })
	values := map[string]string{"MCP_CONTROL_TOKEN": "synthetic-control-token"}
	handler := managementAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), func(key string) string { return values[key] })
	for _, tc := range []struct {
		name, remote, token string
		enforceLocal        bool
		status              int
	}{
		{"authenticated gateway hop", "127.0.0.1:12345", "", false, http.StatusNoContent},
		{"remote cannot claim gateway", "198.51.100.10:12345", "", false, http.StatusUnauthorized},
		{"local token enforcement remains", "127.0.0.1:12345", "", true, http.StatusUnauthorized},
		{"configured gateway bearer token", "127.0.0.1:12345", "Bearer synthetic-control-token", true, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values["MCP_ENFORCE_LOCAL_TOKEN"] = fmt.Sprint(tc.enforceLocal)
			request := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
			request.RemoteAddr, request.Host = tc.remote, "panel.example"
			request.Header.Set("X-Forwarded-For", "198.51.100.20")
			request.Header.Set("X-Forwarded-Proto", "https")
			request.Header.Set("Authorization", tc.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d", response.Code, tc.status)
			}
		})
	}
}

func TestManagementRoutesRequireRemoteControlToken(t *testing.T) {
	gateway, err := newStorageGateway(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	manager, _, _ := gateway.snapshot()
	if err := manager.GlobalConfig().Update("MCP_CONTROL_TOKEN", "synthetic-control-token", false); err != nil {
		t.Fatal(err)
	}
	if err := manager.GlobalConfig().Update("ALLOW_REMOTE_MCP_MANAGEMENT", "true", false); err != nil {
		t.Fatal(err)
	}
	info, err := manager.Create("Auth Test")
	if err != nil {
		t.Fatal(err)
	}
	mux := managementMux(gateway)
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/api/instances", ""},
		{http.MethodGet, "/api/instances/global/extra-env", ""},
		{http.MethodPut, "/api/instances/global/extra-env", `{"key":"SYNTHETIC_PROVIDER_TOKEN","value":"secret"}`},
		{http.MethodDelete, "/api/instances/global/extra-env/SYNTHETIC_PROVIDER_TOKEN", ""},
		{http.MethodGet, "/api/instances/global/backup/settings", ""},
		{http.MethodPost, "/api/instances/global/import/settings", `{}`},
		{http.MethodGet, "/api/storage", ""},
		{http.MethodPost, "/api/storage", `{}`},
		{http.MethodPost, "/instances/" + info.ID + "/frostagent.v1.SettingsService/ListEnvVars", `{}`},
		{http.MethodPost, "/instances/" + info.ID + "/frostagent.v1.SettingsService/UpdateEnvVar", `{"key":"MCP_CONTROL_TOKEN","value":"attacker-token"}`},
		{http.MethodPost, "/instances/" + info.ID + "/frostagent.v1.SettingsService/DeleteEnvVar", `{"key":"MCP_CONTROL_TOKEN"}`},
		{http.MethodPost, "/frostagent.v1.LogService/ListLogs", `{}`},
		{http.MethodGet, "/api/security/locked", ""},
	}
	for _, tc := range cases {
		for _, token := range []string{"", "Bearer incorrect-token"} {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.RemoteAddr = "198.51.100.10:12345"
			request.Header.Set("Authorization", token)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "synthetic-control-token") {
				t.Fatalf("%s %s with %q: %d %s", tc.method, tc.path, token, response.Code, response.Body.String())
			}
		}
	}
	if got := manager.GlobalConfig().Get("MCP_CONTROL_TOKEN"); got != "synthetic-control-token" {
		t.Fatalf("unauthorized request changed control token: %q", got)
	}
	for _, path := range []string{"/api/instances", "/api/storage", "/api/instances/global/extra-env", "/api/instances/global/backup/settings"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = "198.51.100.10:12345"
		request.Header.Set("Authorization", "Bearer synthetic-control-token")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("authorized %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/instances/"+info.ID+"/frostagent.v1.SettingsService/ListEnvVars", strings.NewReader(`{}`))
	request.RemoteAddr = "198.51.100.10:12345"
	request.Header.Set("Authorization", "Bearer synthetic-control-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "synthetic-control-token") {
		t.Fatalf("authorized settings read: %d %s", response.Code, response.Body.String())
	}
	rotation := httptest.NewRequest(http.MethodPost, "/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar",
		strings.NewReader(`{"key":"MCP_CONTROL_TOKEN","value":"rotated-token"}`))
	rotation.RemoteAddr = "198.51.100.10:12345"
	rotation.Header.Set("Authorization", "Bearer synthetic-control-token")
	rotation.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, rotation)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("authorized token rotation: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("old token remained valid after rotation: %d", response.Code)
	}
	request.Header.Set("Authorization", "Bearer rotated-token")
	request.Body = io.NopCloser(strings.NewReader(`{}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("new token rejected after rotation: %d %s", response.Code, response.Body.String())
	}
}

func TestGatewayRechecksSelectedDatabaseAuthorization(t *testing.T) {
	gateway, err := newStorageGateway(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	old, _, _ := gateway.snapshot()
	defer old.Close()
	next, err := instance.NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err := old.GlobalConfig().Update("MCP_CONTROL_TOKEN", "old-database-token", false); err != nil {
		t.Fatal(err)
	}
	if err := next.GlobalConfig().Update("MCP_CONTROL_TOKEN", "new-database-token", false); err != nil {
		t.Fatal(err)
	}
	info, err := next.Create("New Database")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/instances/global/extra-env", "/api/instances/global/backup/settings",
		"/instances/" + info.ID + "/frostagent.v1.SettingsService/ListEnvVars",
		"/api/storage", "/api/security/locked",
	} {
		gateway.mu.Lock()
		gateway.current, gateway.security = old, securityHandlerFor(old)
		gateway.mu.Unlock()
		authorized, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		mux := managementMux(gateway)
		paused := managementAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(authorized)
			<-release
			mux.ServeHTTP(w, r)
		}), old.ControlPlaneGetenv())
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = "198.51.100.10:12345"
		request.Header.Set("Authorization", "Bearer old-database-token")
		response := httptest.NewRecorder()
		go func() { defer close(done); paused.ServeHTTP(response, request) }()
		select {
		case <-authorized:
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("request did not pass the old database authorization")
		}
		gateway.switchMu.Lock()
		gateway.mu.Lock()
		gateway.current, gateway.security = next, securityHandlerFor(next)
		gateway.generation++
		gateway.mu.Unlock()
		gateway.switchMu.Unlock()
		close(release)
		<-done
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("old token accessed new database on %s: %d %s", path, response.Code, response.Body.String())
		}
	}
}

func TestManagementAuthLocalProxyAndAdapterBoundaries(t *testing.T) {
	values := map[string]string{"MCP_CONTROL_TOKEN": "synthetic-control-token", "ALLOW_REMOTE_MCP_MANAGEMENT": "true"}
	handler := managementAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), func(key string) string { return values[key] })
	for _, tc := range []struct {
		name, path, host, remote, token string
		status                          int
	}{
		{"local", "/api/instances", "localhost:8080", "127.0.0.1:12345", "", 204},
		{"remote host via local proxy", "/api/instances", "panel.example", "127.0.0.1:12345", "", 401},
		{"proxy authorized", "/api/instances", "panel.example", "127.0.0.1:12345", "Bearer synthetic-control-token", 204},
		{"remote with local host", "/api/instances", "localhost:8080", "198.51.100.10:12345", "", 401},
		{"frontend assets", "/assets/app.js", "panel.example", "198.51.100.10:12345", "", 204},
		{"adapter", "/instances/a1b2c3d4/ws/onebot", "panel.example", "198.51.100.10:12345", "", 204},
		{"message ingestion", "/instances/a1b2c3d4/api/v1/messages/send", "panel.example", "198.51.100.10:12345", "", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			request.Host, request.RemoteAddr = tc.host, tc.remote
			request.Header.Set("Authorization", tc.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d", response.Code, tc.status)
			}
		})
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
		request := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
		request.Host, request.RemoteAddr = "localhost:8080", "127.0.0.1:12345"
		request.Header.Set(header, "127.0.0.1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("proxy with rewritten Host bypassed authentication (%s): %d", header, response.Code)
		}
	}
	values["MCP_CONTROL_TOKEN"] = ""
	request := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	request.RemoteAddr = "198.51.100.10:12345"
	request.Header.Set("Authorization", "Bearer arbitrary-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("remote access allowed with no configured token: %d", response.Code)
	}
	values["ADMIN_TOKEN"] = "synthetic-admin-token"
	request.Header.Set("Authorization", "Bearer synthetic-admin-token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("admin token fallback rejected: %d", response.Code)
	}
	values["MCP_ENFORCE_LOCAL_TOKEN"] = "true"
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "localhost:8080"
	request.Header.Del("Authorization")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("local enforcement bypassed: %d", response.Code)
	}
}
