package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

func TestBalanceQueryHasNoModelControlledRequestParameters(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-tool-secret" {
			t.Error("credential missing at request boundary")
		}
		_, _ = io.WriteString(w, `{"balance":42}`)
	}))
	defer server.Close()
	cfg := balancequery.Config{Curl: "curl " + server.URL + " -H 'Authorization: Bearer {{API_KEY}}'", APIKey: "synthetic-tool-secret"}
	raw, _ := json.Marshal(cfg)
	tool := BalanceQueryTool(func(string) string { return string(raw) })
	if !tool.IsEnabled() {
		t.Fatal("configured tool unavailable")
	}
	schema, _ := json.Marshal(tool.Parameters())
	if strings.Contains(string(schema), cfg.APIKey) || strings.Contains(string(schema), server.URL) || string(schema) == "null" {
		t.Fatal("request material leaked into tool schema")
	}
	for _, args := range []string{`{"url":"http://other.invalid"}`, `{"api_key":"attempt"}`, `null`, `[]`, `invalid`} {
		if _, err := tool.Execute(args); err == nil {
			t.Errorf("model controlled request accepted: %s", args)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid arguments contacted endpoint")
	}
	if out, err := tool.Execute("{}"); err != nil || out != `{"balance":42}` || calls.Load() != 1 {
		t.Fatalf("query failed: %q %v", out, err)
	}
	if BalanceQueryTool(nil).IsEnabled() {
		t.Fatal("unconfigured tool advertised to model")
	}
}

type balanceModelProvider struct {
	t             *testing.T
	rounds        int
	key, endpoint string
}

func (p *balanceModelProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.rounds++
	wire, _ := json.Marshal(req)
	if strings.Contains(string(wire), p.key) || strings.Contains(string(wire), p.endpoint) {
		p.t.Fatal("model request exposed balance credentials or endpoint")
	}
	if p.rounds == 1 {
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "balance-fixture-call", Type: "function", Function: core.ToolCallFunction{Name: balancequery.ToolName, Arguments: "{}"}}}}}, nil
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != core.RoleTool || last.Content != `{"balance":42}` {
		p.t.Fatal("model did not receive raw balance result")
	}
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "checked"}}, nil
}

func TestBalanceQueryModelRoundTripNeverExposesRequestMaterial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"balance":42}`) }))
	defer server.Close()
	cfg := balancequery.Config{Curl: "curl " + server.URL + " -H 'Authorization: Bearer {{API_KEY}}'", APIKey: "synthetic-model-boundary-key"}
	raw, _ := json.Marshal(cfg)
	scope := newTestScope(t, map[string]string{balancequery.ConfigKey: string(raw)})
	tool := BalanceQueryTool(scope.Config.Get)
	provider := &balanceModelProvider{t: t, key: cfg.APIKey, endpoint: server.URL}
	engine := &llm.Engine{Scope: scope, MaxIterations: 2, Provider: provider, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool}}
	if got := engine.Run("query synthetic balance"); got != "checked" || provider.rounds != 2 {
		t.Fatalf("roundtrip failed: %q, rounds=%d", got, provider.rounds)
	}
}
