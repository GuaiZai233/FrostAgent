package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/security"
	"context"
	"sync/atomic"
	"testing"
)

type mockBanTool struct {
	executed *atomic.Int32
	ctrl     *security.Controller
}

func (b *mockBanTool) Name() string                 { return security.BanUserToolName }
func (b *mockBanTool) Description() string          { return "ban user" }
func (b *mockBanTool) Parameters() map[string]any   { return map[string]any{} }
func (b *mockBanTool) Execute(args string) (string, error) {
	return b.ExecuteContext(context.Background(), args)
}

func (b *mockBanTool) ExecuteContext(ctx context.Context, args string) (string, error) {
	if b.executed != nil {
		b.executed.Add(1)
	}
	runCtx, _ := RunContextFromContext(ctx)
	principal, _ := security.NewPrincipal(runCtx.ActorPlatform, runCtx.ActorUserID)
	if b.ctrl != nil {
		_ = b.ctrl.Lock(principal, "test autonomous ban")
	}
	return "", security.ErrBanUserSuccess
}

type siblingTool struct {
	executed *atomic.Int32
}

func (s *siblingTool) Name() string                 { return "sibling_tool" }
func (s *siblingTool) Description() string          { return "sibling tool" }
func (s *siblingTool) Parameters() map[string]any   { return map[string]any{} }
func (s *siblingTool) Execute(args string) (string, error) {
	s.executed.Add(1)
	return "sibling done", nil
}

func TestBanUserToolLoopShortCircuitAndCancelBatch(t *testing.T) {
	for _, mode := range []security.ControlMode{security.ControlModeSimple, security.ControlModeAggressive} {
		t.Run(string(mode), func(t *testing.T) {
			tmpDir := t.TempDir()
			ctrl := security.NewController(tmpDir)
			ctrl.SetMode(mode)

			classifierCalls := atomic.Int32{}
			ctrl.Watchdog.SetClassifier(&mockGateClassifier{
				fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
					classifierCalls.Add(1)
					return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
				},
			})

			banExecuted := atomic.Int32{}
			siblingExecuted := atomic.Int32{}

			banTool := &mockBanTool{executed: &banExecuted, ctrl: ctrl}
			sibTool := &siblingTool{executed: &siblingExecuted}

			modelCalls := atomic.Int32{}
			provider := &mockMultiStepProvider{
				chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
					modelCalls.Add(1)
					// Iteration 1: Model emits two tool calls: ban_user and sibling_tool
					return &core.ChatResponse{
						Message: core.ChatMessage{
							Role: core.RoleAssistant,
							ToolCalls: []core.ToolCall{
								{
									ID:       "call_ban",
									Type:     "function",
									Function: core.ToolCallFunction{Name: security.BanUserToolName, Arguments: `{"reason":"malicious"}`},
								},
								{
									ID:       "call_sib",
									Type:     "function",
									Function: core.ToolCallFunction{Name: "sibling_tool", Arguments: `{}`},
								},
							},
						},
					}, nil
				},
			}

			engine := &Engine{
				MaxIterations: 5,
				ToolRegistry: map[string]ToolExecutor{
					security.BanUserToolName: banTool,
					"sibling_tool":           sibTool,
				},
				Security: ctrl,
				Provider: provider,
			}

			runCtx := RunContext{
				ActorPlatform: "qq",
				ActorUserID:   "user-to-ban-123",
				InstanceID:    "inst-test",
				SessionID:     "sess-test",
			}

			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "do bad thing"}}, runCtx)

			// 1. Result content must match exact user-facing notice
			if result.Content != security.RejectGatewayMsg {
				t.Fatalf("expected result %q, got %q", security.RejectGatewayMsg, result.Content)
			}

			// 2. ban_user executed exactly once
			if banExecuted.Load() != 1 {
				t.Fatalf("expected ban_user executed once, got %d", banExecuted.Load())
			}

			// 3. sibling_tool was cancelled and never executed
			if siblingExecuted.Load() != 0 {
				t.Fatalf("expected sibling_tool to be cancelled, got %d executions", siblingExecuted.Load())
			}

			// 4. Model was only called once (in iter 1) - no second call to explain
			if modelCalls.Load() != 1 {
				t.Fatalf("expected exactly 1 model call, got %d", modelCalls.Load())
			}

			// 5. User is locked in AccessStore
			p, _ := security.NewPrincipal("qq", "user-to-ban-123")
			if !ctrl.IsLocked(p) {
				t.Fatal("expected user to be locked in AccessStore")
			}

			// 6. BanUser tool arguments and results must have bypassed classifier
			// (Classifier calls for ban_user must be 0)
			if classifierCalls.Load() != 0 {
				t.Fatalf("expected 0 classifier calls for ban_user bypass, got %d", classifierCalls.Load())
			}
		})
	}
}

func TestSecurityGateModesClassifierBypass(t *testing.T) {
	for _, mode := range []security.ControlMode{security.ControlModeOff, security.ControlModeSimple} {
		t.Run(string(mode), func(t *testing.T) {
			tmpDir := t.TempDir()
			ctrl := security.NewController(tmpDir)
			ctrl.SetMode(mode)

			classifierCalls := atomic.Int32{}
			ctrl.Watchdog.SetClassifier(&mockGateClassifier{
				fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
					classifierCalls.Add(1)
					return security.ClassificationResult{
						Category:  security.RiskCategoryMaliciousExecution,
						RiskLevel: security.RiskLevelCritical,
						Reason:    "should not be called",
					}, nil
				},
			})

			provider := &mockMultiStepProvider{
				chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
					return &core.ChatResponse{
						Message: core.ChatMessage{
							Role:    core.RoleAssistant,
							Content: "Normal helpful response",
						},
					}, nil
				},
			}

			engine := &Engine{
				MaxIterations: 1,
				Security:      ctrl,
				Provider:      provider,
			}

			runCtx := RunContext{
				ActorPlatform: "qq",
				ActorUserID:   "benign-user-1",
			}

			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, runCtx)
			if result.Content != "Normal helpful response" {
				t.Fatalf("expected normal response in %s mode, got %q", mode, result.Content)
			}
			if classifierCalls.Load() != 0 {
				t.Fatalf("expected 0 classifier calls in %s mode, got %d", mode, classifierCalls.Load())
			}
		})
	}
}
