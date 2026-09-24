package llm

import (
	"FrostAgent/internal/core"
	"context"
	"strings"
	"testing"
)

func TestEngineEffectiveMaxIterations(t *testing.T) {
	// 1. Nil engine defaults to DefaultMaxIterations
	var nilEngine *Engine
	if nilEngine.EffectiveMaxIterations() != DefaultMaxIterations {
		t.Fatalf("expected nil engine to return DefaultMaxIterations (%d), got %d", DefaultMaxIterations, nilEngine.EffectiveMaxIterations())
	}

	// 2. Engine with MaxIterations=0 defaults to 35
	e1 := &Engine{}
	if e1.EffectiveMaxIterations() != 35 {
		t.Fatalf("expected 35, got %d", e1.EffectiveMaxIterations())
	}

	// 3. Engine with MaxIterations=7 returns 7
	e2 := &Engine{MaxIterations: 7}
	if e2.EffectiveMaxIterations() != 7 {
		t.Fatalf("expected 7, got %d", e2.EffectiveMaxIterations())
	}

	// 4. Engine with non-positive MaxIterations falls back to DefaultMaxIterations
	e3 := &Engine{MaxIterations: -5}
	if e3.EffectiveMaxIterations() != DefaultMaxIterations {
		t.Fatalf("expected %d, got %d", DefaultMaxIterations, e3.EffectiveMaxIterations())
	}
}

type infiniteToolProvider struct {
	iterationCount int
}

func (p *infiniteToolProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	p.iterationCount++
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{
				{
					ID:   "call_dummy",
					Type: "function",
					Function: core.ToolCallFunction{
						Name:      "dummy_tool",
						Arguments: "{}",
					},
				},
			},
		},
	}, nil
}

type dummyTool struct{}

func (d *dummyTool) Name() string                { return "dummy_tool" }
func (d *dummyTool) Description() string         { return "dummy" }
func (d *dummyTool) Parameters() map[string]any { return map[string]any{} }
func (d *dummyTool) Execute(args string) (string, error) {
	return "ok", nil
}

func TestEngineLoopTerminatesAtEffectiveMaxIterations(t *testing.T) {
	provider := &infiniteToolProvider{}
	engine := &Engine{
		MaxIterations: 4,
		Provider:     provider,
		ToolRegistry: map[string]ToolExecutor{"dummy_tool": &dummyTool{}},
	}

	res := engine.RunMessagesWithContext([]ChatMessage{
		{Role: "user", Content: "loop forever"},
	}, RunContext{})

	if provider.iterationCount != 4 {
		t.Fatalf("expected loop to terminate after 4 iterations, got %d", provider.iterationCount)
	}
	if !strings.Contains(res.Content, "达到最大迭代次数") {
		t.Fatalf("expected max iteration error message, got %q", res.Content)
	}
}
