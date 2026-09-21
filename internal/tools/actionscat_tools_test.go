package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/actionscat"
	"FrostAgent/internal/llm"
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

	createTool := ActionsCatCreateActionTool(client)
	out, err = createTool.ExecuteContext(ctx, `{"name": "New Action"}`)
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
		PlannedEnv: map[string]string{
			"PRIVATE_STATE":            "do-not-leak",
			"ACTIONSCAT_RUNTIME_TOKEN": "super-secret-token",
		},
		Stdout:     "Hello from ActionsCat tool test!",
		Stderr:     "",
		DurationMs: 250,
		CreatedAt:  time.Now().UTC(),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/actions":
			if r.Method == http.MethodPost {
				var req actionscat.CreateActionReq
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(actionscat.Action{
					ID:          "act_created_123",
					Name:        req.Name,
					Description: req.Description,
					Enabled:     true,
				})
				return
			}
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

	// Rejected protected ACTIONSCAT_ environment variables
	badEnvOut, _ := runTool.ExecuteContext(ctx, `{"action_id": "act_test_1", "extra_env": {"ACTIONSCAT_TRIGGER_TYPE": "hacked"}}`)
	if !strings.Contains(badEnvOut, "包含受保护的前缀 'ACTIONSCAT_'") {
		t.Fatalf("expected protected prefix error, got: %s", badEnvOut)
	}

	// Valid run execution
	runOut, err := runTool.ExecuteContext(ctx, `{"action_id": "act_test_1", "extra_env": {"FOO": "BAR"}}`)
	if err != nil {
		t.Fatalf("RunActionTool failed: %v", err)
	}
	if !strings.Contains(runOut, "run_123") || !strings.Contains(runOut, "succeeded") {
		t.Fatalf("unexpected run output: %s", runOut)
	}
	// Verify sensitive planned_env is redacted from agent response
	if strings.Contains(runOut, "do-not-leak") || strings.Contains(runOut, "PRIVATE_STATE") || strings.Contains(runOut, "planned_env") {
		t.Fatalf("expected planned_env secrets to be redacted from agent response, got: %s", runOut)
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
	if strings.Contains(getOut, "do-not-leak") || strings.Contains(getOut, "PRIVATE_STATE") || strings.Contains(getOut, "planned_env") {
		t.Fatalf("expected planned_env secrets to be redacted from get_run response, got: %s", getOut)
	}

	// Test GetRunTool without logs
	getNoLogs, err := getRunTool.ExecuteContext(ctx, `{"action_id": "act_test_1", "run_id": "run_123", "include_logs": false}`)
	if err != nil {
		t.Fatalf("GetRunTool without logs failed: %v", err)
	}
	if strings.Contains(getNoLogs, "Hello from ActionsCat tool test!") {
		t.Fatalf("expected logs to be stripped, got: %s", getNoLogs)
	}

	// 5. Test CreateActionTool
	createTool := ActionsCatCreateActionTool(client)
	badCreateOut, _ := createTool.ExecuteContext(ctx, `{}`)
	if !strings.Contains(badCreateOut, "缺少必填参数 'name'") {
		t.Fatalf("expected missing name error, got: %s", badCreateOut)
	}

	createOut, err := createTool.ExecuteContext(ctx, `{"name": "Created Action", "description": "Desc"}`)
	if err != nil {
		t.Fatalf("CreateActionTool failed: %v", err)
	}
	if !strings.Contains(createOut, "act_created_123") || !strings.Contains(createOut, "Created Action") {
		t.Fatalf("unexpected create action output: %s", createOut)
	}
}

func TestActionsCatRunActionTool_MockSessionRefusal(t *testing.T) {
	var postCount atomic.Int32
	mockRun := actionscat.Run{
		ID:          "run_mock_001",
		ActionID:    "act_mock_target",
		Status:      "succeeded",
		TriggerType: "manual",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/runs") {
			postCount.Add(1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(mockRun)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/actions/act_mock_target/runs/") {
			_ = json.NewEncoder(w).Encode(mockRun)
			return
		}
		http.NotFound(w, r)
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

	runTool := ActionsCatRunActionTool(client)

	// 1. When running in a mock session (?mock=true / RunContext.Mock = true),
	// execution MUST be refused without calling the backend TriggerRun API.
	mockCtx := llm.WithRunContext(context.Background(), llm.RunContext{Mock: true})
	out, err := runTool.ExecuteContext(mockCtx, `{"action_id": "act_mock_target"}`)
	if err != nil {
		t.Fatalf("unexpected error from mock tool execution: %v", err)
	}
	if !strings.Contains(out, "模拟会话模式下禁用 ActionsCat 执行") {
		t.Fatalf("expected mock session refusal message, got: %s", out)
	}
	if calls := postCount.Load(); calls != 0 {
		t.Fatalf("expected 0 calls to backend TriggerRun in mock mode, got %d", calls)
	}

	// 2. When running in a normal session (Mock = false), TriggerRun should proceed normally.
	normalCtx := llm.WithRunContext(context.Background(), llm.RunContext{Mock: false})
	normalOut, err := runTool.ExecuteContext(normalCtx, `{"action_id": "act_mock_target"}`)
	if err != nil {
		t.Fatalf("unexpected error from normal tool execution: %v", err)
	}
	if !strings.Contains(normalOut, "run_mock_001") {
		t.Fatalf("expected successful run in normal mode, got: %s", normalOut)
	}
	if calls := postCount.Load(); calls != 1 {
		t.Fatalf("expected 1 call to backend TriggerRun in normal mode, got %d", calls)
	}
}

func TestActionsCatCreateActionTool_MockSessionRefusal(t *testing.T) {
	var postCount atomic.Int32
	mockAction := actionscat.Action{
		ID:      "act_mock_created",
		Name:    "Mock Action",
		Enabled: true,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/actions" {
			postCount.Add(1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(mockAction)
			return
		}
		http.NotFound(w, r)
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

	createTool := ActionsCatCreateActionTool(client)

	// 1. In mock session, creation MUST be refused without calling the backend.
	mockCtx := llm.WithRunContext(context.Background(), llm.RunContext{Mock: true})
	out, err := createTool.ExecuteContext(mockCtx, `{"name": "Mock Action"}`)
	if err != nil {
		t.Fatalf("unexpected error from mock tool execution: %v", err)
	}
	if !strings.Contains(out, "模拟会话模式下禁用 ActionsCat 创建任务") {
		t.Fatalf("expected mock session refusal message, got: %s", out)
	}
	if calls := postCount.Load(); calls != 0 {
		t.Fatalf("expected 0 calls to backend CreateAction in mock mode, got %d", calls)
	}

	// 2. In normal session, creation proceeds normally.
	normalCtx := llm.WithRunContext(context.Background(), llm.RunContext{Mock: false})
	normalOut, err := createTool.ExecuteContext(normalCtx, `{"name": "Mock Action"}`)
	if err != nil {
		t.Fatalf("unexpected error from normal tool execution: %v", err)
	}
	if !strings.Contains(normalOut, "act_mock_created") {
		t.Fatalf("expected successful creation in normal mode, got: %s", normalOut)
	}
	if calls := postCount.Load(); calls != 1 {
		t.Fatalf("expected 1 call to backend CreateAction in normal mode, got %d", calls)
	}
}
