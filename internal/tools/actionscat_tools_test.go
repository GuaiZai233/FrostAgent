package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/actionscat"
)

func TestActionsCatTools_Unconfigured(t *testing.T) {
	client := actionscat.New(func(k string) string { return "" })
	ctx := context.Background()

	listTool := ActionsCatListActionsTool(client)
	out, err := listTool.ExecuteContext(ctx, "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured message, got: %s", out)
	}

	runTool := ActionsCatRunActionTool(client)
	out, err = runTool.ExecuteContext(ctx, `{"action_id": "act_1"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured message, got: %s", out)
	}

	getRunTool := ActionsCatGetRunTool(client)
	out, err = getRunTool.ExecuteContext(ctx, `{"action_id": "act_1", "run_id": "run_1"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured message, got: %s", out)
	}
}

func TestActionsCatTools_Execution(t *testing.T) {
	mockAction1 := actionscat.Action{
		ID:          "act_test_1",
		Name:        "Test Action 1",
		Description: "First test action",
		Enabled:     true,
	}
	mockAction2 := actionscat.Action{
		ID:          "act_test_2",
		Name:        "Test Action 2",
		Description: "Second test action (disabled)",
		Enabled:     false,
	}

	mockRun := actionscat.Run{
		ID:          "run_123",
		ActionID:    "act_test_1",
		Status:      "succeeded",
		TriggerType: "manual",
		Stdout:      "Hello from ActionsCat tool test!",
		Stderr:      "",
		DurationMs:  250,
		CreatedAt:   time.Now().UTC(),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/actions":
			_ = json.NewEncoder(w).Encode([]actionscat.Action{mockAction1, mockAction2})
		case "/api/v1/actions/act_test_1/runs":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(mockRun)
			}
		case "/api/v1/actions/act_test_1/runs/run_123":
			_ = json.NewEncoder(w).Encode(mockRun)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := actionscat.New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "mock_token"
		default:
			return ""
		}
	})

	ctx := context.Background()

	// 1. Test ListActionsTool (all)
	listTool := ActionsCatListActionsTool(client)
	out, err := listTool.ExecuteContext(ctx, "{}")
	if err != nil {
		t.Fatalf("ListActionsTool failed: %v", err)
	}
	if !strings.Contains(out, "act_test_1") || !strings.Contains(out, "act_test_2") {
		t.Fatalf("expected both actions in output, got: %s", out)
	}

	// 2. Test ListActionsTool (enabled_only)
	outEnabled, err := listTool.ExecuteContext(ctx, `{"enabled_only": true}`)
	if err != nil {
		t.Fatalf("ListActionsTool with enabled_only failed: %v", err)
	}
	if !strings.Contains(outEnabled, "act_test_1") || strings.Contains(outEnabled, "act_test_2") {
		t.Fatalf("expected only enabled action, got: %s", outEnabled)
	}

	// 3. Test RunActionTool
	runTool := ActionsCatRunActionTool(client)
	// Missing action_id
	badOut, _ := runTool.ExecuteContext(ctx, `{}`)
	if !strings.Contains(badOut, "缺少必填参数") {
		t.Fatalf("expected missing param warning, got: %s", badOut)
	}

	// Valid run execution
	runOut, err := runTool.ExecuteContext(ctx, `{"action_id": "act_test_1", "extra_env": {"FOO": "BAR"}}`)
	if err != nil {
		t.Fatalf("RunActionTool failed: %v", err)
	}
	if !strings.Contains(runOut, "run_123") || !strings.Contains(runOut, "succeeded") {
		t.Fatalf("unexpected run output: %s", runOut)
	}

	// 4. Test GetRunTool
	getRunTool := ActionsCatGetRunTool(client)
	getOut, err := getRunTool.ExecuteContext(ctx, `{"action_id": "act_test_1", "run_id": "run_123"}`)
	if err != nil {
		t.Fatalf("GetRunTool failed: %v", err)
	}
	if !strings.Contains(getOut, "Hello from ActionsCat tool test!") {
		t.Fatalf("expected stdout in get run output, got: %s", getOut)
	}

	// Test GetRunTool without logs
	getNoLogs, err := getRunTool.ExecuteContext(ctx, `{"action_id": "act_test_1", "run_id": "run_123", "include_logs": false}`)
	if err != nil {
		t.Fatalf("GetRunTool without logs failed: %v", err)
	}
	if strings.Contains(getNoLogs, "Hello from ActionsCat tool test!") {
		t.Fatalf("expected logs to be stripped, got: %s", getNoLogs)
	}
}
