package llm

import (
	"context"
	"sync/atomic"
	"testing"

	"FrostAgent/internal/core"
	"FrostAgent/internal/runtimescope"
)

func TestRunCanceledOptionalTurn(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	liveTurn := &SessionTurn{ctx: context.Background()}
	interruptedTurn := &SessionTurn{ctx: canceled}

	for _, tc := range []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{"no run context", context.Background(), false},
		{"nil turn", WithRunContext(context.Background(), RunContext{}), false},
		{"zero-value turn", WithRunContext(context.Background(), RunContext{Turn: &SessionTurn{}}), false},
		{"live turn", WithRunContext(context.Background(), RunContext{Turn: liveTurn}), false},
		{"canceled without run context", canceled, true},
		{"canceled with nil turn", WithRunContext(canceled, RunContext{}), true},
		{"canceled with live turn", WithRunContext(canceled, RunContext{Turn: liveTurn}), true},
		{"interrupted turn with live context", WithRunContext(context.Background(), RunContext{Turn: interruptedTurn}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runCanceled(tc.ctx); got != tc.want {
				t.Fatalf("runCanceled() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestInterruptibleWorkersWithoutTurn(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"no run context", context.Background()},
		{"nil turn", WithRunContext(context.Background(), RunContext{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var toolCalls atomic.Int32
			tool := injectionTool{"test-tool", func() (string, error) {
				toolCalls.Add(1)
				return "tool-result", nil
			}}
			call := ToolCall{ID: "test-call", Type: "function", Function: ToolCallFunction{Name: tool.Name(), Arguments: "{}"}}
			result, err := executeInterruptibleTool(tc.ctx, tool, call)
			if err != nil || result.Content != "tool-result" || toolCalls.Load() != 1 {
				t.Fatalf("tool result = %+v, err = %v, calls = %d", result, err, toolCalls.Load())
			}

			var modelCalls atomic.Int32
			want := finalInjection("model-result")
			engine := &Engine{Provider: injectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
				modelCalls.Add(1)
				return want, nil
			})}
			response, modelErr, commitErr := engine.chatAndSettle(tc.ctx, core.ChatRequest{}, "", nil)
			if response != want || modelErr != nil || commitErr != nil || modelCalls.Load() != 1 {
				t.Fatalf("model response = %+v, model err = %v, commit err = %v, calls = %d", response, modelErr, commitErr, modelCalls.Load())
			}
		})
	}
}

func TestRunMessagesWithContextWithoutTurn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sessionID string
	}{
		{"empty run context", ""},
		{"legacy session", "private:synthetic-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := runtimescope.New(nil, nil, nil)
			t.Cleanup(func() { scope.Cancel(); scope.Wait() })
			engine := &Engine{Scope: scope, MaxIterations: 3}
			run := RunContext{}
			if tc.sessionID != "" {
				engine.SessionManager = NewSessionManager(scope)
				run.SessionID = engine.SessionManager.GetOrCreate(tc.sessionID).ID()
			}

			var toolCalls, modelCalls atomic.Int32
			tool := injectionTool{"test-tool", func() (string, error) {
				toolCalls.Add(1)
				return "tool-result", nil
			}}
			engine.ToolRegistry = map[string]ToolExecutor{tool.Name(): tool}
			engine.Provider = injectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				got, ok := RunContextFromContext(ctx)
				if !ok || got.Turn != nil {
					t.Errorf("provider context = %+v, present = %t; want nil Turn", got, ok)
				}
				if run.SessionID != "" && (got.SessionID != run.SessionID || got.Epoch == 0 || ctx.Done() == nil) {
					t.Errorf("legacy session did not bind its execution context: %+v", got)
				}
				if modelCalls.Add(1) == 1 {
					return injectionCalls(tool.Name()), nil
				}
				last := req.Messages[len(req.Messages)-1]
				if last.Role != core.RoleTool || last.ToolCallID != tool.Name() || last.Content != "tool-result" {
					t.Errorf("tool result was not retained in the next model request: %+v", last)
				}
				return finalInjection("done"), nil
			})

			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "test"}}, run)
			if result.Error != nil || result.LLMFailed || result.Silent || result.Content != "done" {
				t.Fatalf("run without Turn = %+v", result)
			}
			if modelCalls.Load() != 2 || toolCalls.Load() != 1 {
				t.Fatalf("model calls = %d, tool calls = %d; want 2 and 1", modelCalls.Load(), toolCalls.Load())
			}
		})
	}
}
