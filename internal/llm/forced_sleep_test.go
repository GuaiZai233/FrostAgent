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
// backend-issued wait without actually sleeping.
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

// hasSyntheticSleepCall reports whether any message sent to the provider carries a
// time_sleep tool call. The backend wait must never appear as a model-style call.
func hasSyntheticSleepCall(messages []core.ChatMessage) bool {
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			if call.Function.Name == timesleep.ToolName {
				return true
			}
		}
	}
	return false
}

func TestForcedMentionSleepRunsOnceAndRefreshesPrompt(t *testing.T) {
	var executed []string
	calls, refreshes := 0, 0
	e := &Engine{MaxIterations: 3, ToolRegistry: map[string]ToolExecutor{timesleep.ToolName: recordingSleepTool{args: &executed}}}
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		if hasSyntheticSleepCall(req.Messages) {
			t.Errorf("backend wait was sent to the provider as a tool call: %v", req.Messages)
		}
		if all := fmt.Sprint(req.Messages); !strings.Contains(all, "refreshed-background") {
			t.Errorf("prompt was not refreshed after the wait: %s", all)
		}
		return finalInjection("done"), nil
	})

	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "original-background"}}, RunContext{
		Mock: true, ReplyToBotMentionOnly: true,
		RefreshPrompt: func(context.Context) (PromptRefresh, error) {
			refreshes++
			return PromptRefresh{Content: "refreshed-background"}, nil
		},
	})

	if result.Content != "done" || result.Error != nil || calls != 1 {
		t.Fatalf("unexpected result %+v after %d model calls", result, calls)
	}
	if len(executed) != 1 || executed[0] != `{"seconds":10}` {
		t.Fatalf("backend wait executed %v, want exactly one {\"seconds\":10}", executed)
	}
	if refreshes != 1 {
		t.Fatalf("prompt refreshes = %d, want 1", refreshes)
	}
}

func TestForcedMentionSleepRequiresTrustedFlag(t *testing.T) {
	var executed []string
	e := &Engine{MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{timesleep.ToolName: recordingSleepTool{args: &executed}}}
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		if hasSyntheticSleepCall(req.Messages) {
			t.Errorf("sleep recorded without the trusted flag")
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
		return finalInjection("done"), nil
	})

	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "@bot"}}, RunContext{Mock: true, ReplyToBotMentionOnly: true})

	if result.Content != "done" || result.Error != nil || len(executed) != 1 {
		t.Fatalf("timeout should not fail the turn: %+v executed=%v", result, executed)
	}
}

func TestForcedMentionSleepCanceledTurnDoesNotWaitOrRecord(t *testing.T) {
	var executed []string
	e := &Engine{ToolRegistry: map[string]ToolExecutor{timesleep.ToolName: recordingSleepTool{args: &executed}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ran, abort := e.runForcedMentionSleep(ctx)

	if ran || abort == nil || !abort.Silent || len(executed) != 0 {
		t.Fatalf("canceled turn waited or recorded: ran=%v abort=%+v executed=%v", ran, abort, executed)
	}
}

func TestForcedMentionSleepCanceledRunReturnsSilentWithoutModelCall(t *testing.T) {
	var executed []string
	calls := 0
	e := &Engine{MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{timesleep.ToolName: recordingSleepTool{args: &executed}}}
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		return finalInjection("done"), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "@bot"}}, RunContext{Mock: true, Context: ctx, ReplyToBotMentionOnly: true})

	if !result.Silent || calls != 0 || len(executed) != 0 {
		t.Fatalf("canceled run proceeded: %+v calls=%d executed=%v", result, calls, executed)
	}
}
