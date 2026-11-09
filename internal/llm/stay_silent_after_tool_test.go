package llm

import (
	"context"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

// A read-only tool followed by stay_silent must end the run as a terminal
// silence with no final text, so the adapter sends nothing for it.
func TestStaySilentAfterReadOnlyToolEndsRunSilently(t *testing.T) {
	probe := &staticTool{name: "memory_probe", result: `{"hits":0}`}
	provider := &sequentialProvider{
		responses: []core.ChatResponse{
			{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "probe", Type: "function", Function: core.ToolCallFunction{Name: "memory_probe", Arguments: `{}`}}}}},
			{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: StaySilentToolName, Arguments: `{}`}}}}},
		},
	}
	engine := &Engine{
		MaxIterations:  5,
		Provider:       provider,
		SessionManager: NewSessionManager(),
		ToolRegistry: map[string]ToolExecutor{
			"memory_probe":     probe,
			StaySilentToolName: &staticTool{name: StaySilentToolName, result: "已选择保持沉默"},
		},
	}

	result := engine.runLoopWithResult(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})

	if !result.Silent {
		t.Fatalf("expected terminal silence after read-only tool, got %+v", result)
	}
	if result.Content != "" {
		t.Fatalf("terminal silence must carry no final text, got %q", result.Content)
	}
}
