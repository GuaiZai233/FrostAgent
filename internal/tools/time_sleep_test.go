package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/timesleep"
)

func TestTimeSleepValidation(t *testing.T) {
	tool := TimeSleepTool(nil)
	for _, args := range []string{`{}`, `null`, `{"seconds":null}`, `{"seconds":0}`, `{"seconds":-1}`, `{"seconds":1e100}`, `{"seconds":1e-100}`, `{"seconds":"1"}`, `{`} {
		if _, err := tool.Execute(args); err == nil {
			t.Errorf("accepted %s", args)
		}
	}
	if got, err := timesleep.Timeout(""); err != nil || got != time.Minute {
		t.Fatalf("default timeout: %v, %v", got, err)
	}
	for _, raw := range []string{"0", "-1", "NaN", "+Inf", "bad", "1e100"} {
		if _, err := timesleep.Timeout(raw); err == nil {
			t.Errorf("accepted timeout %q", raw)
		}
	}
}

func TestTimeSleepSuccessAndLiveTimeout(t *testing.T) {
	raw := "1"
	tool := TimeSleepTool(func(string) string { return raw })
	start := time.Now()
	if result, err := tool.Execute(`{"seconds":0.01}`); err != nil || result == "" {
		t.Fatalf("sleep: %q, %v", result, err)
	}
	if time.Since(start) < 10*time.Millisecond {
		t.Fatal("returned before requested duration")
	}
	raw = "0.01"
	for _, seconds := range []string{"0.01", "60"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := tool.ExecuteContext(ctx, `{"seconds":`+seconds+`}`)
		cancel()
		if !errors.Is(err, timesleep.ErrTimeout) || err.Error() != "Timeout" {
			t.Fatalf("timeout: %v", err)
		}
	}
}

func TestTimeSleepCancellation(t *testing.T) {
	for _, before := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if before {
			cancel()
		} else {
			time.AfterFunc(10*time.Millisecond, cancel)
		}
		_, err := TimeSleepTool(nil).ExecuteContext(ctx, `{"seconds":60}`)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	}
}

type sleepProvider func(context.Context, core.ChatRequest) (*core.ChatResponse, error)

func (f sleepProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	return f(ctx, req)
}

func TestTimeSleepContinuesSameTurn(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint(timeout), func(t *testing.T) {
			e := &llm.Engine{SessionManager: llm.NewSessionManager(), MaxIterations: 2}
			session := e.SessionManager.GetOrCreate("private:synthetic-sleep")
			turn := session.ReserveSequenceTurn("sender:synthetic", true, false)
			defer turn.Done()
			turn.Wait()
			tool := TimeSleepTool(func(string) string {
				if timeout {
					return "0.001"
				}
				return "60"
			})
			e.ToolRegistry = map[string]llm.ToolExecutor{tool.Name(): tool}
			calls := 0
			e.Provider = sleepProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				calls++
				if calls == 1 {
					return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "sleep-call", Type: "function", Function: core.ToolCallFunction{Name: timesleep.ToolName, Arguments: `{"seconds":0.002}`}}}}}, nil
				}
				last := req.Messages[len(req.Messages)-1]
				want := "已挂起"
				if timeout {
					want = "Timeout"
				}
				if last.ToolCallID != "sleep-call" || !strings.Contains(fmt.Sprint(last.Content), want) {
					t.Errorf("sleep result not returned to same run: %+v", last)
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "continued"}}, nil
			})
			messages, _, _ := session.PrepareTurnMessages(turn, "synthetic-input", "synthetic-input", nil)
			result := e.RunMessagesWithContext(messages, llm.RunContext{SessionID: session.ID(), Epoch: turn.Epoch(), Turn: turn, Mock: true})
			if result.Error != nil || result.Content != "continued" || calls != 2 || e.TotalMessagesProcessed.Load() != 2 || turn.Interrupted() {
				t.Fatalf("sleep did not continue in same turn: %+v, calls=%d", result, calls)
			}
		})
	}
}

func TestTimeSleepConsumesIterationBudget(t *testing.T) {
	tool := TimeSleepTool(nil)
	e := &llm.Engine{MaxIterations: 1, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool}}
	calls := 0
	e.Provider = sleepProvider(func(_ context.Context, _ core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "sleep-budget", Type: "function", Function: core.ToolCallFunction{Name: timesleep.ToolName, Arguments: `{"seconds":0.001}`}}}}}, nil
	})
	result := e.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "synthetic-input"}}, llm.RunContext{Mock: true})
	if !strings.Contains(result.Content, llm.ErrMaxIterationsReached.Error()) || calls != 1 || e.TotalMessagesProcessed.Load() != 1 {
		t.Fatalf("sleep reset iteration budget: %+v, calls=%d", result, calls)
	}
}

func TestTimeSleepInterruptedByNewInputAndReset(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint(reset), func(t *testing.T) {
			e := &llm.Engine{SessionManager: llm.NewSessionManager(), MaxIterations: 2}
			s := e.SessionManager.GetOrCreate("private:synthetic-cancel")
			started := make(chan struct{})
			tool := TimeSleepTool(func(string) string { close(started); return "60" })
			e.ToolRegistry = map[string]llm.ToolExecutor{tool.Name(): tool}
			e.Provider = sleepProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "interrupted-sleep", Type: "function", Function: core.ToolCallFunction{Name: timesleep.ToolName, Arguments: `{"seconds":60}`}}}}}, nil
			})
			turn := s.ReserveSequenceTurn("sender:synthetic", true, false)
			turn.Wait()
			messages, _, _ := s.PrepareTurnMessages(turn, "old-input", "old-input", nil)
			finished := make(chan llm.AgentRunResult, 1)
			go func() {
				finished <- e.RunMessagesWithContext(messages, llm.RunContext{SessionID: s.ID(), Turn: turn, Epoch: turn.Epoch(), Mock: true, RefreshPrompt: func(context.Context) (llm.PromptRefresh, error) {
					t.Error("canceled sleep refreshed old prompt")
					return llm.PromptRefresh{}, nil
				}})
				turn.Done()
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("sleep did not start")
			}
			if reset {
				if err := s.ResetSession(nil); err != nil {
					t.Fatal(err)
				}
			} else {
				next := s.ReserveSequenceTurn("sender:synthetic", true, false)
				defer next.Done()
			}
			select {
			case result := <-finished:
				if !result.Silent || result.Content != "" || !errors.Is(result.Error, context.Canceled) || e.TotalMessagesProcessed.Load() != 1 {
					t.Fatalf("stale sleep resumed: %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("sleep ignored interruption")
			}
		})
	}
}
