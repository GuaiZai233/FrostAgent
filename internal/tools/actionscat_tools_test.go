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

	listBuildsTool := ActionsCatListBuildsTool(client)
	out, err = listBuildsTool.ExecuteContext(noCtx, `{"action_id": "act_1"}`)
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

	// create_version
	createVerTool := ActionsCatCreateVersionTool(client, scope)
	out, _ = createVerTool.ExecuteContext(noCtx, `{"action_id": "act_1", "files": {"main.go": "pkg"}}`)
	if !strings.Contains(out, "无法获取调用者会话上下文") {
		t.Fatalf("expected context missing, got: %s", out)
	}
	out, _ = createVerTool.ExecuteContext(normalCtx, `{"action_id": "act_1", "files": {"main.go": "pkg"}}`)
	if !strings.Contains(out, "权限不足") {
		t.Fatalf("expected permission denied, got: %s", out)
	}
	out, _ = createVerTool.ExecuteContext(adminCtx, `{"action_id": "act_1", "files": {"main.go": "pkg"}}`)
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured, got: %s", out)
	}

	// build_version
	buildVerTool := ActionsCatBuildVersionTool(client, scope)
	out, _ = buildVerTool.ExecuteContext(normalCtx, `{"action_id": "act_1", "version_id": "ver_1"}`)
	if !strings.Contains(out, "权限不足") {
		t.Fatalf("expected permission denied, got: %s", out)
	}
	out, _ = buildVerTool.ExecuteContext(adminCtx, `{"action_id": "act_1", "version_id": "ver_1"}`)
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured, got: %s", out)
	}

	// get_build
	getBuildTool := ActionsCatGetBuildTool(client)
	out, _ = getBuildTool.ExecuteContext(noCtx, `{"action_id": "act_1", "build_id": "bld_1"}`)
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured, got: %s", out)
	}

	// activate_build
	activateTool := ActionsCatActivateBuildTool(client, scope)
	out, _ = activateTool.ExecuteContext(normalCtx, `{"action_id": "act_1", "version_id": "ver_1", "build_id": "bld_1"}`)
	if !strings.Contains(out, "权限不足") {
		t.Fatalf("expected permission denied, got: %s", out)
	}
	out, _ = activateTool.ExecuteContext(adminCtx, `{"action_id": "act_1", "version_id": "ver_1", "build_id": "bld_1"}`)
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured, got: %s", out)
	}

	// deploy_action
	deployTool := ActionsCatDeployActionTool(client, scope)
	out, _ = deployTool.ExecuteContext(normalCtx, `{"action_id": "act_1", "files": {"main.go": "pkg"}}`)
	if !strings.Contains(out, "权限不足") {
		t.Fatalf("expected permission denied, got: %s", out)
	}
	out, _ = deployTool.ExecuteContext(adminCtx, `{"action_id": "act_1", "files": {"main.go": "pkg"}}`)
	if !strings.Contains(out, "尚未配置") {
		t.Fatalf("expected unconfigured, got: %s", out)
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
		case "/api/v1/actions/act_test_3":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(mockAction3)
				return
			}
		case "/api/v1/actions/act_test_3/versions":
			if r.Method == http.MethodPost {
				var req actionscat.CreateVersionReq
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.WriteHeader(http.StatusCreated)
				verID := "ver_101"
				if _, hasFail := req.Files["fail.go"]; hasFail {
					verID = "ver_fail"
				}
				_ = json.NewEncoder(w).Encode(actionscat.ActionVersion{
					ID:            verID,
					ActionID:      "act_test_3",
					VersionNumber: 1,
					SourceDigest:  "sha256:abc101",
					CreatedAt:     time.Now().UTC(),
				})
				return
			}
		case "/api/v1/actions/act_test_3/versions/ver_101/builds":
			if r.Method == http.MethodPost {
				_ = json.NewEncoder(w).Encode(actionscat.ArtifactBuild{
					ID:             "bld_101",
					ActionID:       "act_test_3",
					VersionID:      "ver_101",
					Status:         "succeeded",
					ArtifactDigest: "sha256:art101",
					ArtifactSize:   4096,
					Stdout:         "compile ok",
					CreatedAt:      time.Now().UTC(),
				})
				return
			}
		case "/api/v1/actions/act_test_3/versions/ver_fail/builds":
			if r.Method == http.MethodPost {
				exitCode := 1
				_ = json.NewEncoder(w).Encode(actionscat.ArtifactBuild{
					ID:        "bld_fail",
					ActionID:  "act_test_3",
					VersionID: "ver_fail",
					Status:    "failed",
					ExitCode:  &exitCode,
					Stderr:    "syntax error: unexpected newline",
					CreatedAt: time.Now().UTC(),
				})
				return
			}
		case "/api/v1/actions/act_test_3/builds/bld_101":
			_ = json.NewEncoder(w).Encode(actionscat.ArtifactBuild{
				ID:             "bld_101",
				ActionID:       "act_test_3",
				VersionID:      "ver_101",
				Status:         "succeeded",
				ArtifactDigest: "sha256:art101",
				Stdout:         "compile ok",
				CreatedAt:      time.Now().UTC(),
			})
			return
		case "/api/v1/actions/act_test_3/builds/bld_fail":
			exitCode := 1
			_ = json.NewEncoder(w).Encode(actionscat.ArtifactBuild{
				ID:        "bld_fail",
				ActionID:  "act_test_3",
				VersionID: "ver_fail",
				Status:    "failed",
				ExitCode:  &exitCode,
				Stderr:    "syntax error: unexpected newline",
				CreatedAt: time.Now().UTC(),
			})
			return
		case "/api/v1/actions/act_test_3/active-build":
			if r.Method == http.MethodPost {
				var req actionscat.SetActiveBuildReq
				_ = json.NewDecoder(r.Body).Decode(&req)
				mockAction3.ActiveVersionID = req.VersionID
				mockAction3.ActiveBuildID = req.BuildID
				_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
				return
			}
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

	// 7. Test CreateVersionTool
	createVerTool := ActionsCatCreateVersionTool(client, scope)
	// Missing action_id
	badVerOut, _ := createVerTool.ExecuteContext(adminCtx, `{"files": {"main.go": "pkg"}}`)
	if !strings.Contains(badVerOut, "缺少必填参数 'action_id'") {
		t.Fatalf("expected missing action_id error, got: %s", badVerOut)
	}
	// Empty files
	badFilesOut, _ := createVerTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3", "files": {}}`)
	if !strings.Contains(badFilesOut, "files 文件映射不能为空") {
		t.Fatalf("expected empty files error, got: %s", badFilesOut)
	}
	// Successful version creation
	createVerOut, err := createVerTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3", "files": {"main.go": "package main"}}`)
	if err != nil {
		t.Fatalf("CreateVersionTool failed: %v", err)
	}
	if !strings.Contains(createVerOut, "ver_101") || !strings.Contains(createVerOut, "版本不可变") {
		t.Fatalf("unexpected create version output: %s", createVerOut)
	}

	// 8. Test BuildVersionTool
	buildVerTool := ActionsCatBuildVersionTool(client, scope)
	// Missing params
	badBuildOut, _ := buildVerTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3"}`)
	if !strings.Contains(badBuildOut, "缺少必填参数") {
		t.Fatalf("expected missing params error, got: %s", badBuildOut)
	}
	// Failed build
	failBuildOut, err := buildVerTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3", "version_id": "ver_fail"}`)
	if err != nil {
		t.Fatalf("BuildVersionTool for failed build returned unexpected err: %v", err)
	}
	if !strings.Contains(failBuildOut, "版本构建未成功") || !strings.Contains(failBuildOut, "syntax error") || !strings.Contains(failBuildOut, "版本具有不可变性") {
		t.Fatalf("expected failed build diagnostics and immutability notice, got: %s", failBuildOut)
	}
	// Successful build
	buildOut, err := buildVerTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3", "version_id": "ver_101"}`)
	if err != nil {
		t.Fatalf("BuildVersionTool failed: %v", err)
	}
	if !strings.Contains(buildOut, "bld_101") || !strings.Contains(buildOut, "succeeded") || !strings.Contains(buildOut, "actionscat_activate_build") {
		t.Fatalf("unexpected build output: %s", buildOut)
	}

	// 9. Test GetBuildTool
	getBuildTool := ActionsCatGetBuildTool(client)
	getBuildOut, err := getBuildTool.ExecuteContext(ctx, `{"action_id": "act_test_3", "build_id": "bld_101"}`)
	if err != nil {
		t.Fatalf("GetBuildTool failed: %v", err)
	}
	if !strings.Contains(getBuildOut, "bld_101") || !strings.Contains(getBuildOut, "compile ok") {
		t.Fatalf("unexpected get build output: %s", getBuildOut)
	}
	// Without logs
	getBuildNoLogs, err := getBuildTool.ExecuteContext(ctx, `{"action_id": "act_test_3", "build_id": "bld_101", "include_logs": false}`)
	if err != nil {
		t.Fatalf("GetBuildTool without logs failed: %v", err)
	}
	if strings.Contains(getBuildNoLogs, "compile ok") {
		t.Fatalf("expected logs stripped from get_build, got: %s", getBuildNoLogs)
	}

	// 10. Test ActivateBuildTool
	activateTool := ActionsCatActivateBuildTool(client, scope)
	// Refuse activation of failed build
	badActOut, _ := activateTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3", "version_id": "ver_fail", "build_id": "bld_fail"}`)
	if !strings.Contains(badActOut, "拒绝激活构建") || !strings.Contains(badActOut, "非 succeeded") {
		t.Fatalf("expected activation refusal for failed build, got: %s", badActOut)
	}
	// Successful activation
	actOut, err := activateTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3", "version_id": "ver_101", "build_id": "bld_101"}`)
	if err != nil {
		t.Fatalf("ActivateBuildTool failed: %v", err)
	}
	if !strings.Contains(actOut, "构建版本激活成功") || !strings.Contains(actOut, `"runnable": true`) {
		t.Fatalf("expected runnable true after activation, got: %s", actOut)
	}

	// 11. Test DeployActionTool
	deployTool := ActionsCatDeployActionTool(client, scope)
	// Deploy with failing code -> halts at build step with logs, does not activate
	failDeployOut, err := deployTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3", "files": {"fail.go": "bad code"}}`)
	if err != nil {
		t.Fatalf("DeployActionTool with failing code returned unexpected err: %v", err)
	}
	if !strings.Contains(failDeployOut, "部署失败") || !strings.Contains(failDeployOut, "syntax error") {
		t.Fatalf("expected deploy failure with syntax error logs, got: %s", failDeployOut)
	}

	// Deploy with valid code -> full lifecycle succeeds
	deployOut, err := deployTool.ExecuteContext(adminCtx, `{"action_id": "act_test_3", "files": {"main.go": "package main"}}`)
	if err != nil {
		t.Fatalf("DeployActionTool failed: %v", err)
	}
	if !strings.Contains(deployOut, "部署并激活成功") || !strings.Contains(deployOut, `"runnable": true`) || !strings.Contains(deployOut, `"status": "deployed"`) {
		t.Fatalf("unexpected deploy output: %s", deployOut)
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

func TestActionsCatTools_FullMutationAuthorizationMatrix(t *testing.T) {
	var mutationCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mutationCount.Add(1)
			switch {
			case strings.HasSuffix(r.URL.Path, "/versions"):
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(actionscat.ActionVersion{ID: "ver_mock_1", ActionID: "act_1"})
				return
			case strings.HasSuffix(r.URL.Path, "/builds"):
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(actionscat.ArtifactBuild{ID: "bld_mock_1", Status: "succeeded"})
				return
			case strings.HasSuffix(r.URL.Path, "/active-build"):
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
				return
			}
		}
		if r.Method == http.MethodGet {
			if strings.Contains(r.URL.Path, "/builds/") {
				_ = json.NewEncoder(w).Encode(actionscat.ArtifactBuild{
					ID:        "bld_1",
					VersionID: "ver_1",
					Status:    "succeeded",
				})
				return
			}
			if strings.Contains(r.URL.Path, "/actions/act_1") {
				_ = json.NewEncoder(w).Encode(actionscat.Action{ID: "act_1", Enabled: true, ActiveBuildID: "bld_mock_1"})
				return
			}
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
		admincmd.AdminQQIDsEnv: "10001",
	})

	noCtx := context.Background()
	normalUserCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "99999",
		Mock:        false,
	})
	mockAdminCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "10001",
		Mock:        true,
	})
	adminCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "10001",
		Mock:        false,
	})

	testCases := []struct {
		name       string
		tool       Tool
		args       string
		mockErrMsg string
	}{
		{
			name:       "actionscat_create_version",
			tool:       ActionsCatCreateVersionTool(client, scope),
			args:       `{"action_id": "act_1", "files": {"main.go": "pkg"}}`,
			mockErrMsg: "模拟会话模式下禁用 ActionsCat 创建版本",
		},
		{
			name:       "actionscat_build_version",
			tool:       ActionsCatBuildVersionTool(client, scope),
			args:       `{"action_id": "act_1", "version_id": "ver_1"}`,
			mockErrMsg: "模拟会话模式下禁用 ActionsCat 构建版本",
		},
		{
			name:       "actionscat_activate_build",
			tool:       ActionsCatActivateBuildTool(client, scope),
			args:       `{"action_id": "act_1", "version_id": "ver_1", "build_id": "bld_1"}`,
			mockErrMsg: "模拟会话模式下禁用 ActionsCat 激活构建",
		},
		{
			name:       "actionscat_deploy_action",
			tool:       ActionsCatDeployActionTool(client, scope),
			args:       `{"action_id": "act_1", "files": {"main.go": "pkg"}}`,
			mockErrMsg: "模拟会话模式下禁用 ActionsCat 部署操作",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			before := mutationCount.Load()

			// 1. Missing context
			out, err := tc.tool.ExecuteContext(noCtx, tc.args)
			if err != nil {
				t.Fatalf("unexpected error on missing context: %v", err)
			}
			if !strings.Contains(out, "无法获取调用者会话上下文") {
				t.Fatalf("expected missing context error, got: %s", out)
			}
			if mutationCount.Load() != before {
				t.Fatalf("expected 0 calls on missing context")
			}

			// 2. Normal user (non-admin)
			out, err = tc.tool.ExecuteContext(normalUserCtx, tc.args)
			if err != nil {
				t.Fatalf("unexpected error on non-admin user: %v", err)
			}
			if !strings.Contains(out, "权限不足") {
				t.Fatalf("expected permission denied for non-admin, got: %s", out)
			}
			if mutationCount.Load() != before {
				t.Fatalf("expected 0 calls on non-admin user")
			}

			// 3. Mock session
			out, err = tc.tool.ExecuteContext(mockAdminCtx, tc.args)
			if err != nil {
				t.Fatalf("unexpected error on mock session: %v", err)
			}
			if !strings.Contains(out, tc.mockErrMsg) {
				t.Fatalf("expected mock refusal (%s), got: %s", tc.mockErrMsg, out)
			}
			if mutationCount.Load() != before {
				t.Fatalf("expected 0 calls on mock session")
			}

			// 4. Admin in normal session -> proceeds
			out, err = tc.tool.ExecuteContext(adminCtx, tc.args)
			if err != nil {
				t.Fatalf("unexpected error on admin: %v", err)
			}
			if mutationCount.Load() <= before {
				t.Fatalf("expected mutation calls for admin, got %d (before: %d)", mutationCount.Load(), before)
			}
		})
	}
}

func TestActionsCatTools_BuildTimeoutHandling(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/versions") {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(actionscat.ActionVersion{ID: "ver_timeout_1", ActionID: "act_timeout"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/builds") {
			// Simulate long-running build by sleeping past short client context deadline
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(actionscat.ArtifactBuild{ID: "bld_1", Status: "succeeded"})
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
		admincmd.AdminQQIDsEnv: "10001",
	})

	adminCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "10001",
		Mock:        false,
	})

	// 1. BuildVersionTool timeout
	buildTool := ActionsCatBuildVersionTool(client, scope)
	timeoutCtx, cancel := context.WithTimeout(adminCtx, 30*time.Millisecond)
	defer cancel()

	out, err := buildTool.ExecuteContext(timeoutCtx, `{"action_id": "act_timeout", "version_id": "ver_timeout_1"}`)
	if err != nil {
		t.Fatalf("BuildVersionTool returned unexpected err: %v", err)
	}
	if !strings.Contains(out, "构建请求超时或网络传输中断，构建结果未知") || !strings.Contains(out, "actionscat_list_builds") || !strings.Contains(out, "严禁立即重复触发构建") {
		t.Fatalf("expected build timeout indeterminate guidance, got: %s", out)
	}

	// 2. DeployActionTool timeout
	deployTool := ActionsCatDeployActionTool(client, scope)
	timeoutCtx2, cancel2 := context.WithTimeout(adminCtx, 30*time.Millisecond)
	defer cancel2()

	deployOut, err := deployTool.ExecuteContext(timeoutCtx2, `{"action_id": "act_timeout", "files": {"main.go": "pkg"}}`)
	if err != nil {
		t.Fatalf("DeployActionTool returned unexpected err: %v", err)
	}
	if !strings.Contains(deployOut, "构建请求超时或网络传输中断，构建结果未知") || !strings.Contains(deployOut, "actionscat_list_builds") || !strings.Contains(deployOut, "严禁立即重新部署或重复提交构建") {
		t.Fatalf("expected deploy timeout indeterminate guidance, got: %s", deployOut)
	}
}


func TestActionsCatTools_EntrypointAndNetworkValidation(t *testing.T) {
	// 1. Files empty
	_, err := prepareCreateVersionReq(createVersionInput{Files: nil})
	if err == nil || !strings.Contains(err.Error(), "文件映射不能为空") {
		t.Fatalf("expected error for empty files, got: %v", err)
	}

	// 2. Entrypoint sanitization tests
	tests := []struct {
		name               string
		rawEntrypoint      string
		expectedEntrypoint string
	}{
		{"default when empty", "", "entrypoint"},
		{"plain relative", "entrypoint", "entrypoint"},
		{"leading slash", "/entrypoint", "entrypoint"},
		{"sandbox prefix", "/sandbox/entrypoint", "entrypoint"},
		{"custom relative", "bin/runner", "bin/runner"},
		{"custom leading slash", "/bin/runner", "bin/runner"},
		{"custom sandbox prefix", "/sandbox/out/entrypoint", "out/entrypoint"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := prepareCreateVersionReq(createVersionInput{
				Files: map[string]string{"main.go": "package main"},
				RuntimeSpec: &actionscat.RuntimeSpec{
					Entrypoint: tc.rawEntrypoint,
				},
			})
			if err != nil {
				t.Fatalf("prepareCreateVersionReq failed: %v", err)
			}
			if req.RuntimeSpec.Entrypoint != tc.expectedEntrypoint {
				t.Fatalf("entrypoint %q: expected %q, got %q", tc.rawEntrypoint, tc.expectedEntrypoint, req.RuntimeSpec.Entrypoint)
			}
		})
	}

	// 3. Network mode validation
	validModes := []struct {
		input    string
		expected string
	}{
		{"", "none"},
		{"none", "none"},
		{"NONE", "none"},
		{"public", "public"},
		{"Public", "public"},
		{"isolated", "isolated"},
		{"ISOLATED", "isolated"},
		{"allowlist", "allowlist"},
		{"ALLOWLIST", "allowlist"},
	}
	for _, tc := range validModes {
		reqInput := createVersionInput{
			Files: map[string]string{"main.go": "package main"},
			RuntimeSpec: &actionscat.RuntimeSpec{
				Network: actionscat.NetworkPolicy{
					Mode: tc.input,
				},
			},
		}
		if strings.ToLower(tc.input) == "allowlist" {
			reqInput.RuntimeSpec.Network.Allow = []actionscat.NetworkAllowRule{
				{Host: "api.example.com", Port: 443},
			}
		}
		req, err := prepareCreateVersionReq(reqInput)
		if err != nil {
			t.Fatalf("mode %q: unexpected error: %v", tc.input, err)
		}
		if req.RuntimeSpec.Network.Mode != tc.expected {
			t.Fatalf("mode %q: expected %q, got %q", tc.input, tc.expected, req.RuntimeSpec.Network.Mode)
		}
	}

	// Invalid network modes (such as "bridge" or "custom")
	invalidModes := []string{"bridge", "BRIDGE", "host", "container:123", "overlay"}
	for _, mode := range invalidModes {
		_, err := prepareCreateVersionReq(createVersionInput{
			Files: map[string]string{"main.go": "package main"},
			RuntimeSpec: &actionscat.RuntimeSpec{
				Network: actionscat.NetworkPolicy{
					Mode: mode,
				},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "无效的网络模式") {
			t.Fatalf("mode %q: expected invalid network mode error, got: %v", mode, err)
		}
	}

	// 4. Network allowlist rules validation
	// 4a. Allowlist with empty allow list
	_, err = prepareCreateVersionReq(createVersionInput{
		Files: map[string]string{"main.go": "package main"},
		RuntimeSpec: &actionscat.RuntimeSpec{
			Network: actionscat.NetworkPolicy{
				Mode:  "allowlist",
				Allow: nil,
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "allow 规则列表不能为空") {
		t.Fatalf("expected empty allow rules error, got: %v", err)
	}

	// 4b. Allowlist rule with empty host
	_, err = prepareCreateVersionReq(createVersionInput{
		Files: map[string]string{"main.go": "package main"},
		RuntimeSpec: &actionscat.RuntimeSpec{
			Network: actionscat.NetworkPolicy{
				Mode: "allowlist",
				Allow: []actionscat.NetworkAllowRule{
					{Host: "   ", Port: 443},
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "host 不能为空") {
		t.Fatalf("expected empty host error, got: %v", err)
	}

	// 4c. Allowlist rule with invalid port
	invalidPorts := []int{0, -1, 65536, 70000}
	for _, port := range invalidPorts {
		_, err = prepareCreateVersionReq(createVersionInput{
			Files: map[string]string{"main.go": "package main"},
			RuntimeSpec: &actionscat.RuntimeSpec{
				Network: actionscat.NetworkPolicy{
					Mode: "allowlist",
					Allow: []actionscat.NetworkAllowRule{
						{Host: "api.example.com", Port: port},
					},
				},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "无效，必须在 1-65535 之间") {
			t.Fatalf("port %d: expected port out of range error, got: %v", port, err)
		}
	}

	// 4d. Valid allowlist rules
	req, err := prepareCreateVersionReq(createVersionInput{
		Files: map[string]string{"main.go": "package main"},
		RuntimeSpec: &actionscat.RuntimeSpec{
			Network: actionscat.NetworkPolicy{
				Mode: "allowlist",
				Allow: []actionscat.NetworkAllowRule{
					{Host: "api.example.com", Port: 443},
					{Host: "db.internal", Port: 5432},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected valid allowlist rules to pass, got: %v", err)
	}
	if len(req.RuntimeSpec.Network.Allow) != 2 {
		t.Fatalf("expected 2 allow rules, got %d", len(req.RuntimeSpec.Network.Allow))
	}
}

func TestActionsCatTools_ListBuildsTool(t *testing.T) {
	exitCodeZero := 0
	exitCodeOne := 1

	mockBuilds := []actionscat.ArtifactBuild{
		{
			ID:          "bld_101",
			ActionID:    "act_demo",
			VersionID:   "ver_1",
			BuildNumber: 1,
			Status:      "succeeded",
			ExitCode:    &exitCodeZero,
			CreatedAt:   time.Now().Add(-10 * time.Minute).UTC(),
		},
		{
			ID:          "bld_102",
			ActionID:    "act_demo",
			VersionID:   "ver_2",
			BuildNumber: 2,
			Status:      "failed",
			ExitCode:    &exitCodeOne,
			Stderr:      "compile error in main.go: syntax error",
			CreatedAt:   time.Now().Add(-5 * time.Minute).UTC(),
		},
		{
			ID:          "bld_103",
			ActionID:    "act_demo",
			VersionID:   "ver_1",
			BuildNumber: 3,
			Status:      "building",
			CreatedAt:   time.Now().UTC(),
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/actions/act_demo/builds" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(mockBuilds)
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
			return "valid_token"
		default:
			return ""
		}
	})

	tool := ActionsCatListBuildsTool(client)
	ctx := context.Background()

	// 1. Missing action_id
	out, err := tool.ExecuteContext(ctx, `{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "缺少必填参数 'action_id'") {
		t.Fatalf("expected action_id required error, got: %s", out)
	}

	// 2. Query all builds (no version_id filter)
	out, err = tool.ExecuteContext(ctx, `{"action_id": "act_demo"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res []AgentBuildDTO
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("failed to parse JSON result: %v (raw: %s)", err, out)
	}
	if len(res) != 3 {
		t.Fatalf("expected 3 builds, got %d", len(res))
	}
	if res[0].ID != "bld_101" || res[1].ID != "bld_102" || res[2].ID != "bld_103" {
		t.Fatalf("unexpected builds order: %+v", res)
	}

	// 3. Query with version_id filter ("ver_1")
	out, err = tool.ExecuteContext(ctx, `{"action_id": "act_demo", "version_id": "ver_1"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var filtered []AgentBuildDTO
	if err := json.Unmarshal([]byte(out), &filtered); err != nil {
		t.Fatalf("failed to parse filtered JSON result: %v (raw: %s)", err, out)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 builds for ver_1, got %d", len(filtered))
	}
	for _, b := range filtered {
		if b.VersionID != "ver_1" {
			t.Fatalf("expected version_id ver_1, got %s", b.VersionID)
		}
	}

	// 4. Query with non-existent version_id
	out, err = tool.ExecuteContext(ctx, `{"action_id": "act_demo", "version_id": "ver_nonexistent"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "未查询到") || !strings.Contains(out, "ver_nonexistent") {
		t.Fatalf("expected empty/not found message, got: %s", out)
	}
}
