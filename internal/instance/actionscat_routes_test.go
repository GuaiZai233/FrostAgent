package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"FrostAgent/internal/instanceconfig"
)

func TestManager_ActionsCatControlPlaneAuth(t *testing.T) {
	t.Setenv("SANDBOX_ENABLED", "false")
	dir := t.TempDir()
	g, err := instanceconfig.Open(filepath.Join(dir, ".env"), true)
	if err != nil {
		t.Fatal(err)
	}

	testEnv := map[string]string{
		"MCP_CONTROL_TOKEN": "ctrl_secret_token_888",
	}

	m, err := New(filepath.Join(dir, "data"), g, filepath.Join(dir, "dialogue.yml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	// Set dynamic mcpGetenv so it reads from testEnv before creating instance
	m.mcpGetenv = func(k string) string {
		return testEnv[k]
	}

	info, err := m.Create("test-actionscat-inst")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}

	endpoints := []struct {
		name      string
		path      string
		headerKey string
		headerVal string
	}{
		{name: "instance-scoped v0", path: "/instances/" + info.ID + "/api/actionscat/status"},
		{name: "instance-scoped v1", path: "/instances/" + info.ID + "/api/v1/actionscat/status"},
		{name: "default alias v0", path: "/api/actionscat/status"},
		{name: "default alias v1", path: "/api/v1/actionscat/status"},
		{name: "default alias with query param", path: "/api/actionscat/status?instance_id=" + info.ID},
		{name: "default alias with X-Instance-ID header", path: "/api/actionscat/status", headerKey: "X-Instance-ID", headerVal: info.ID},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			newReq := func(method string) *http.Request {
				r := httptest.NewRequest(method, ep.path, nil)
				if ep.headerKey != "" {
					r.Header.Set(ep.headerKey, ep.headerVal)
				}
				return r
			}

			// 1. Remote without token -> 401
			{
				req := newReq(http.MethodGet)
				req.RemoteAddr = "192.0.2.1:1234"
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("[%s] expected 401 for remote without token, got %d (body: %s)", ep.name, rec.Code, rec.Body.String())
				}
				var resp map[string]string
				_ = json.NewDecoder(rec.Body).Decode(&resp)
				if resp["error"] == "" {
					t.Fatalf("[%s] expected error message in response body", ep.name)
				}
			}

			// 2. Remote with wrong token -> 401
			{
				req := newReq(http.MethodGet)
				req.RemoteAddr = "192.0.2.1:1234"
				req.Header.Set("Authorization", "Bearer invalid_token")
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("[%s] expected 401 for remote with invalid token, got %d", ep.name, rec.Code)
				}
			}

			// 3. Remote with valid token -> 200
			{
				req := newReq(http.MethodGet)
				req.RemoteAddr = "192.0.2.1:1234"
				req.Header.Set("Authorization", "Bearer ctrl_secret_token_888")
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("[%s] expected 200 for remote with valid token, got %d (body: %s)", ep.name, rec.Code, rec.Body.String())
				}
			}

			// 4. Loopback default without token -> 200
			{
				req := newReq(http.MethodGet)
				req.RemoteAddr = "127.0.0.1:43210"
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("[%s] expected 200 for loopback default without token, got %d (body: %s)", ep.name, rec.Code, rec.Body.String())
				}
			}

			// 5. Loopback with MCP_ENFORCE_LOCAL_TOKEN=true
			{
				testEnv["MCP_ENFORCE_LOCAL_TOKEN"] = "true"
				defer func() {
					testEnv["MCP_ENFORCE_LOCAL_TOKEN"] = ""
				}()

				// 5a. Loopback without token -> 401
				reqNoToken := newReq(http.MethodGet)
				reqNoToken.RemoteAddr = "127.0.0.1:43210"
				recNoToken := httptest.NewRecorder()
				m.ServeHTTP(recNoToken, reqNoToken)
				if recNoToken.Code != http.StatusUnauthorized {
					t.Fatalf("[%s] expected 401 for loopback when MCP_ENFORCE_LOCAL_TOKEN=true and no token, got %d", ep.name, recNoToken.Code)
				}

				// 5b. Loopback with valid token -> 200
				reqWithToken := newReq(http.MethodGet)
				reqWithToken.RemoteAddr = "127.0.0.1:43210"
				reqWithToken.Header.Set("Authorization", "Bearer ctrl_secret_token_888")
				recWithToken := httptest.NewRecorder()
				m.ServeHTTP(recWithToken, reqWithToken)
				if recWithToken.Code != http.StatusOK {
					t.Fatalf("[%s] expected 200 for loopback when MCP_ENFORCE_LOCAL_TOKEN=true with valid token, got %d", ep.name, recWithToken.Code)
				}
			}
		})
	}
}
