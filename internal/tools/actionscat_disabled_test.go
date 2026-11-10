package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/service/botstatus"
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
			wantEnabled := 1
			if endpoint != "" {
				wantEnabled += 16
			}
			checkOverviewCount(17)
			if got := len(engine.ModelTools()); got != wantEnabled {
				t.Fatalf("default enabled tools = %d, want %d", got, wantEnabled)
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
			checkOverviewCount(17)
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
			other := actionscat.New(newTestScope(t, map[string]string{"ACTIONSCAT_ENDPOINT": server.URL}).Getenv)
			if !ActionsCatListActionsTool(other).IsEnabled() {
				t.Fatal("disabling one instance affected another")
			}
			if err := scope.Config.Update("ACTIONSCAT_ENABLED", "true", false); err != nil {
				t.Fatal(err)
			}
			if got := len(engine.ModelTools()); got != wantEnabled {
				t.Fatalf("re-enabled model tools = %d, want %d", got, wantEnabled)
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
	content, _ := last.Content.(string)
	prefix := "[FrostAgent 错误] 工具调用失败：ActionsCat 已被管理员禁用 (Ref_ID: "
	if last.Role != core.RoleTool || !strings.HasPrefix(content, prefix) {
		p.t.Errorf("stale call response = %+v", last)
	} else {
		id := strings.TrimSuffix(strings.TrimPrefix(content, prefix), ")")
		if _, found := p.scope.Log().FindError(id); !found {
			p.t.Errorf("missing stale-call diagnostics: %q", content)
		}
	}
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "disabled"}}, nil
}

func TestActionsCatDisableDuringModelCallRejectsStaleCall(t *testing.T) {
	scope := newTestScope(t, map[string]string{"ACTIONSCAT_ENDPOINT": "http://actionscat.example"})
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

// Check the actual request to the model, including hot endpoint changes.
type actionsCatVisibilityProvider struct {
	t     *testing.T
	want  int
	calls int
}

func (p *actionsCatVisibilityProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.calls++
	count := 0
	for _, tool := range req.Tools {
		if strings.HasPrefix(tool.Name, "actionscat_") {
			count++
		}
	}
	if count != p.want || len(req.Tools) != p.want+1 {
		p.t.Errorf("model request exposes %d ActionsCat tools and %d total, want %d and %d", count, len(req.Tools), p.want, p.want+1)
	}
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "checked"}}, nil
}

func TestActionsCatToolVisibilityFollowsConfiguration(t *testing.T) {
	scope := newTestScope(t, nil)
	client := actionscat.New(scope.Getenv)
	provider := &actionsCatVisibilityProvider{t: t}
	ordinary := StaySilentTool()
	engine := &llm.Engine{
		Scope: scope, MaxIterations: 1, Provider: provider,
		ToolRegistry: map[string]llm.ToolExecutor{ordinary.Name(): ordinary},
	}
	registry := NewRegistry()
	for _, tool := range actionsCatTestTools(client, scope) {
		engine.ToolRegistry[tool.Name()] = tool
		registry.Register(tool)
	}
	for _, step := range []struct {
		name     string
		endpoint string
		enabled  string
		want     int
	}{
		{"missing endpoint", "", "", 0},
		{"whitespace endpoint", "  ", "true", 0},
		{"slash-only endpoint", "///", "true", 0},
		{"configured with default switch", "http://actionscat.example", "", 16},
		{"disabled", "http://actionscat.example", "false", 0},
		{"re-enabled", "http://actionscat.example", "true", 16},
		{"endpoint removed", "", "true", 0},
		{"endpoint restored", "http://actionscat.example/", "true", 16},
	} {
		t.Run(step.name, func(t *testing.T) {
			for key, value := range map[string]string{"ACTIONSCAT_ENDPOINT": step.endpoint, "ACTIONSCAT_ENABLED": step.enabled} {
				if err := scope.Config.Update(key, value, false); err != nil {
					t.Fatal(err)
				}
			}
			provider.t, provider.want = t, step.want
			before := provider.calls
			if got := engine.Run("check tools"); got != "checked" {
				t.Fatalf("model result = %q", got)
			}
			if provider.calls != before+1 {
				t.Fatalf("model calls = %d, want %d", provider.calls, before+1)
			}
			if got := len(registry.ListTools()); got != step.want {
				t.Fatalf("registry exposes %d tools, want %d", got, step.want)
			}
		})
	}
}

func TestActionsCatNilClientToolsHidden(t *testing.T) {
	scope := newTestScope(t, map[string]string{"ADMIN_QQ_IDS": "synthetic-admin"})
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{ActorUserID: "synthetic-admin"})
	for _, tool := range actionsCatTestTools(nil, scope) {
		if tool.IsEnabled() {
			t.Errorf("%s exposed with nil client", tool.Name())
		}
		out, err := tool.ExecuteContext(ctx, "{}")
		if err != nil || !strings.Contains(out, "尚未配置") {
			t.Errorf("%s unconfigured result = %q, %v", tool.Name(), out, err)
		}
	}
}
