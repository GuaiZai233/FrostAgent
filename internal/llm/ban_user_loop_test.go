package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/security"
	"context"
	"errors"
	"strings"
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

type mockSilentTool struct{}

func (m *mockSilentTool) Name() string               { return StaySilentToolName }
func (m *mockSilentTool) Description() string        { return "silent" }
func (m *mockSilentTool) Parameters() map[string]any { return map[string]any{} }
func (m *mockSilentTool) Execute(args string) (string, error) {
	return "ok", nil
}

func TestProactiveTurnDisallowsSideEffectingToolsAndBanUser(t *testing.T) {
	tmpDir := t.TempDir()
	ctrl := security.NewController(tmpDir)
	ctrl.SetMode(security.ControlModeAggressive)

	banExecuted := atomic.Int32{}
	siblingExecuted := atomic.Int32{}

	banTool := &mockBanTool{executed: &banExecuted, ctrl: ctrl}
	sibTool := &siblingTool{executed: &siblingExecuted}
	silentTool := &mockSilentTool{}

	var receivedTools []core.Tool
	provider := &mockMultiStepProvider{
		chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			receivedTools = req.Tools
			// Model attempts to call ban_user on proactive turn
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:       "call_ban",
							Type:     "function",
							Function: core.ToolCallFunction{Name: security.BanUserToolName, Arguments: `{"reason":"proactive ban attempt"}`},
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
			StaySilentToolName:       silentTool,
		},
		Security: ctrl,
		Provider: provider,
	}

	runCtx := RunContext{
		ActorPlatform: "qq",
		ActorUserID:   "bystander-user-456",
		InstanceID:    "inst-test",
		SessionID:     "sess-test",
		Proactive:     true,
	}

	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "unaddressed group chat"}}, runCtx)

	// 1. Proactive tools offered to LLM must NOT contain ban_user or sibling_tool
	for _, tool := range receivedTools {
		if tool.Name == security.BanUserToolName || tool.Name == "sibling_tool" {
			t.Fatalf("proactive turn should not offer tool %s to model schema", tool.Name)
		}
	}

	// 2. ban_user execution must be blocked server-side: executed == 0
	if banExecuted.Load() != 0 {
		t.Fatalf("expected ban_user not executed in proactive turn, got %d", banExecuted.Load())
	}

	// 3. Bystander must NOT be locked in AccessStore
	p, _ := security.NewPrincipal("qq", "bystander-user-456")
	if ctrl.IsLocked(p) {
		t.Fatal("bystander must not be locked during proactive turn ban attempt")
	}

	// 4. Result must terminate silently without Banned flag
	if !result.Silent {
		t.Fatal("expected result.Silent to be true on proactive ban attempt")
	}
	if result.Banned {
		t.Fatal("expected result.Banned to be false on proactive ban attempt")
	}
}

func TestProactiveTurnOutputBlockedWatchdog(t *testing.T) {
	tmpDir := t.TempDir()
	ctrl := security.NewController(tmpDir)
	ctrl.SetMode(security.ControlModeAggressive)

	ctrl.Watchdog.SetClassifier(&mockGateClassifier{
		fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
			if input.Stage == security.StageModelOutput {
				return security.ClassificationResult{
					Category:  security.RiskCategoryViolenceTerrorism,
					RiskLevel: security.RiskLevelCritical,
					Reason:    "blocked output",
				}, nil
			}
			return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
		},
	})

	provider := &mockMultiStepProvider{
		chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "bad output text",
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
		ActorUserID:   "user-1",
		Proactive:     true,
	}

	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hi"}}, runCtx)
	if !result.OutputBlocked {
		t.Fatal("expected OutputBlocked to be true when StageModelOutput is blocked by watchdog")
	}

	// Also verify classifier outage triggers OutputBlocked == true
	ctrl.Watchdog.SetClassifier(&mockGateClassifier{
		fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
			return security.ClassificationResult{}, errors.New("classifier service down")
		},
	})
	resultOutage := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hi"}}, runCtx)
	if !resultOutage.OutputBlocked {
		t.Fatal("expected OutputBlocked to be true when classifier has an outage at StageModelOutput")
	}
}

type mockGenericTool struct {
	name     string
	executed *atomic.Int32
}

func (m *mockGenericTool) Name() string                 { return m.name }
func (m *mockGenericTool) Description() string          { return "generic tool" }
func (m *mockGenericTool) Parameters() map[string]any   { return map[string]any{} }
func (m *mockGenericTool) Execute(args string) (string, error) {
	if m.executed != nil {
		m.executed.Add(1)
	}
	return "ok", nil
}

func TestProactiveTurnDisallowsSideEffectingToolsEarlyExit(t *testing.T) {
	tmpDir := t.TempDir()
	ctrl := security.NewController(tmpDir)

	cmdExecuted := atomic.Int32{}
	cmdTool := &mockGenericTool{name: "execute_command", executed: &cmdExecuted}

	modelCalls := atomic.Int32{}
	provider := &mockMultiStepProvider{
		chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			modelCalls.Add(1)
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:       "call_cmd_1",
							Type:     "function",
							Function: core.ToolCallFunction{Name: "execute_command", Arguments: `{"cmd":"ls"}`},
						},
					},
				},
			}, nil
		},
	}

	engine := &Engine{
		MaxIterations: 5,
		ToolRegistry: map[string]ToolExecutor{
			"execute_command": cmdTool,
		},
		Security: ctrl,
		Provider: provider,
	}

	runCtx := RunContext{
		ActorPlatform: "qq",
		ActorUserID:   "bystander-user-789",
		Proactive:     true,
	}

	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, runCtx)

	// 1. Side-effecting tool must not be executed
	if cmdExecuted.Load() != 0 {
		t.Fatalf("expected execute_command not executed in proactive turn, got %d", cmdExecuted.Load())
	}

	// 2. Loop must terminate immediately without calling model again
	if modelCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 model call due to early termination, got %d", modelCalls.Load())
	}

	// 3. Result must terminate silently with empty content
	if !result.Silent {
		t.Fatal("expected result.Silent to be true on proactive side-effecting tool call")
	}
	if result.Content != "" {
		t.Fatalf("expected result.Content to be empty, got %q", result.Content)
	}
}

func TestProactiveTurnMaxIterationsReachedIsSilent(t *testing.T) {
	ctrl := security.NewController(t.TempDir())

	memExecuted := atomic.Int32{}
	memTool := &mockGenericTool{name: "memory", executed: &memExecuted}

	provider := &mockMultiStepProvider{
		chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:       "call_mem_1",
							Type:     "function",
							Function: core.ToolCallFunction{Name: "memory", Arguments: `{"action":"list"}`},
						},
					},
				},
			}, nil
		},
	}

	engine := &Engine{
		MaxIterations: 3,
		ToolRegistry: map[string]ToolExecutor{
			"memory": memTool,
		},
		Security: ctrl,
		Provider: provider,
	}

	t.Run("proactive turn exhausting max iterations exits silently", func(t *testing.T) {
		runCtx := RunContext{
			ActorPlatform: "qq",
			ActorUserID:   "user-proactive",
			Proactive:     true,
		}

		result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, runCtx)

		if !result.Silent {
			t.Fatal("expected result.Silent to be true for proactive turn reaching max iterations")
		}
		if result.Content != "" {
			t.Fatalf("expected result.Content to be empty, got %q", result.Content)
		}
		if !errors.Is(result.Error, ErrMaxIterationsReached) {
			t.Fatalf("expected ErrMaxIterationsReached, got %v", result.Error)
		}
	})

	t.Run("explicit wake turn exhausting max iterations returns error content", func(t *testing.T) {
		runCtx := RunContext{
			ActorPlatform: "qq",
			ActorUserID:   "user-explicit",
			Proactive:     false,
		}

		result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, runCtx)

		if result.Silent {
			t.Fatal("expected result.Silent to be false for explicit turn reaching max iterations")
		}
		expectedErrMsg := "FrostAgent错误：达到最大迭代次数，未能得出最终答案"
		if result.Content != expectedErrMsg {
			t.Fatalf("expected %q, got %q", expectedErrMsg, result.Content)
		}
	})
}

func TestProactiveTurnDisallowsActionsCatTools(t *testing.T) {
	listActionsExecuted := atomic.Int32{}
	getRunExecuted := atomic.Int32{}
	listActionsTool := &mockGenericTool{name: "actionscat_list_actions", executed: &listActionsExecuted}
	getRunTool := &mockGenericTool{name: "actionscat_get_run", executed: &getRunExecuted}

	t.Run("proactive turn filters schemas and denies execution of ActionsCat tools", func(t *testing.T) {
		var receivedTools []core.Tool
		modelCalls := atomic.Int32{}
		provider := &mockMultiStepProvider{
			chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				modelCalls.Add(1)
				receivedTools = req.Tools
				return &core.ChatResponse{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{
							{
								ID:       "call_act_1",
								Type:     "function",
								Function: core.ToolCallFunction{Name: "actionscat_get_run", Arguments: `{"run_id":"run-123"}`},
							},
						},
					},
				}, nil
			},
		}

		engine := &Engine{
			MaxIterations: 5,
			ToolRegistry: map[string]ToolExecutor{
				"actionscat_list_actions": listActionsTool,
				"actionscat_get_run":      getRunTool,
				"memory":                  &mockGenericTool{name: "memory"},
				StaySilentToolName:        &mockSilentTool{},
			},
			Provider: provider,
		}

		runCtx := RunContext{
			ActorPlatform: "qq",
			ActorUserID:   "bystander-user-111",
			Proactive:     true,
		}

		result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "unaddressed msg"}}, runCtx)

		// 1. Tool schemas offered to LLM must not contain any actionscat tools
		for _, tool := range receivedTools {
			if strings.HasPrefix(tool.Name, "actionscat_") {
				t.Fatalf("proactive turn should not offer tool %s in model schema", tool.Name)
			}
		}

		// 2. ActionsCat tool must not be executed
		if getRunExecuted.Load() != 0 || listActionsExecuted.Load() != 0 {
			t.Fatalf("expected 0 execution of actionscat tools, got get_run=%d list_actions=%d", getRunExecuted.Load(), listActionsExecuted.Load())
		}

		// 3. Immediate silent exit on first iteration
		if modelCalls.Load() != 1 {
			t.Fatalf("expected exactly 1 model call on proactive denial fuse, got %d", modelCalls.Load())
		}
		if !result.Silent {
			t.Fatal("expected result.Silent to be true on proactive actionscat tool attempt")
		}
		if result.Content != "" {
			t.Fatalf("expected result.Content to be empty, got %q", result.Content)
		}
	})

	t.Run("explicit wake turn allows schema and execution of ActionsCat tools", func(t *testing.T) {
		listActionsExecuted.Store(0)
		var receivedTools []core.Tool
		provider := &mockMultiStepProvider{
			chatFunc: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				receivedTools = req.Tools
				if len(req.Messages) > 0 && req.Messages[len(req.Messages)-1].Role == core.RoleTool {
					return &core.ChatResponse{
						Message: core.ChatMessage{
							Role:    core.RoleAssistant,
							Content: "成功获取动作列表",
						},
					}, nil
				}
				return &core.ChatResponse{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{
							{
								ID:       "call_act_2",
								Type:     "function",
								Function: core.ToolCallFunction{Name: "actionscat_list_actions", Arguments: `{}`},
							},
						},
					},
				}, nil
			},
		}

		engine := &Engine{
			MaxIterations: 5,
			ToolRegistry: map[string]ToolExecutor{
				"actionscat_list_actions": listActionsTool,
				"actionscat_get_run":      getRunTool,
				"memory":                  &mockGenericTool{name: "memory"},
			},
			Provider: provider,
		}

		runCtx := RunContext{
			ActorPlatform: "qq",
			ActorUserID:   "admin-user",
			Proactive:     false,
		}

		result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "列出动作"}}, runCtx)

		// 1. Tool schemas offered to LLM must contain actionscat tools
		foundListActions := false
		foundGetRun := false
		for _, tool := range receivedTools {
			if tool.Name == "actionscat_list_actions" {
				foundListActions = true
			}
			if tool.Name == "actionscat_get_run" {
				foundGetRun = true
			}
		}
		if !foundListActions || !foundGetRun {
			t.Fatalf("explicit wake turn must offer actionscat tools, found list=%v, get=%v", foundListActions, foundGetRun)
		}

		// 2. Tool was executed
		if listActionsExecuted.Load() != 1 {
			t.Fatalf("expected actionscat_list_actions executed once, got %d", listActionsExecuted.Load())
		}

		// 3. Normal reply returned
		if result.Silent {
			t.Fatal("expected result.Silent to be false on explicit wake")
		}
		if result.Content != "成功获取动作列表" {
			t.Fatalf("expected '成功获取动作列表', got %q", result.Content)
		}
	})
}
