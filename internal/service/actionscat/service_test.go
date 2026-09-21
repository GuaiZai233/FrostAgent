package actionscat

import (
	"strings"
	"context"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	actclient "FrostAgent/internal/actionscat"
)

func TestService_Status_Unconfigured(t *testing.T) {
	cl := actclient.New(func(k string) string { return "" })
	svc := New(cl, "inst_1")

	req := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()

	svc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var st actclient.StatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&st); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if st.Configured || st.Healthy {
		t.Fatalf("expected unconfigured, got %+v", st)
	}
}

func TestService_EndToEnd_ProxyEndpoints(t *testing.T) {
	mockAction := actclient.Action{
		ID:          "act_weather",
		Name:        "Weather Fetcher",
		Description: "Fetches weather updates",
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
	}
	mockRun := actclient.Run{
		ID:          "run_weather_01",
		ActionID:    "act_weather",
		Status:      "succeeded",
		TriggerType: "manual",
		Stdout:      "Weather: Sunny 24C",
		Stderr:      "",
		DurationMs:  150,
		CreatedAt:   time.Now().UTC(),
	}

	backendTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/api/v1/actions":
			if r.Method == http.MethodPost {
				var req actclient.CreateActionReq
				_ = json.NewDecoder(r.Body).Decode(&req)
				created := mockAction
				created.ID = "act_new_created"
				created.Name = req.Name
				created.Description = req.Description
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(created)
				return
			}
			_ = json.NewEncoder(w).Encode([]actclient.Action{mockAction})
		case "/api/v1/actions/act_weather":
			_ = json.NewEncoder(w).Encode(mockAction)
		case "/api/v1/actions/act_weather/runs":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(mockRun)
				return
			}
			_ = json.NewEncoder(w).Encode([]actclient.Run{mockRun})
		case "/api/v1/actions/act_weather/runs/run_weather_01":
			_ = json.NewEncoder(w).Encode(mockRun)
		case "/api/v1/actions/act_weather/runs/run_weather_01/logs":
			_ = json.NewEncoder(w).Encode(actclient.RunLogs{
				Stdout: mockRun.Stdout,
				Stderr: mockRun.Stderr,
			})
		case "/api/v1/actions/act_weather/versions":
			if r.Method == http.MethodPost {
				var req actclient.CreateVersionReq
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(actclient.ActionVersion{
					ID:            "ver_weather_01",
					ActionID:      "act_weather",
					VersionNumber: 1,
				})
				return
			}
			_ = json.NewEncoder(w).Encode([]actclient.ActionVersion{
				{ID: "ver_weather_01", ActionID: "act_weather", VersionNumber: 1},
			})
		case "/api/v1/actions/act_weather/versions/ver_weather_01":
			_ = json.NewEncoder(w).Encode(actclient.ActionVersion{
				ID:            "ver_weather_01",
				ActionID:      "act_weather",
				VersionNumber: 1,
			})
		case "/api/v1/actions/act_weather/versions/ver_weather_01/builds":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(actclient.ArtifactBuild{
					ID:        "bld_weather_01",
					ActionID:  "act_weather",
					VersionID: "ver_weather_01",
					Status:    "succeeded",
					Stdout:    "Build OK",
				})
				return
			}
		case "/api/v1/actions/act_weather/builds":
			_ = json.NewEncoder(w).Encode([]actclient.ArtifactBuild{
				{
					ID:        "bld_weather_01",
					ActionID:  "act_weather",
					VersionID: "ver_weather_01",
					Status:    "succeeded",
					Stdout:    "Build OK",
				},
			})
		case "/api/v1/actions/act_weather/builds/bld_weather_01":
			_ = json.NewEncoder(w).Encode(actclient.ArtifactBuild{
				ID:        "bld_weather_01",
				ActionID:  "act_weather",
				VersionID: "ver_weather_01",
				Status:    "succeeded",
				Stdout:    "Build OK",
			})
		case "/api/v1/actions/act_weather/builds/bld_weather_01/logs":
			_ = json.NewEncoder(w).Encode(actclient.BuildLogs{
				Stdout: "Build OK",
				Stderr: "",
			})
		case "/api/v1/actions/act_weather/active-build":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
				return
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer backendTS.Close()

	cl := actclient.New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return backendTS.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_mgmt_token"
		default:
			return ""
		}
	})

	svc := New(cl, "inst_test")

	newReq := func(method, target string, body io.Reader) *http.Request {
		r := httptest.NewRequest(method, target, body)
		r.RemoteAddr = "127.0.0.1:1234"
		return r
	}

	// 1. GET /api/actionscat/status
	{
		req := newReq(http.MethodGet, "/api/actionscat/status", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status status code: %d", rec.Code)
		}
		var st actclient.StatusResponse
		_ = json.NewDecoder(rec.Body).Decode(&st)
		if !st.Configured || !st.Healthy {
			t.Fatalf("expected configured and healthy: %+v", st)
		}
	}

	// 2. GET /api/actionscat/actions
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("actions status code: %d", rec.Code)
		}
		var actions []actclient.Action
		_ = json.NewDecoder(rec.Body).Decode(&actions)
		if len(actions) != 1 || actions[0].ID != "act_weather" {
			t.Fatalf("unexpected actions: %+v", actions)
		}
	}

	// 3. GET /api/actionscat/actions/act_weather
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get action status code: %d", rec.Code)
		}
		var act actclient.Action
		_ = json.NewDecoder(rec.Body).Decode(&act)
		if act.Name != "Weather Fetcher" {
			t.Fatalf("unexpected action: %+v", act)
		}
	}

	// 4. POST /api/actionscat/actions/act_weather/runs
	{
		body, _ := json.Marshal(actclient.ManualRunReq{
			ExtraEnv: map[string]string{"CITY": "Tokyo"},
		})
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_weather/runs", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create run status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		var run actclient.Run
		_ = json.NewDecoder(rec.Body).Decode(&run)
		if run.ID != "run_weather_01" {
			t.Fatalf("unexpected run: %+v", run)
		}
	}

	// 5. GET /api/actionscat/actions/act_weather/runs
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather/runs?limit=10", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list runs status code: %d", rec.Code)
		}
		var runs []actclient.Run
		_ = json.NewDecoder(rec.Body).Decode(&runs)
		if len(runs) != 1 || runs[0].ID != "run_weather_01" {
			t.Fatalf("unexpected runs: %+v", runs)
		}
	}

	// 6. GET /api/actionscat/actions/act_weather/runs/run_weather_01
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather/runs/run_weather_01", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get run status code: %d", rec.Code)
		}
		var run actclient.Run
		_ = json.NewDecoder(rec.Body).Decode(&run)
		if run.Stdout != "Weather: Sunny 24C" {
			t.Fatalf("unexpected run stdout: %s", run.Stdout)
		}
	}

	// 7. GET /api/actionscat/actions/act_weather/runs/run_weather_01/logs
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather/runs/run_weather_01/logs", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get run logs status code: %d", rec.Code)
		}
		var logs actclient.RunLogs
		_ = json.NewDecoder(rec.Body).Decode(&logs)
		if logs.Stdout != "Weather: Sunny 24C" {
			t.Fatalf("unexpected logs: %+v", logs)
		}
	}

	// 8. POST /api/actionscat/actions
	{
		body, _ := json.Marshal(actclient.CreateActionReq{
			Name:        "New Action",
			Description: "Created via service",
		})
		req := newReq(http.MethodPost, "/api/actionscat/actions", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create action status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		var act actclient.Action
		_ = json.NewDecoder(rec.Body).Decode(&act)
		if act.Name != "New Action" || act.ID != "act_new_created" {
			t.Fatalf("unexpected created action: %+v", act)
		}
	}

	// 9. POST /api/actionscat/actions/act_weather/versions
	{
		body, _ := json.Marshal(actclient.CreateVersionReq{
			Files: map[string]string{"main.go": "package main"},
		})
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_weather/versions", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create version status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		var ver actclient.ActionVersion
		_ = json.NewDecoder(rec.Body).Decode(&ver)
		if ver.ID != "ver_weather_01" {
			t.Fatalf("unexpected version: %+v", ver)
		}
	}

	// 10. GET /api/actionscat/actions/act_weather/versions
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather/versions", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list versions status code: %d", rec.Code)
		}
		var vers []actclient.ActionVersion
		_ = json.NewDecoder(rec.Body).Decode(&vers)
		if len(vers) != 1 || vers[0].ID != "ver_weather_01" {
			t.Fatalf("unexpected versions: %+v", vers)
		}
	}

	// 11. GET /api/actionscat/actions/act_weather/versions/ver_weather_01
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather/versions/ver_weather_01", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get version status code: %d", rec.Code)
		}
		var ver actclient.ActionVersion
		_ = json.NewDecoder(rec.Body).Decode(&ver)
		if ver.ID != "ver_weather_01" {
			t.Fatalf("unexpected version: %+v", ver)
		}
	}

	// 12. POST /api/actionscat/actions/act_weather/versions/ver_weather_01/builds
	{
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_weather/versions/ver_weather_01/builds", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("build version status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		var bld actclient.ArtifactBuild
		_ = json.NewDecoder(rec.Body).Decode(&bld)
		if bld.ID != "bld_weather_01" || bld.Status != "succeeded" {
			t.Fatalf("unexpected build: %+v", bld)
		}
	}

	// 13. GET /api/actionscat/actions/act_weather/builds (ListBuilds)
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather/builds", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list builds status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		var blds []actclient.ArtifactBuild
		_ = json.NewDecoder(rec.Body).Decode(&blds)
		if len(blds) != 1 || blds[0].ID != "bld_weather_01" {
			t.Fatalf("unexpected builds: %+v", blds)
		}

		// Also verify /api/v1/ prefix alias
		reqV1 := newReq(http.MethodGet, "/api/v1/actionscat/actions/act_weather/builds", nil)
		recV1 := httptest.NewRecorder()
		svc.ServeHTTP(recV1, reqV1)
		if recV1.Code != http.StatusOK {
			t.Fatalf("list builds v1 alias status code: %d", recV1.Code)
		}
	}

	// 14. GET /api/actionscat/actions/act_weather/builds/bld_weather_01
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather/builds/bld_weather_01", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get build status code: %d", rec.Code)
		}
		var bld actclient.ArtifactBuild
		_ = json.NewDecoder(rec.Body).Decode(&bld)
		if bld.ID != "bld_weather_01" {
			t.Fatalf("unexpected build: %+v", bld)
		}
	}

	// 15. GET /api/actionscat/actions/act_weather/builds/bld_weather_01/logs
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_weather/builds/bld_weather_01/logs", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get build logs status code: %d", rec.Code)
		}
		var logs actclient.BuildLogs
		_ = json.NewDecoder(rec.Body).Decode(&logs)
		if logs.Stdout != "Build OK" {
			t.Fatalf("unexpected logs: %+v", logs)
		}
	}

	// 16. POST /api/actionscat/actions/act_weather/active-build
	{
		body, _ := json.Marshal(actclient.SetActiveBuildReq{
			VersionID: "ver_weather_01",
			BuildID:   "bld_weather_01",
		})
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_weather/active-build", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("set active build status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		var res map[string]bool
		_ = json.NewDecoder(rec.Body).Decode(&res)
		if !res["ok"] {
			t.Fatalf("expected ok: true, got: %+v", res)
		}
	}
}

func TestService_ValidationAndHardening(t *testing.T) {
	cl := actclient.New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return "http://127.0.0.1:9999"
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "valid_token"
		default:
			return ""
		}
	})
	svc := New(cl, "inst_test")

	newReq := func(method, target string, body io.Reader) *http.Request {
		r := httptest.NewRequest(method, target, body)
		r.RemoteAddr = "127.0.0.1:1234"
		return r
	}

	// 1. POST runs with malformed JSON
	{
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_1/runs", bytes.NewReader([]byte("{invalid-json")))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json, got %d", rec.Code)
		}
	}

	// 2. POST runs with trailing tokens
	{
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_1/runs", bytes.NewReader([]byte(`{"extra_env":{}} {"extra": true}`)))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for trailing json, got %d", rec.Code)
		}
	}

	// 3. POST runs with reserved ACTIONSCAT_ prefix
	{
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_1/runs", bytes.NewReader([]byte(`{"extra_env":{"ACTIONSCAT_OVERRIDE":"val"}}`)))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for reserved prefix, got %d", rec.Code)
		}
	}

	// 4. POST dispatch with reserved ACTIONSCAT_ prefix
	{
		req := newReq(http.MethodPost, "/api/actionscat/dispatch", bytes.NewReader([]byte(`{"extra_env":{"ACTIONSCAT_ACTION_ID":"val"}}`)))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for reserved prefix in dispatch, got %d", rec.Code)
		}
	}

	// 5. POST dispatch with malformed JSON
	{
		req := newReq(http.MethodPost, "/api/actionscat/dispatch", bytes.NewReader([]byte(`{"unclosed`)))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json in dispatch, got %d", rec.Code)
		}
	}
}

func TestService_ControlPlaneAuth(t *testing.T) {
	cl := actclient.New(func(k string) string { return "" })

	env := map[string]string{
		"MCP_CONTROL_TOKEN": "secret_token_123",
	}
	getenv := func(k string) string {
		return env[k]
	}

	svc := NewScoped(cl, "inst_auth_test", getenv)

	// 1. Remote + no token -> 401 Unauthorized
	{
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for remote without token, got %d", rec.Code)
		}
	}

	// 2. Remote + wrong token -> 401 Unauthorized
	{
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("Authorization", "Bearer wrong_token")
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for wrong token, got %d", rec.Code)
		}
	}

	// 3. Remote + valid token -> 200 OK
	{
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("Authorization", "Bearer secret_token_123")
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for valid token, got %d", rec.Code)
		}
	}

	// 4. Loopback default without token -> 200 OK
	{
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for loopback default without token, got %d", rec.Code)
		}
	}

	// 5. Loopback with MCP_ENFORCE_LOCAL_TOKEN=true and no token -> 401 Unauthorized
	{
		env["MCP_ENFORCE_LOCAL_TOKEN"] = "true"
		defer delete(env, "MCP_ENFORCE_LOCAL_TOKEN")

		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for loopback when MCP_ENFORCE_LOCAL_TOKEN=true and no token, got %d", rec.Code)
		}

		// Loopback with valid token when MCP_ENFORCE_LOCAL_TOKEN=true -> 200 OK
		reqWithToken := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
		reqWithToken.RemoteAddr = "127.0.0.1:54321"
		reqWithToken.Header.Set("Authorization", "Bearer secret_token_123")
		recWithToken := httptest.NewRecorder()
		svc.ServeHTTP(recWithToken, reqWithToken)
		if recWithToken.Code != http.StatusOK {
			t.Fatalf("expected 200 for loopback with valid token when MCP_ENFORCE_LOCAL_TOKEN=true, got %d", recWithToken.Code)
		}
	}
}

func TestService_BuildUnknownResult_GatewayTimeout(t *testing.T) {
	backendTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/builds") && r.Method == http.MethodPost {
			// Simulate drop / hang by sleeping or closing
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer backendTS.Close()

	cl := actclient.New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return backendTS.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_token"
		default:
			return ""
		}
	})

	svc := New(cl, "inst_timeout_test")

	req := httptest.NewRequest(http.MethodPost, "/api/actionscat/actions/act_1/versions/ver_1/builds", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	// Context with tiny timeout to trigger ErrBuildUnknownResult
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 Gateway Timeout for ErrBuildUnknownResult, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown") {
		t.Fatalf("expected unknown in error body, got: %s", rec.Body.String())
	}
}

func TestService_BuildServer500_GatewayTimeout(t *testing.T) {
	backendTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/builds") && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "backend compilation failed after build created",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer backendTS.Close()

	cl := actclient.New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return backendTS.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_token"
		default:
			return ""
		}
	})

	svc := New(cl, "inst_500_test")

	req := httptest.NewRequest(http.MethodPost, "/api/actionscat/actions/act_1/versions/ver_1/builds", nil)
	req.RemoteAddr = "127.0.0.1:1234"

	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 Gateway Timeout for backend 500 on build, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown") {
		t.Fatalf("expected unknown in error body, got: %s", rec.Body.String())
	}
}

func TestService_SchedulesAndMatchersProxy(t *testing.T) {
	mockSchedule := actclient.Schedule{
		ID:        "sched_789",
		ActionID:  "act_cron",
		CronExpr:  "0 8 * * *",
		Timezone:  "Asia/Shanghai",
		NextRunAt: time.Now().UTC().Add(time.Hour),
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	mockMatcher := actclient.Matcher{
		ID:          "m_789",
		ActionID:    "act_cron",
		Name:        "bilibili-link",
		MatchType:   "regex",
		Pattern:     `https://b23\.tv/(?P<bvid>\w+)`,
		TargetField: "text",
		CaptureEnvMap: map[string]string{
			"bvid": "BVID",
		},
		Priority:         10,
		ContinueMatching: false,
		Enabled:          true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}

	var receivedDeleteScheduleID string
	var receivedDeleteMatcherID string
	var lastCreatedScheduleReq actclient.CreateScheduleReq
	var lastCreatedMatcherReq actclient.CreateMatcherReq

	backendTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/actions/act_cron/schedules":
			if r.Method == http.MethodPost {
				var req actclient.CreateScheduleReq
				_ = json.NewDecoder(r.Body).Decode(&req)
				lastCreatedScheduleReq = req
				created := mockSchedule
				created.CronExpr = req.CronExpr
				created.Timezone = req.Timezone
				created.Enabled = req.Enabled
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(created)
				return
			}
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode([]actclient.Schedule{mockSchedule})
				return
			}

		case r.URL.Path == "/api/v1/schedules/sched_789" && r.Method == http.MethodDelete:
			receivedDeleteScheduleID = "sched_789"
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			return

		case r.URL.Path == "/api/v1/actions/act_cron/matchers":
			if r.Method == http.MethodPost {
				var req actclient.CreateMatcherReq
				_ = json.NewDecoder(r.Body).Decode(&req)
				lastCreatedMatcherReq = req
				created := mockMatcher
				created.Name = req.Name
				created.MatchType = req.MatchType
				created.Pattern = req.Pattern
				created.CaptureEnvMap = req.CaptureEnvMap
				created.Enabled = req.Enabled
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(created)
				return
			}
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode([]actclient.Matcher{mockMatcher})
				return
			}

		case r.URL.Path == "/api/v1/matchers/m_789" && r.Method == http.MethodDelete:
			receivedDeleteMatcherID = "m_789"
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			return

		default:
			http.NotFound(w, r)
		}
	}))
	defer backendTS.Close()

	cl := actclient.New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return backendTS.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_mgmt_token"
		default:
			return ""
		}
	})

	svc := New(cl, "inst_test")

	newReq := func(method, target string, body io.Reader) *http.Request {
		r := httptest.NewRequest(method, target, body)
		r.RemoteAddr = "127.0.0.1:1234"
		return r
	}

	// 1. POST /api/actionscat/actions/act_cron/schedules
	{
		body, _ := json.Marshal(actclient.CreateScheduleReq{
			CronExpr: "0 8 * * *",
			Timezone: "Asia/Shanghai",
			Enabled:  true,
		})
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/schedules", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create schedule status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		var sched actclient.Schedule
		_ = json.NewDecoder(rec.Body).Decode(&sched)
		if sched.ID != "sched_789" || sched.CronExpr != "0 8 * * *" {
			t.Fatalf("unexpected schedule: %+v", sched)
		}
	}

	// 2. GET /api/actionscat/actions/act_cron/schedules (and with /api/v1/ prefix)
	{
		req := newReq(http.MethodGet, "/api/v1/actionscat/actions/act_cron/schedules", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list schedules status code: %d", rec.Code)
		}
		var list []actclient.Schedule
		_ = json.NewDecoder(rec.Body).Decode(&list)
		if len(list) != 1 || list[0].ID != "sched_789" {
			t.Fatalf("unexpected schedules list: %+v", list)
		}
	}

	// 3. DELETE /api/actionscat/schedules/sched_789
	{
		req := newReq(http.MethodDelete, "/api/actionscat/schedules/sched_789", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("delete schedule status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		if receivedDeleteScheduleID != "sched_789" {
			t.Fatalf("expected backend delete for sched_789, got %q", receivedDeleteScheduleID)
		}
	}

	// 4. POST /api/actionscat/actions/act_cron/matchers
	{
		body, _ := json.Marshal(actclient.CreateMatcherReq{
			Name:      "bilibili-link",
			MatchType: "regex",
			Pattern:   `https://b23\.tv/(?P<bvid>\w+)`,
			Enabled:   true,
		})
		req := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create matcher status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		var m actclient.Matcher
		_ = json.NewDecoder(rec.Body).Decode(&m)
		if m.ID != "m_789" || m.Name != "bilibili-link" {
			t.Fatalf("unexpected matcher: %+v", m)
		}
	}

	// 5. GET /api/actionscat/actions/act_cron/matchers
	{
		req := newReq(http.MethodGet, "/api/actionscat/actions/act_cron/matchers", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list matchers status code: %d", rec.Code)
		}
		var list []actclient.Matcher
		_ = json.NewDecoder(rec.Body).Decode(&list)
		if len(list) != 1 || list[0].ID != "m_789" {
			t.Fatalf("unexpected matchers list: %+v", list)
		}
	}

	// 6. DELETE /api/actionscat/matchers/m_789
	{
		req := newReq(http.MethodDelete, "/api/actionscat/matchers/m_789", nil)
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("delete matcher status code: %d, body: %s", rec.Code, rec.Body.String())
		}
		if receivedDeleteMatcherID != "m_789" {
			t.Fatalf("expected backend delete for m_789, got %q", receivedDeleteMatcherID)
		}
	}

	// 7. Fail-closed on malformed JSON and trailing tokens
	{
		// Malformed JSON for schedule
		req1 := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/schedules", bytes.NewReader([]byte("{invalid-json")))
		rec1 := httptest.NewRecorder()
		svc.ServeHTTP(rec1, req1)
		if rec1.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json in create schedule, got %d", rec1.Code)
		}

		// Trailing tokens for schedule
		req2 := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/schedules", bytes.NewReader([]byte(`{"cron_expr":"* * * * *"} 123`)))
		rec2 := httptest.NewRecorder()
		svc.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for trailing tokens in create schedule, got %d", rec2.Code)
		}

		// Empty cron_expr
		req3 := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/schedules", bytes.NewReader([]byte(`{"cron_expr":""}`)))
		rec3 := httptest.NewRecorder()
		svc.ServeHTTP(rec3, req3)
		if rec3.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty cron_expr, got %d", rec3.Code)
		}

		// Malformed JSON for matcher
		req4 := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte("{invalid-json")))
		rec4 := httptest.NewRecorder()
		svc.ServeHTTP(rec4, req4)
		if rec4.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json in create matcher, got %d", rec4.Code)
		}

		// Trailing tokens for matcher
		req5 := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{"name":"test","pattern":"p"} "extra"`)))
		rec5 := httptest.NewRecorder()
		svc.ServeHTTP(rec5, req5)
		if rec5.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for trailing tokens in create matcher, got %d", rec5.Code)
		}

		// Empty name / pattern for matcher
		req6 := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{"name":"","pattern":"p"}`)))
		rec6 := httptest.NewRecorder()
		svc.ServeHTTP(rec6, req6)
		if rec6.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty name in create matcher, got %d", rec6.Code)
		}
	}

	// 8. Schedules: optional enabled defaults to true, explicit false preserved
	{
		// Omitted enabled -> defaults to true
		reqOmit := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/schedules", bytes.NewReader([]byte(`{"cron_expr":"0 10 * * *"}`)))
		recOmit := httptest.NewRecorder()
		svc.ServeHTTP(recOmit, reqOmit)
		if recOmit.Code != http.StatusCreated {
			t.Fatalf("expected 201 for schedule with omitted enabled, got %d", recOmit.Code)
		}
		if !lastCreatedScheduleReq.Enabled {
			t.Fatalf("expected schedule Enabled to default to true, got false")
		}

		// Explicit false -> preserved
		reqFalse := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/schedules", bytes.NewReader([]byte(`{"cron_expr":"0 10 * * *","enabled":false}`)))
		recFalse := httptest.NewRecorder()
		svc.ServeHTTP(recFalse, reqFalse)
		if recFalse.Code != http.StatusCreated {
			t.Fatalf("expected 201 for schedule with explicit false enabled, got %d", recFalse.Code)
		}
		if lastCreatedScheduleReq.Enabled {
			t.Fatalf("expected schedule Enabled to be false, got true")
		}
	}

	// 9. Matchers: optional enabled defaults to true, explicit false preserved, default match_type to exact
	{
		// Omitted enabled -> defaults to true
		reqOmit := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{"name":"m_omit","pattern":"hello"}`)))
		recOmit := httptest.NewRecorder()
		svc.ServeHTTP(recOmit, reqOmit)
		if recOmit.Code != http.StatusCreated {
			t.Fatalf("expected 201 for matcher with omitted enabled, got %d", recOmit.Code)
		}
		if !lastCreatedMatcherReq.Enabled {
			t.Fatalf("expected matcher Enabled to default to true, got false")
		}
		if lastCreatedMatcherReq.MatchType != "exact" {
			t.Fatalf("expected matcher MatchType to default to exact, got %q", lastCreatedMatcherReq.MatchType)
		}

		// Explicit false -> preserved
		reqFalse := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{"name":"m_false","pattern":"hello","enabled":false}`)))
		recFalse := httptest.NewRecorder()
		svc.ServeHTTP(recFalse, reqFalse)
		if recFalse.Code != http.StatusCreated {
			t.Fatalf("expected 201 for matcher with explicit false enabled, got %d", recFalse.Code)
		}
		if lastCreatedMatcherReq.Enabled {
			t.Fatalf("expected matcher Enabled to be false, got true")
		}
	}

	// 10. Matchers: match_type enum validation
	{
		reqInvalidType := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{"name":"m_bad","pattern":"hello","match_type":"bogus"}`)))
		recInvalidType := httptest.NewRecorder()
		svc.ServeHTTP(recInvalidType, reqInvalidType)
		if recInvalidType.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid match_type, got %d", recInvalidType.Code)
		}
		if !strings.Contains(recInvalidType.Body.String(), "invalid match_type") {
			t.Fatalf("expected invalid match_type message, got: %s", recInvalidType.Body.String())
		}
	}

	// 11. Matchers: regex pattern pre-compilation validation vs plain string modes
	{
		// Invalid regex pattern when match_type is regex -> 400
		reqBadRegex := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{"name":"m_bad_re","pattern":"(?P<city>[)","match_type":"regex"}`)))
		recBadRegex := httptest.NewRecorder()
		svc.ServeHTTP(recBadRegex, reqBadRegex)
		if recBadRegex.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid regex pattern, got %d", recBadRegex.Code)
		}
		if !strings.Contains(recBadRegex.Body.String(), "invalid regex pattern") {
			t.Fatalf("expected invalid regex pattern error, got: %s", recBadRegex.Body.String())
		}

		// Meta-characters allowed in exact mode
		reqExact := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{"name":"m_exact","pattern":"(?P<city>[)","match_type":"exact"}`)))
		recExact := httptest.NewRecorder()
		svc.ServeHTTP(recExact, reqExact)
		if recExact.Code != http.StatusCreated {
			t.Fatalf("expected 201 for meta-characters in exact match_type, got %d (body: %s)", recExact.Code, recExact.Body.String())
		}
		if lastCreatedMatcherReq.Pattern != "(?P<city>[)" {
			t.Fatalf("expected exact pattern preserved, got: %q", lastCreatedMatcherReq.Pattern)
		}

		// Meta-characters allowed in contains mode
		reqContains := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{"name":"m_contains","pattern":"(?P<city>[)","match_type":"contains"}`)))
		recContains := httptest.NewRecorder()
		svc.ServeHTTP(recContains, reqContains)
		if recContains.Code != http.StatusCreated {
			t.Fatalf("expected 201 for meta-characters in contains match_type, got %d (body: %s)", recContains.Code, recContains.Body.String())
		}
	}

	// 12. Matchers: capture_env_map ACTIONSCAT_ reserved prefix protection on target env var
	{
		// Reject when target env var uses ACTIONSCAT_ prefix
		reqReserved := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{
			"name":"m_reserved",
			"pattern":"^city (?P<city>\\w+)$",
			"match_type":"regex",
			"capture_env_map":{"city":"ACTIONSCAT_ACTION_ID"}
		}`)))
		recReserved := httptest.NewRecorder()
		svc.ServeHTTP(recReserved, reqReserved)
		if recReserved.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for reserved ACTIONSCAT_ prefix in capture_env_map, got %d", recReserved.Code)
		}
		if !strings.Contains(recReserved.Body.String(), "uses reserved prefix ACTIONSCAT_") {
			t.Fatalf("expected reserved prefix error, got: %s", recReserved.Body.String())
		}

		// Allow valid capture mapping
		reqValid := newReq(http.MethodPost, "/api/actionscat/actions/act_cron/matchers", bytes.NewReader([]byte(`{
			"name":"m_valid",
			"pattern":"^city (?P<city>\\w+)$",
			"match_type":"regex",
			"capture_env_map":{"city":"CITY"}
		}`)))
		recValid := httptest.NewRecorder()
		svc.ServeHTTP(recValid, reqValid)
		if recValid.Code != http.StatusCreated {
			t.Fatalf("expected 201 for valid capture_env_map, got %d (body: %s)", recValid.Code, recValid.Body.String())
		}
		if lastCreatedMatcherReq.CaptureEnvMap["city"] != "CITY" {
			t.Fatalf("expected capture_env_map city->CITY, got: %+v", lastCreatedMatcherReq.CaptureEnvMap)
		}
	}
}
