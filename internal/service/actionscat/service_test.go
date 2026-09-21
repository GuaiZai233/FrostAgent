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
