package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/actionscat"
	"FrostAgent/internal/admincmd"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/runtimescope"
)

func newTestScope(t *testing.T, env map[string]string) *runtimescope.Scope {
	t.Helper()
	dir := t.TempDir()
	instanceStore, err := instanceconfig.Open(filepath.Join(dir, "instance.env"), false)
	if err != nil {
		t.Fatalf("打开实例配置失败: %v", err)
	}
	globalStore, err := instanceconfig.Open(filepath.Join(dir, "global.env"), true)
	if err != nil {
		t.Fatalf("打开全局配置失败: %v", err)
	}
	for k, v := range env {
		if err := instanceStore.Update(k, v, false); err != nil {
			t.Fatalf("写入配置失败: %v", err)
		}
	}
	return runtimescope.New(instanceStore, globalStore, nil)
}

func TestActionsCatTools_Unconfigured(t *testing.T) {
	client := actionscat.New(func(k string) string { return "" })
	scope := newTestScope(t, map[string]string{
		admincmd.AdminQQIDsEnv: "10001",
	})
	adminCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "10001",
	})
	normalCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "10002",
	})
	noCtx := context.Background()

	listTool := ActionsCatListActionsTool(client)
	out, err := listTool.ExecuteContext(noCtx, "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured message, got: %s", out)
	}

	runTool := ActionsCatRunActionTool(client)
	out, err = runTool.ExecuteContext(noCtx, `{"action_id": "act_1"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured message, got: %s", out)
	}

	getRunTool := ActionsCatGetRunTool(client)
	out, err = getRunTool.ExecuteContext(noCtx, `{"action_id": "act_1", "run_id": "run_1"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured message, got: %s", out)
	}

	createTool := ActionsCatCreateActionTool(client, scope)
	// Missing RunContext -> should fail permission check
	out, err = createTool.ExecuteContext(noCtx, `{"name": "New Action"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "无法获取调用者会话上下文") {
		t.Fatalf("expected context missing message, got: %s", out)
	}

	// Normal user -> should fail permission check
	out, err = createTool.ExecuteContext(normalCtx, `{"name": "New Action"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "权限不足") {
		t.Fatalf("expected permission denied message, got: %s", out)
	}

	// Admin user -> should pass permission check and report unconfigured
	out, err = createTool.ExecuteContext(adminCtx, `{"name": "New Action"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured message for admin, got: %s", out)
	}
}

func TestActionsCatTools_Execution(t *testing.T) {
	mockAction1 := actionscat.Action{
		ID:              "act_test_1",
		Name:            "Test Action 1",
		Description:     "First test action (runnable)",
		Enabled:         true,
		ActiveVersionID: "ver_1",
		ActiveBuildID:   "bld_1",
		MaxConcurrency:  2,
	}
	mockAction2 := actionscat.Action{
		ID:              "act_test_2",
		Name:            "Test Action 2",
		Description:     "Second test action (disabled)",
		Enabled:         false,
		ActiveVersionID: "ver_2",
		ActiveBuildID:   "bld_2",
		MaxConcurrency:  1,
	}
	mockAction3 := actionscat.Action{
		ID:              "act_test_3",
		Name:            "Test Action 3",
		Description:     "Third test action (enabled but unbuilt)",
		Enabled:         true,
		ActiveVersionID: "ver_3",
		ActiveBuildID:   "",
		MaxConcurrency:  1,
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
					ID:             "act_created_123",
					Name:           req.Name,
					Description:    req.Description,
					MaxConcurrency: req.MaxConcurrency,
					Enabled:        true,
				})
				return
			}
			_ = json.NewEncoder(w).Encode([]actionscat.Action{mockAction1, mockAction2, mockAction3})
		case "/api/v1/actions/act_test_1/runs":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(mockRun)
			}
		case "/api/v1/actions/act_test_3/runs":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "action has no active build",
				})
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

	scope := newTestScope(t, map[string]string{
		admincmd.AdminQQIDsEnv: "10001",
	})
	adminCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "10001",
	})

	ctx := context.Background()

	// 1. Test ListActionsTool (all actions)
	listTool := ActionsCatListActionsTool(client)
	out, err := listTool.ExecuteContext(ctx, "{}")
	if err != nil {
		t.Fatalf("ListActionsTool failed: %v", err)
	}
	if !strings.Contains(out, "act_test_1") || !strings.Contains(out, "act_test_2") || !strings.Contains(out, "act_test_3") {
		t.Fatalf("expected all 3 actions in output, got: %s", out)
	}
	if !strings.Contains(out, `"runnable": true`) || !strings.Contains(out, `"runnable": false`) {
		t.Fatalf("expected runnable field in action DTO, got: %s", out)
	}

	// 2. Test ListActionsTool (enabled_only)
	outEnabled, err := listTool.ExecuteContext(ctx, `{"enabled_only": true}`)
	if err != nil {
		t.Fatalf("ListActionsTool with enabled_only failed: %v", err)
	}
	if !strings.Contains(outEnabled, "act_test_1") || strings.Contains(outEnabled, "act_test_2") || !strings.Contains(outEnabled, "act_test_3") {
		t.Fatalf("expected enabled actions only (act_test_1 and act_test_3), got: %s", outEnabled)
	}

	// 3. Test ListActionsTool (runnable_only)
	outRunnable, err := listTool.ExecuteContext(ctx, `{"runnable_only": true}`)
	if err != nil {
		t.Fatalf("ListActionsTool with runnable_only failed: %v", err)
	}
	if !strings.Contains(outRunnable, "act_test_1") || strings.Contains(outRunnable, "act_test_2") || strings.Contains(outRunnable, "act_test_3") {
		t.Fatalf("expected only runnable action (act_test_1), got: %s", outRunnable)
	}

	// 4. Test RunActionTool
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

	// Unbuilt action execution returns friendly guidance
	unbuiltOut, _ := runTool.ExecuteContext(ctx, `{"action_id": "act_test_3"}`)
	if !strings.Contains(unbuiltOut, "尚未激活构建版本 (active_build_id 为空)") {
		t.Fatalf("expected unbuilt action guidance message, got: %s", unbuiltOut)
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

	// 5. Test GetRunTool
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

	// 6. Test CreateActionTool (admin context)
	createTool := ActionsCatCreateActionTool(client, scope)
	badCreateOut, _ := createTool.ExecuteContext(adminCtx, `{}`)
	if !strings.Contains(badCreateOut, "缺少必填参数 'name'") {
		t.Fatalf("expected missing name error, got: %s", badCreateOut)
	}

	createOut, err := createTool.ExecuteContext(adminCtx, `{"name": "Created Action", "description": "Desc", "max_concurrency": 3}`)
	if err != nil {
		t.Fatalf("CreateActionTool failed: %v", err)
	}
	if !strings.Contains(createOut, "act_created_123") || !strings.Contains(createOut, "Created Action") {
		t.Fatalf("unexpected create action output: %s", createOut)
	}
	if !strings.Contains(createOut, `"runnable": false`) || !strings.Contains(createOut, "尚未关联激活构建") {
		t.Fatalf("expected created action to note unbuilt status, got: %s", createOut)
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

func TestActionsCatCreateActionTool_AuthorizationMatrix(t *testing.T) {
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

	scope := newTestScope(t, map[string]string{
		admincmd.AdminQQIDsEnv: "10001, 10002",
	})
	createTool := ActionsCatCreateActionTool(client, scope)

	// 1. Missing RunContext -> Refuse immediately with 0 backend calls
	noCtx := context.Background()
	out, err := createTool.ExecuteContext(noCtx, `{"name": "Test Action"}`)
	if err != nil {
		t.Fatalf("unexpected error on missing context: %v", err)
	}
	if !strings.Contains(out, "无法获取调用者会话上下文") {
		t.Fatalf("expected missing context error, got: %s", out)
	}
	if calls := postCount.Load(); calls != 0 {
		t.Fatalf("expected 0 backend calls for missing context, got %d", calls)
	}

	// 2. Normal non-admin user -> Refuse immediately with 0 backend calls
	normalUserCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "99999",
		Mock:        false,
	})
	out, err = createTool.ExecuteContext(normalUserCtx, `{"name": "Test Action"}`)
	if err != nil {
		t.Fatalf("unexpected error on non-admin user: %v", err)
	}
	if !strings.Contains(out, "权限不足") {
		t.Fatalf("expected permission denied for non-admin, got: %s", out)
	}
	if calls := postCount.Load(); calls != 0 {
		t.Fatalf("expected 0 backend calls for non-admin user, got %d", calls)
	}

	// 3. Mock session (even if user is admin) -> Refuse immediately with 0 backend calls
	mockAdminCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "10001",
		Mock:        true,
	})
	out, err = createTool.ExecuteContext(mockAdminCtx, `{"name": "Test Action"}`)
	if err != nil {
		t.Fatalf("unexpected error on mock session: %v", err)
	}
	if !strings.Contains(out, "模拟会话模式下禁用 ActionsCat 创建任务") {
		t.Fatalf("expected mock refusal, got: %s", out)
	}
	if calls := postCount.Load(); calls != 0 {
		t.Fatalf("expected 0 backend calls for mock session, got %d", calls)
	}

	// 4. Admin user in normal session -> Allowed, executes 1 backend call
	adminCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "10001",
		Mock:        false,
	})
	out, err = createTool.ExecuteContext(adminCtx, `{"name": "Test Action"}`)
	if err != nil {
		t.Fatalf("unexpected error on admin user: %v", err)
	}
	if !strings.Contains(out, "act_mock_created") {
		t.Fatalf("expected successful action creation for admin, got: %s", out)
	}
	if calls := postCount.Load(); calls != 1 {
		t.Fatalf("expected exactly 1 backend call for admin user, got %d", calls)
	}
}
