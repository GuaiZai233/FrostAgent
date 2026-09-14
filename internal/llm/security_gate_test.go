package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/security"
)

type mockGateClassifier struct {
	fn func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error)
}

func (m *mockGateClassifier) Classify(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
	if m.fn != nil {
		return m.fn(ctx, input)
	}
	return security.ClassificationResult{
		RiskLevel:  security.RiskLevelNone,
		Confidence: 1.0,
	}, nil
}

type staticProvider struct {
	response *core.ChatResponse
}

func (p *staticProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	return p.response, nil
}

func TestEngineRejectsLockedActorBeforeProviderExecution(t *testing.T) {
	controller := security.NewController(t.TempDir())
	principal, err := security.NewPrincipal("test-platform", "actor-under-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Access.Lock(principal, "test lock"); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{Security: controller}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		ActorPlatform: "test-platform",
		ActorUserID:   "actor-under-test",
	})
	if !result.Silent || !errors.Is(result.Error, security.ErrLocked) {
		t.Fatalf("locked actor should be rejected before execution: silent=%v err=%v", result.Silent, result.Error)
	}
}

func TestEngineModelOutputClassifierFailure(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	controller := security.NewController(t.TempDir())
	controller.Watchdog.SetClassifier(&mockGateClassifier{
		fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
			return security.ClassificationResult{}, errors.New("upstream gateway timeout")
		},
	})
	engine := &Engine{
		MaxIterations: 1,
		ToolRegistry:  map[string]ToolExecutor{},
		Security:      controller,
		Provider: &staticProvider{
			response: &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "这是正常的模型输出",
				},
			},
		},
	}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "你好"}}, RunContext{
		ActorPlatform: "test-platform",
		ActorUserID:   "actor-001",
	})
	want := "FrostAgent安全控制：安全审查服务暂时不可用，模型输出已拦截。"
	if result.Content != want {
		t.Fatalf("expected classifier failure message %q, got %q", want, result.Content)
	}

	snapshot := logs.Snapshot()
	var watchdogErrors []logs.LogEntry
	var engineWarns []logs.LogEntry
	for _, entry := range snapshot {
		if entry.Category == logs.SYSTEM && entry.Level == logs.ERROR && strings.Contains(entry.Content, "安全审查分类器异常 (Fail-Closed):") {
			watchdogErrors = append(watchdogErrors, entry)
		}
		if entry.Category == logs.SYSTEM && entry.Level == logs.WARN && strings.Contains(entry.Content, "模型输出因安全审查服务异常被拦截:") {
			engineWarns = append(engineWarns, entry)
		}
	}
	if len(watchdogErrors) != 1 {
		t.Fatalf("expected exactly 1 Watchdog ERROR log, got %d, snapshot=%+v", len(watchdogErrors), snapshot)
	}
	if len(engineWarns) != 1 {
		t.Fatalf("expected exactly 1 Engine WARN log, got %d, snapshot=%+v", len(engineWarns), snapshot)
	}
	watchdogEvalID := extractEvalID(watchdogErrors[0].Content)
	engineEvalID := extractEvalID(engineWarns[0].Content)
	if watchdogEvalID == "" || watchdogEvalID != engineEvalID {
		t.Fatalf("expected shared eval_id between Watchdog and Engine, got watchdog=%q, engine=%q", watchdogEvalID, engineEvalID)
	}
	if strings.Contains(engineWarns[0].Content, "error_type=") || strings.Contains(engineWarns[0].Content, "reason=") {
		t.Fatalf("engine WARN log must not duplicate error_type or reason: %q", engineWarns[0].Content)
	}
	for _, entry := range snapshot {
		if entry.Level == logs.ERROR && !strings.Contains(entry.Content, "安全审查分类器异常 (Fail-Closed):") {
			t.Fatalf("unexpected ERROR log found outside Watchdog: %+v", entry)
		}
	}
}

func TestEngineModelOutputPolicyBlock(t *testing.T) {
	controller := security.NewController(t.TempDir())
	controller.Watchdog.SetClassifier(&mockGateClassifier{
		fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
			return security.ClassificationResult{
				Category:   security.RiskCategoryMaliciousExecution,
				RiskLevel:  security.RiskLevelHigh,
				Intent:     security.IntentMalicious,
				Confidence: 0.99,
				Reason:     "harmful payload detected",
			}, nil
		},
	})
	engine := &Engine{
		MaxIterations: 1,
		ToolRegistry:  map[string]ToolExecutor{},
		Security:      controller,
		Provider: &staticProvider{
			response: &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "危险脚本内容",
				},
			},
		},
	}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "生成脚本"}}, RunContext{
		ActorPlatform: "test-platform",
		ActorUserID:   "actor-002",
	})
	want := "FrostAgent安全控制：模型输出已拦截。"
	if result.Content != want {
		t.Fatalf("expected policy block message %q, got %q", want, result.Content)
	}
}

type dummyTool struct{}

func (d *dummyTool) Name() string               { return "dummy_tool" }
func (d *dummyTool) Description() string        { return "dummy tool" }
func (d *dummyTool) Parameters() map[string]any { return map[string]any{} }
func (d *dummyTool) Execute(args string) (string, error) {
	return "normal tool execution result", nil
}

func TestEngineToolArgumentClassifierFailure(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	controller := security.NewController(t.TempDir())
	controller.Watchdog.SetClassifier(&mockGateClassifier{
		fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
			if input.Stage == security.StageToolArgument {
				return security.ClassificationResult{}, errors.New("classifier offline")
			}
			return security.ClassificationResult{RiskLevel: security.RiskLevelNone, Confidence: 1.0}, nil
		},
	})

	callCount := 0
	provider := &mockMultiStepProvider{
		chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			callCount++
			if callCount == 1 {
				return &core.ChatResponse{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{{
							ID:       "call_1",
							Type:     "function",
							Function: core.ToolCallFunction{Name: "dummy_tool", Arguments: `{"q":"test"}`},
						}},
					},
				}, nil
			}
			// Second iteration gets the tool response message in history
			for _, m := range req.Messages {
				if m.Role == core.RoleTool {
					return &core.ChatResponse{
						Message: core.ChatMessage{Role: core.RoleAssistant, Content: fmt.Sprintf("got tool response: %v", m.Content)},
					}, nil
				}
			}
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "no tool msg"}}, nil
		},
	}

	engine := &Engine{
		MaxIterations: 2,
		ToolRegistry:  map[string]ToolExecutor{"dummy_tool": &dummyTool{}},
		Security:      controller,
		Provider:      provider,
	}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "run tool"}}, RunContext{
		ActorPlatform: "test-platform",
		ActorUserID:   "actor-003",
	})
	want := "got tool response: FrostAgent安全控制：安全审查服务暂时不可用，该工具调用已被阻止。"
	if result.Content != want {
		t.Fatalf("expected tool argument failure message %q, got %q", want, result.Content)
	}

	snapshot := logs.Snapshot()
	var watchdogErrors []logs.LogEntry
	var engineWarns []logs.LogEntry
	for _, entry := range snapshot {
		if entry.Category == logs.SYSTEM && entry.Level == logs.ERROR && strings.Contains(entry.Content, "安全审查分类器异常 (Fail-Closed):") {
			watchdogErrors = append(watchdogErrors, entry)
		}
		if entry.Category == logs.SYSTEM && entry.Level == logs.WARN && strings.Contains(entry.Content, "工具入参因安全审查服务异常被阻止:") {
			engineWarns = append(engineWarns, entry)
		}
	}
	if len(watchdogErrors) != 1 {
		t.Fatalf("expected exactly 1 Watchdog ERROR log, got %d, snapshot=%+v", len(watchdogErrors), snapshot)
	}
	if len(engineWarns) != 1 {
		t.Fatalf("expected exactly 1 Engine WARN log, got %d, snapshot=%+v", len(engineWarns), snapshot)
	}
	watchdogEvalID := extractEvalID(watchdogErrors[0].Content)
	engineEvalID := extractEvalID(engineWarns[0].Content)
	if watchdogEvalID == "" || watchdogEvalID != engineEvalID {
		t.Fatalf("expected shared eval_id between Watchdog and Engine, got watchdog=%q, engine=%q", watchdogEvalID, engineEvalID)
	}
	if !strings.Contains(engineWarns[0].Content, "tool=dummy_tool") {
		t.Fatalf("expected tool context in engine WARN log: %q", engineWarns[0].Content)
	}
	if strings.Contains(engineWarns[0].Content, "error_type=") || strings.Contains(engineWarns[0].Content, "reason=") {
		t.Fatalf("engine WARN log must not duplicate error_type or reason: %q", engineWarns[0].Content)
	}
	for _, entry := range snapshot {
		if entry.Level == logs.ERROR && !strings.Contains(entry.Content, "安全审查分类器异常 (Fail-Closed):") {
			t.Fatalf("unexpected ERROR log found outside Watchdog: %+v", entry)
		}
	}
}

func TestEngineToolResultClassifierFailure(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	controller := security.NewController(t.TempDir())
	controller.Watchdog.SetClassifier(&mockGateClassifier{
		fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
			if input.Stage == security.StageToolResult {
				return security.ClassificationResult{}, errors.New("classifier timeout on result")
			}
			return security.ClassificationResult{RiskLevel: security.RiskLevelNone, Confidence: 1.0}, nil
		},
	})

	callCount := 0
	provider := &mockMultiStepProvider{
		chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			callCount++
			if callCount == 1 {
				return &core.ChatResponse{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{{
							ID:       "call_2",
							Type:     "function",
							Function: core.ToolCallFunction{Name: "dummy_tool", Arguments: `{"q":"test"}`},
						}},
					},
				}, nil
			}
			for _, m := range req.Messages {
				if m.Role == core.RoleTool {
					return &core.ChatResponse{
						Message: core.ChatMessage{Role: core.RoleAssistant, Content: fmt.Sprintf("got tool response: %v", m.Content)},
					}, nil
				}
			}
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "no tool msg"}}, nil
		},
	}

	engine := &Engine{
		MaxIterations: 2,
		ToolRegistry:  map[string]ToolExecutor{"dummy_tool": &dummyTool{}},
		Security:      controller,
		Provider:      provider,
	}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "run tool"}}, RunContext{
		ActorPlatform: "test-platform",
		ActorUserID:   "actor-004",
	})
	want := "got tool response: FrostAgent安全控制：安全审查服务暂时不可用，外部工具结果已隔离。"
	if result.Content != want {
		t.Fatalf("expected tool result failure message %q, got %q", want, result.Content)
	}

	snapshot := logs.Snapshot()
	var watchdogErrors []logs.LogEntry
	var engineWarns []logs.LogEntry
	for _, entry := range snapshot {
		if entry.Category == logs.SYSTEM && entry.Level == logs.ERROR && strings.Contains(entry.Content, "安全审查分类器异常 (Fail-Closed):") {
			watchdogErrors = append(watchdogErrors, entry)
		}
		if entry.Category == logs.SYSTEM && entry.Level == logs.WARN && strings.Contains(entry.Content, "工具结果因安全审查服务异常被隔离:") {
			engineWarns = append(engineWarns, entry)
		}
	}
	if len(watchdogErrors) != 1 {
		t.Fatalf("expected exactly 1 Watchdog ERROR log, got %d, snapshot=%+v", len(watchdogErrors), snapshot)
	}
	if len(engineWarns) != 1 {
		t.Fatalf("expected exactly 1 Engine WARN log, got %d, snapshot=%+v", len(engineWarns), snapshot)
	}
	watchdogEvalID := extractEvalID(watchdogErrors[0].Content)
	engineEvalID := extractEvalID(engineWarns[0].Content)
	if watchdogEvalID == "" || watchdogEvalID != engineEvalID {
		t.Fatalf("expected shared eval_id between Watchdog and Engine, got watchdog=%q, engine=%q", watchdogEvalID, engineEvalID)
	}
	if !strings.Contains(engineWarns[0].Content, "tool=dummy_tool") {
		t.Fatalf("expected tool context in engine WARN log: %q", engineWarns[0].Content)
	}
	if strings.Contains(engineWarns[0].Content, "error_type=") || strings.Contains(engineWarns[0].Content, "reason=") {
		t.Fatalf("engine WARN log must not duplicate error_type or reason: %q", engineWarns[0].Content)
	}
	for _, entry := range snapshot {
		if entry.Level == logs.ERROR && !strings.Contains(entry.Content, "安全审查分类器异常 (Fail-Closed):") {
			t.Fatalf("unexpected ERROR log found outside Watchdog: %+v", entry)
		}
	}
}

type mockMultiStepProvider struct {
	chatFunc func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error)
}

func (m *mockMultiStepProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	return m.chatFunc(ctx, req)
}

func TestEngineModelOutputAccessStoreFailure(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	controller := security.NewController(t.TempDir())
	provider := &mockMultiStepProvider{
		chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			// Corrupt the access store file before the model output checkpoint
			if err := os.WriteFile(controller.Access.Path(), []byte("{broken json content"), 0600); err != nil {
				return nil, err
			}
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "这是正常的模型输出",
				},
			}, nil
		},
	}

	engine := &Engine{
		MaxIterations: 1,
		ToolRegistry:  map[string]ToolExecutor{},
		Security:      controller,
		Provider:      provider,
	}

	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "你好"}}, RunContext{
		ActorPlatform: "test-platform",
		ActorUserID:   "actor-access-failure",
	})

	want := "FrostAgent安全控制：安全审查服务暂时不可用，模型输出已拦截。"
	if result.Content != want {
		t.Fatalf("expected service failure message %q, got %q", want, result.Content)
	}

	snapshot := logs.Snapshot()
	var foundLog bool
	var evalID string
	for _, entry := range snapshot {
		if entry.Category == logs.SYSTEM && entry.Level == logs.WARN && strings.Contains(entry.Content, "模型输出因安全审查服务异常被拦截") {
			foundLog = true
			parts := strings.Split(entry.Content, "eval_id=")
			if len(parts) > 1 {
				evalID = strings.TrimSpace(parts[1])
			}
		}
	}
	if !foundLog {
		t.Fatalf("expected Warn log for model output security service exception, snapshot=%+v", snapshot)
	}
	if evalID == "" || !strings.HasPrefix(evalID, "eval_model_output_") {
		t.Fatalf("expected valid eval_model_output_* correlation ID, got %q", evalID)
	}
}

func TestEngineModelOutputAccessStoreLocked(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	controller := security.NewController(t.TempDir())
	principal, err := security.NewPrincipal("test-platform", "actor-locked-during-chat")
	if err != nil {
		t.Fatal(err)
	}

	provider := &mockMultiStepProvider{
		chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			if err := controller.Access.Lock(principal, "policy violation ban"); err != nil {
				return nil, err
			}
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "这是正常的模型输出",
				},
			}, nil
		},
	}

	engine := &Engine{
		MaxIterations: 1,
		ToolRegistry:  map[string]ToolExecutor{},
		Security:      controller,
		Provider:      provider,
	}

	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "你好"}}, RunContext{
		ActorPlatform: "test-platform",
		ActorUserID:   "actor-locked-during-chat",
	})

	want := "FrostAgent安全控制：模型输出已拦截。"
	if result.Content != want {
		t.Fatalf("expected policy block message %q, got %q", want, result.Content)
	}

	snapshot := logs.Snapshot()
	var foundLog bool
	var evalID string
	for _, entry := range snapshot {
		if entry.Category == logs.SYSTEM && entry.Level == logs.WARN && strings.Contains(entry.Content, "模型输出被安全控制拦截") {
			foundLog = true
			parts := strings.Split(entry.Content, "eval_id=")
			if len(parts) > 1 {
				evalID = strings.TrimSpace(parts[1])
			}
		}
	}
	if !foundLog {
		t.Fatalf("expected Warn log for model output policy block, snapshot=%+v", snapshot)
	}
	if evalID == "" || !strings.HasPrefix(evalID, "eval_model_output_") {
		t.Fatalf("expected valid eval_model_output_* correlation ID, got %q", evalID)
	}
}

func TestEngineSecurityEvaluateAccessStoreFailureStages(t *testing.T) {
	stages := []struct {
		stage      security.WatchdogStage
		source     security.WatchdogSource
		wantPrefix string
	}{
		{security.StageModelOutput, security.SourceModelOutput, "eval_model_output_"},
		{security.StageToolArgument, security.SourceToolArgument, "eval_tool_argument_"},
		{security.StageToolResult, security.SourceToolResult, "eval_tool_result_"},
	}

	for _, tc := range stages {
		t.Run(string(tc.stage), func(t *testing.T) {
			controller := security.NewController(t.TempDir())
			engine := &Engine{Security: controller}
			runCtx := RunContext{ActorPlatform: "test-platform", ActorUserID: "actor-stages"}

			// 1. Storage failure: corrupt access store
			if err := os.WriteFile(controller.Access.Path(), []byte("{malformed json"), 0600); err != nil {
				t.Fatal(err)
			}
			blocked, decision := engine.securityEvaluate(runCtx, tc.stage, tc.source, "payload", "tool_x")
			if !blocked {
				t.Errorf("[%s] expected blocked=true on storage failure", tc.stage)
			}
			if !decision.IsFailure {
				t.Errorf("[%s] expected IsFailure=true on access storage failure", tc.stage)
			}
			if !strings.HasPrefix(decision.EvaluationID, tc.wantPrefix) {
				t.Errorf("[%s] expected evaluation ID prefix %q, got %q", tc.stage, tc.wantPrefix, decision.EvaluationID)
			}
			if !strings.Contains(decision.Reason, "access control unavailable") {
				t.Errorf("[%s] expected reason containing 'access control unavailable', got %q", tc.stage, decision.Reason)
			}

			// 2. Policy lock: principal is locked
			p, err := runPrincipal(runCtx)
			if err != nil {
				t.Fatal(err)
			}
			// Re-create valid access store with lock
			controllerLocked := security.NewController(t.TempDir())
			if err := controllerLocked.Access.Lock(p, "test lockout"); err != nil {
				t.Fatal(err)
			}
			engineLocked := &Engine{Security: controllerLocked}
			blockedLocked, decisionLocked := engineLocked.securityEvaluate(runCtx, tc.stage, tc.source, "payload", "tool_x")
			if !blockedLocked {
				t.Errorf("[%s] expected blocked=true on locked principal", tc.stage)
			}
			if decisionLocked.IsFailure {
				t.Errorf("[%s] expected IsFailure=false on locked principal (policy block)", tc.stage)
			}
			if !strings.HasPrefix(decisionLocked.EvaluationID, tc.wantPrefix) {
				t.Errorf("[%s] expected evaluation ID prefix %q, got %q", tc.stage, tc.wantPrefix, decisionLocked.EvaluationID)
			}
			if decisionLocked.Reason != "access denied / locked" {
				t.Errorf("[%s] expected reason 'access denied / locked', got %q", tc.stage, decisionLocked.Reason)
			}
		})
	}
}


func extractEvalID(content string) string {
	idx := strings.Index(content, "eval_id=")
	if idx == -1 {
		return ""
	}
	val := content[idx+len("eval_id="):]
	if end := strings.IndexAny(val, " \t\r\n"); end != -1 {
		val = val[:end]
	}
	return strings.TrimSpace(val)
}
