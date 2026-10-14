package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/actionscat"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/service/botstatus"
	"connectrpc.com/connect"
)

func actionsCatTestTools(client *actionscat.Client, scope *runtimescope.Scope) []Tool {
	return []Tool{
		ActionsCatListActionsTool(client), ActionsCatRunActionTool(client), ActionsCatGetRunTool(client),
		ActionsCatCreateActionTool(client, scope), ActionsCatCreateVersionTool(client, scope),
		ActionsCatBuildVersionTool(client, scope), ActionsCatGetBuildTool(client), ActionsCatListBuildsTool(client),
		ActionsCatActivateBuildTool(client, scope), ActionsCatDeployActionTool(client, scope),
		ActionsCatCreateScheduleTool(client, scope), ActionsCatListSchedulesTool(client),
		ActionsCatDeleteScheduleTool(client, scope), ActionsCatCreateMatcherTool(client, scope),
		ActionsCatListMatchersTool(client), ActionsCatDeleteMatcherTool(client, scope),
	}
}

func TestActionsCatDisabledToolsHideAndRejectAllEntryPoints(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	for _, endpoint := range []string{"", server.URL} {
		t.Run("endpoint="+endpoint, func(t *testing.T) {
			scope := newTestScope(t, map[string]string{"ACTIONSCAT_ENDPOINT": endpoint})
			client := actionscat.New(scope.Getenv)
			engine := &llm.Engine{ToolRegistry: map[string]llm.ToolExecutor{}}
			registry := NewRegistry()
			for _, tool := range actionsCatTestTools(client, scope) {
				engine.ToolRegistry[tool.Name()] = tool
				registry.Register(tool)
			}
			ordinary := StaySilentTool()
			engine.ToolRegistry[ordinary.Name()] = ordinary
			statusService := botstatus.New(engine, "test", "")
			checkOverviewCount := func(want int) {
				t.Helper()
				res, err := statusService.GetOverview(context.Background(), connect.NewRequest(&v1.GetOverviewRequest{}))
				if err != nil || len(res.Msg.Tools) != want {
					t.Fatalf("overview tool count: response=%v, error=%v, want=%d", res, err, want)
				}
			}
			checkOverviewCount(17)
			if got := len(engine.ModelTools()); got != 17 {
				t.Fatalf("default enabled tools = %d, want 17", got)
			}
			if err := scope.Config.Update("ACTIONSCAT_ENABLED", "false", false); err != nil {
				t.Fatal(err)
			}
			if got := engine.ModelTools(); len(got) != 1 || got[0].Name != ordinary.Name() {
				t.Fatalf("disabled model tools = %v", got)
			}
			if got := registry.ListTools(); len(got) != 0 {
				t.Fatalf("disabled registry still exposes tools: %v", got)
			}
			checkOverviewCount(1)
			contexts := []context.Context{
				context.Background(),
				llm.WithRunContext(context.Background(), llm.RunContext{Mock: true}),
				llm.WithRunContext(context.Background(), llm.RunContext{ActorUserID: "synthetic-user"}),
			}
			for _, tool := range actionsCatTestTools(client, scope) {
				t.Run(tool.Name(), func(t *testing.T) {
					for _, ctx := range contexts {
						// Invalid arguments must not outrank the administrator's switch.
						out, err := tool.ExecuteContext(ctx, "invalid-json")
						assertActionsCatDisabled(t, out, err)
						result, err := tool.ExecuteResult(ctx, "invalid-json")
						assertActionsCatDisabled(t, result.Content, err)
					}
					out, err := tool.Execute("{}")
					assertActionsCatDisabled(t, out, err)
					out, err = registry.Execute(tool.Name(), "{}")
					assertActionsCatDisabled(t, out, err)
				})
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("disabled tools made %d requests", got)
			}
			other := actionscat.New(newTestScope(t, nil).Getenv)
			if !ActionsCatListActionsTool(other).IsEnabled() {
				t.Fatal("disabling one instance affected another")
			}
			if err := scope.Config.Update("ACTIONSCAT_ENABLED", "true", false); err != nil {
				t.Fatal(err)
			}
			if got := len(engine.ModelTools()); got != 17 {
				t.Fatalf("re-enabled model tools = %d, want 17", got)
			}
			checkOverviewCount(17)
		})
	}
}

func assertActionsCatDisabled(t *testing.T, out string, err error) {
	t.Helper()
	if out != "" || !errors.Is(err, actionscat.ErrDisabled) || err.Error() != "ActionsCat已被FrostAgent管理员禁用。" {
		t.Fatalf("disabled result = %q, %v", out, err)
	}
}

type disablingActionsCatProvider struct {
	t     *testing.T
	scope *runtimescope.Scope
	round int
}

func (p *disablingActionsCatProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.round++
	if p.round == 1 {
		if len(req.Tools) != 1 || req.Tools[0].Name != "actionscat_list_actions" {
			p.t.Fatalf("initial tools = %v", req.Tools)
		}
		if err := p.scope.Config.Update("ACTIONSCAT_ENABLED", "false", false); err != nil {
			p.t.Fatal(err)
		}
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "stale-call", Type: "function", Function: core.ToolCallFunction{Name: "actionscat_list_actions", Arguments: "{}"},
		}}}}, nil
	}
	if len(req.Tools) != 0 {
		p.t.Fatalf("tools remain exposed after disabling: %v", req.Tools)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != core.RoleTool || last.Content != "ActionsCat已被FrostAgent管理员禁用。" {
		p.t.Fatalf("stale call response = %+v", last)
	}
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "disabled"}}, nil
}

func TestActionsCatDisableDuringModelCallRejectsStaleCall(t *testing.T) {
	scope := newTestScope(t, nil)
	tool := ActionsCatListActionsTool(actionscat.New(scope.Getenv))
	provider := &disablingActionsCatProvider{t: t, scope: scope}
	engine := &llm.Engine{
		Scope: scope, MaxIterations: 3, Provider: provider,
		ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool},
	}
	if got := engine.Run("list actions"); got != "disabled" || provider.round != 2 {
		t.Fatalf("agent result = %q, rounds = %d", got, provider.round)
	}
}
