package actionscat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_NotConfigured(t *testing.T) {
	client := New(func(k string) string { return "" })
	if client.IsConfigured() {
		t.Fatal("expected client to not be configured")
	}

	ctx := context.Background()
	_, err := client.ListActions(ctx)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}

	st := client.Status(ctx)
	if st.Configured || st.Healthy {
		t.Fatalf("expected unconfigured status, got %+v", st)
	}
}

func TestClient_InvalidURL(t *testing.T) {
	client := New(func(k string) string {
		if k == "ACTIONSCAT_ENDPOINT" {
			return "ftp://invalid-scheme"
		}
		return ""
	})

	ctx := context.Background()
	_, err := client.Health(ctx)
	if !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("expected ErrInvalidURL, got %v", err)
	}
}

func TestClient_HealthAndStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := New(func(k string) string {
		if k == "ACTIONSCAT_ENDPOINT" {
			return ts.URL
		}
		return ""
	})

	ctx := context.Background()
	hs, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if hs.Status != "ok" {
		t.Fatalf("expected status ok, got %s", hs.Status)
	}

	status := client.Status(ctx)
	if !status.Configured || !status.Healthy {
		t.Fatalf("expected configured and healthy, got %+v", status)
	}
}

func TestClient_ActionsAndRuns(t *testing.T) {
	const expectedToken = "test_mgmt_token_xyz"
	mockAction := Action{
		ID:          "act_test_1",
		Name:        "Test Action",
		Description: "A test action for integration",
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	mockRun := Run{
		ID:          "run_test_100",
		ActionID:    "act_test_1",
		Status:      "succeeded",
		TriggerType: "manual",
		Stdout:      "Execution successful",
		Stderr:      "",
		DurationMs:  120,
		CreatedAt:   time.Now().UTC(),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+expectedToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		switch r.URL.Path {
		case "/api/v1/actions":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]Action{mockAction})
				return
			}
		case "/api/v1/actions/act_test_1":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(mockAction)
				return
			}
		case "/api/v1/actions/act_test_1/runs":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(mockRun)
				return
			}
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]Run{mockRun})
				return
			}
		case "/api/v1/actions/act_test_1/runs/run_test_100":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(mockRun)
				return
			}
		case "/api/v1/actions/act_test_1/runs/run_test_100/logs":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(RunLogs{
					Stdout: mockRun.Stdout,
					Stderr: mockRun.Stderr,
				})
				return
			}
		case "/api/v1/actions/act_missing":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return expectedToken
		default:
			return ""
		}
	})

	ctx := context.Background()

	// 1. List Actions
	actions, err := client.ListActions(ctx)
	if err != nil {
		t.Fatalf("ListActions failed: %v", err)
	}
	if len(actions) != 1 || actions[0].ID != "act_test_1" {
		t.Fatalf("unexpected actions: %+v", actions)
	}

	// 2. Get Action
	act, err := client.GetAction(ctx, "act_test_1")
	if err != nil {
		t.Fatalf("GetAction failed: %v", err)
	}
	if act.Name != "Test Action" {
		t.Fatalf("unexpected action name: %s", act.Name)
	}

	// 3. Get Missing Action -> ErrNotFound
	_, err = client.GetAction(ctx, "act_missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing action, got %v", err)
	}

	// 4. Trigger Run
	run, err := client.TriggerRun(ctx, "act_test_1", ManualRunReq{
		ExtraEnv: map[string]string{"ENV_A": "VAL_A"},
	})
	if err != nil {
		t.Fatalf("TriggerRun failed: %v", err)
	}
	if run.ID != "run_test_100" || run.Status != "succeeded" {
		t.Fatalf("unexpected run: %+v", run)
	}

	// 5. TriggerRunAndWait
	runWait, err := client.TriggerRunAndWait(ctx, "act_test_1", ManualRunReq{}, 5*time.Second)
	if err != nil {
		t.Fatalf("TriggerRunAndWait failed: %v", err)
	}
	if !runWait.IsTerminal() {
		t.Fatalf("expected terminal run, got status %s", runWait.Status)
	}

	// 6. List Runs
	runs, err := client.ListRuns(ctx, "act_test_1", 10, 0)
	if err != nil {
		t.Fatalf("ListRuns failed: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run_test_100" {
		t.Fatalf("unexpected runs: %+v", runs)
	}

	// 7. Get Run
	rObj, err := client.GetRun(ctx, "act_test_1", "run_test_100")
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if rObj.Stdout != "Execution successful" {
		t.Fatalf("unexpected stdout: %s", rObj.Stdout)
	}

	// 8. Get Run Logs
	logs, err := client.GetRunLogs(ctx, "act_test_1", "run_test_100")
	if err != nil {
		t.Fatalf("GetRunLogs failed: %v", err)
	}
	if logs.Stdout != "Execution successful" {
		t.Fatalf("unexpected log stdout: %s", logs.Stdout)
	}
}

func TestClient_Dispatch(t *testing.T) {
	const dispatchToken = "disp_token_123"
	var receivedEvent map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/dispatch" && r.Method == http.MethodPost {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+dispatchToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&receivedEvent)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"matched": true})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_DISPATCH_TOKEN":
			return dispatchToken
		default:
			return ""
		}
	})

	err := client.Dispatch(context.Background(), map[string]any{
		"platform": "mock",
		"content":  "test dispatch",
	})
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	if receivedEvent["content"] != "test dispatch" {
		t.Fatalf("unexpected received event: %+v", receivedEvent)
	}
}
