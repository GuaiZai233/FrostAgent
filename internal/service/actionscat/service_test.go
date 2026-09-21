package actionscat

import (
	"bytes"
	"encoding/json"
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

	// 1. GET /api/actionscat/status
	{
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/status", nil)
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
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/actions", nil)
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
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/actions/act_weather", nil)
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
		req := httptest.NewRequest(http.MethodPost, "/api/actionscat/actions/act_weather/runs", bytes.NewReader(body))
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
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/actions/act_weather/runs?limit=10", nil)
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
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/actions/act_weather/runs/run_weather_01", nil)
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
		req := httptest.NewRequest(http.MethodGet, "/api/actionscat/actions/act_weather/runs/run_weather_01/logs", nil)
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

	// 1. POST runs with malformed JSON
	{
		req := httptest.NewRequest(http.MethodPost, "/api/actionscat/actions/act_1/runs", bytes.NewReader([]byte("{invalid-json")))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json, got %d", rec.Code)
		}
	}

	// 2. POST runs with trailing tokens
	{
		req := httptest.NewRequest(http.MethodPost, "/api/actionscat/actions/act_1/runs", bytes.NewReader([]byte(`{"extra_env":{}} {"extra": true}`)))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for trailing json, got %d", rec.Code)
		}
	}

	// 3. POST runs with reserved ACTIONSCAT_ prefix
	{
		req := httptest.NewRequest(http.MethodPost, "/api/actionscat/actions/act_1/runs", bytes.NewReader([]byte(`{"extra_env":{"ACTIONSCAT_OVERRIDE":"val"}}`)))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for reserved prefix, got %d", rec.Code)
		}
	}

	// 4. POST dispatch with reserved ACTIONSCAT_ prefix
	{
		req := httptest.NewRequest(http.MethodPost, "/api/actionscat/dispatch", bytes.NewReader([]byte(`{"extra_env":{"ACTIONSCAT_ACTION_ID":"val"}}`)))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for reserved prefix in dispatch, got %d", rec.Code)
		}
	}

	// 5. POST dispatch with malformed JSON
	{
		req := httptest.NewRequest(http.MethodPost, "/api/actionscat/dispatch", bytes.NewReader([]byte(`{"unclosed`)))
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json in dispatch, got %d", rec.Code)
		}
	}
}
