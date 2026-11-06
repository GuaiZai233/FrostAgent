package llm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

type switchedTool struct{ calls int }

func (*switchedTool) Name() string                     { return "switch_fixture" }
func (*switchedTool) Description() string              { return "Synthetic test tool" }
func (*switchedTool) Parameters() map[string]any       { return map[string]any{"type": "object"} }
func (t *switchedTool) Execute(string) (string, error) { t.calls++; return "executed", nil }

type staleToolProvider struct {
	t      *testing.T
	tool   *switchedTool
	rounds int
}

func (p *staleToolProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.rounds++
	if len(req.Tools) != 0 {
		p.t.Fatal("disabled tool advertised to model")
	}
	if p.rounds == 1 {
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "synthetic-call", Type: "function", Function: core.ToolCallFunction{Name: p.tool.Name(), Arguments: "{}"},
		}}}}, nil
	}
	if p.tool.calls != 0 {
		p.t.Fatal("stale model call executed disabled tool")
	}
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "blocked"}}, nil
}

func TestToolSwitchRejectsStaleCallsAndIsInstanceLocal(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	tool := &switchedTool{}
	provider := &staleToolProvider{t: t, tool: tool}
	e := &Engine{Scope: scope, MaxIterations: 2, Provider: provider, ToolRegistry: map[string]ToolExecutor{tool.Name(): tool}}
	other := &Engine{ToolRegistry: e.ToolRegistry}
	if len(e.ModelTools()) != 1 {
		t.Fatal("missing configuration must preserve enabled default")
	}
	if err := config.Update(DisabledToolsKey, `["switch_fixture"]`, false); err != nil {
		t.Fatal(err)
	}
	if got := e.Run("synthetic prompt"); got != "blocked" {
		t.Fatalf("result = %q", got)
	}
	if len(other.ModelTools()) != 1 {
		t.Fatal("switch leaked to another instance")
	}
	if err := config.Update(DisabledToolsKey, "[]", false); err != nil {
		t.Fatal(err)
	}
	if len(e.ModelTools()) != 1 {
		t.Fatal("re-enabling required runtime restart")
	}
	if err := config.Update(DisabledToolsKey, "malformed", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.findToolExecutor(tool.Name()); ok {
		t.Fatal("malformed switch configuration must fail closed")
	}
}
