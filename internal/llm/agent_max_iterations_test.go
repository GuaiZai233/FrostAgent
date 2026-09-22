package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineEffectiveMaxIterations(t *testing.T) {
	// 1. Nil engine defaults to DefaultMaxIterations
	var nilEngine *Engine
	if nilEngine.EffectiveMaxIterations() != DefaultMaxIterations {
		t.Fatalf("expected nil engine to return DefaultMaxIterations (%d), got %d", DefaultMaxIterations, nilEngine.EffectiveMaxIterations())
	}

	// 2. Engine with no Scope and MaxIterations=0 defaults to 35
	e1 := &Engine{}
	if e1.EffectiveMaxIterations() != 35 {
		t.Fatalf("expected 35, got %d", e1.EffectiveMaxIterations())
	}

	// 3. Engine with no Scope and MaxIterations=7 returns 7
	e2 := &Engine{MaxIterations: 7}
	if e2.EffectiveMaxIterations() != 7 {
		t.Fatalf("expected 7, got %d", e2.EffectiveMaxIterations())
	}

	// 4. Engine with Scope containing AGENT_MAX_ITERATIONS
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	store, err := instanceconfig.Open(envPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update("AGENT_MAX_ITERATIONS", "50", false); err != nil {
		t.Fatal(err)
	}

	scope := runtimescope.New(store, nil, logs.General)
	e3 := &Engine{
		Scope:         scope,
		MaxIterations: 10, // should be overridden by scope
	}
	if e3.EffectiveMaxIterations() != 50 {
		t.Fatalf("expected 50 from scope, got %d", e3.EffectiveMaxIterations())
	}

	// 5. Engine with Scope containing non-positive or invalid AGENT_MAX_ITERATIONS falls back
	for _, badVal := range []string{"0", "-5", "abc", "  "} {
		if err := store.Update("AGENT_MAX_ITERATIONS", badVal, false); err != nil {
			t.Fatal(err)
		}
		if e3.EffectiveMaxIterations() != 10 {
			t.Fatalf("for bad val %q, expected fallback to Engine.MaxIterations=10, got %d", badVal, e3.EffectiveMaxIterations())
		}
	}

	// 6. Engine with Scope containing empty AGENT_MAX_ITERATIONS and MaxIterations=0 falls back to DefaultMaxIterations
	e4 := &Engine{
		Scope:         scope,
		MaxIterations: 0,
	}
	if err := store.Update("AGENT_MAX_ITERATIONS", "", false); err != nil {
		t.Fatal(err)
	}
	if e4.EffectiveMaxIterations() != DefaultMaxIterations {
		t.Fatalf("expected fallback to DefaultMaxIterations=35, got %d", e4.EffectiveMaxIterations())
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
	dir := t.TempDir()
	store, err := instanceconfig.Open(filepath.Join(dir, ".env"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update("AGENT_MAX_ITERATIONS", "4", false); err != nil {
		t.Fatal(err)
	}

	scope := runtimescope.New(store, nil, logs.General)
	provider := &infiniteToolProvider{}
	engine := &Engine{
		Scope:        scope,
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
