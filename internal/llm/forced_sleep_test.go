package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/timesleep"
)

// recordingSleepTool records the arguments it receives so tests can check the
// backend-issued call without actually sleeping.
type recordingSleepTool struct {
	args *[]string
	err  error
}

func (t recordingSleepTool) Name() string               { return timesleep.ToolName }
func (t recordingSleepTool) Description() string        { return "synthetic sleep" }
func (t recordingSleepTool) Parameters() map[string]any { return nil }
func (t recordingSleepTool) Execute(args string) (string, error) {
	*t.args = append(*t.args, args)
	if t.err != nil {
		return "", t.err
	}
	return "已挂起 10 秒", nil
}

// sleepExchange reports whether the request already contains a backend-issued
// time_sleep call followed by its tool result, and returns the result content.
func sleepExchange(messages []core.ChatMessage) (bool, string) {
	var callID string
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			if call.Function.Name == timesleep.ToolName && call.Function.Arguments == `{"seconds":10}` {
				callID = call.ID
			}
		}
	}
	if callID == "" {
		return false, ""
	}
	for _, msg := range messages {
		if msg.Role == core.RoleTool && msg.ToolCallID == callID {
			return true, fmt.Sprint(msg.Content)
		}
	}
	return false, ""
}

func TestForcedMentionSleepRunsBeforeFirstModelCall(t *testing.T) {
	var executed []string
	calls := 0
	e := &Engine{MaxIterations: 3, ToolRegistry: map[string]ToolExecutor{timesleep.ToolName: recordingSleepTool{args: &executed}}}
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		ok, content := sleepExchange(req.Messages)
		if !ok {
			t.Errorf("first model request lacks the forced sleep exchange: %v", req.Messages)
		}
		if !strings.Contains(content, "已挂起") {
			t.Errorf("unexpected sleep result recorded for the model: %q", content)
		}
		return finalInjection("done"), nil
	})

	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "@bot"}}, RunContext{Mock: true, ReplyToBotMentionOnly: true})

	if result.Content != "done" || result.Error != nil || calls != 1 {
		t.Fatalf("unexpected result %+v after %d model calls", result, calls)
	}
	if len(executed) != 1 || executed[0] != `{"seconds":10}` {
		t.Fatalf("forced sleep executed %v, want exactly one {\"seconds\":10}", executed)
	}
}

func TestForcedMentionSleepRequiresTrustedFlag(t *testing.T) {
	var executed []string
	e := &Engine{MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{timesleep.ToolName: recordingSleepTool{args: &executed}}}
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		if ok, _ := sleepExchange(req.Messages); ok {
			t.Errorf("sleep exchange injected without the trusted flag")
		}
		return finalInjection("done"), nil
	})

	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "@bot"}}, RunContext{Mock: true})

	if result.Content != "done" || len(executed) != 0 {
		t.Fatalf("flag off changed execution: %+v executed=%v", result, executed)
	}
}

func TestForcedMentionSleepSkippedWhenToolUnavailable(t *testing.T) {
	calls := 0
	e := &Engine{MaxIterations: 2}
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		if ok, _ := sleepExchange(req.Messages); ok {
			t.Errorf("sleep exchange recorded although time_sleep is not registered")
		}
		return finalInjection("done"), nil
	})

	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "@bot"}}, RunContext{Mock: true, ReplyToBotMentionOnly: true})

	if result.Content != "done" || result.Error != nil || calls != 1 {
		t.Fatalf("disabled tool should not block the turn: %+v calls=%d", result, calls)
	}
}

func TestForcedMentionSleepTimeoutStillContinuesTurn(t *testing.T) {
	var executed []string
	e := &Engine{MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{timesleep.ToolName: recordingSleepTool{args: &executed, err: timesleep.ErrTimeout}}}
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		ok, content := sleepExchange(req.Messages)
		if !ok || !strings.Contains(content, "Timeout") {
			t.Errorf("timeout was not reported to the model: ok=%v content=%q", ok, content)
		}
		return finalInjection("done"), nil
	})

	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "@bot"}}, RunContext{Mock: true, ReplyToBotMentionOnly: true})

	if result.Content != "done" || result.Error != nil || len(executed) != 1 {
		t.Fatalf("timeout should be reported and the turn should finish: %+v executed=%v", result, executed)
	}
}
