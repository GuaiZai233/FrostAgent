package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"FrostAgent/internal/core"
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
}

func TestEngineToolResultClassifierFailure(t *testing.T) {
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
}

type mockMultiStepProvider struct {
	chatFunc func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error)
}

func (m *mockMultiStepProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	return m.chatFunc(ctx, req)
}
