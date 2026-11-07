package tools

import (
	"context"
	"encoding/json"
	"errors"
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
	tool := BalanceQueryTool(func(string) string { return string(raw) }, nil)
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
	if BalanceQueryTool(nil, nil).IsEnabled() {
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
	const key = "synthetic-model-boundary-key"
	cfg := balancequery.Config{Curl: "curl " + server.URL + " -H 'Authorization: Bearer {{API_KEY}}'", ProviderID: "fixture-provider"}
	raw, _ := json.Marshal(cfg)
	scope := newTestScope(t, map[string]string{balancequery.ConfigKey: string(raw)})
	tool := BalanceQueryTool(scope.Config.Get, func(string) (string, error) { return key, nil })
	provider := &balanceModelProvider{t: t, key: key, endpoint: server.URL}
	engine := &llm.Engine{Scope: scope, MaxIterations: 2, Provider: provider, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool}}
	if got := engine.Run("query synthetic balance"); got != "checked" || provider.rounds != 2 {
		t.Fatalf("roundtrip failed: %q, rounds=%d", got, provider.rounds)
	}
}

func TestBalanceQueryUsesCurrentProviderKeyAndFailsClosed(t *testing.T) {
	var key atomic.Value
	key.Store("synthetic-first-provider-key")
	var unavailable, echo atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+key.Load().(string) {
			t.Error("query did not use the current provider credential")
		}
		if echo.Load() {
			_, _ = io.WriteString(w, key.Load().(string))
		} else {
			_, _ = io.WriteString(w, `{"balance":42}`)
		}
	}))
	defer server.Close()
	raw, _ := json.Marshal(balancequery.Config{Curl: "curl " + server.URL + " -H 'Authorization: Bearer {{API_KEY}}'", ProviderID: "fixture-provider"})
	get := func(string) string { return string(raw) }
	tool := BalanceQueryTool(get, func(id string) (string, error) {
		if id != "fixture-provider" {
			t.Fatal("resolved a provider other than the saved selection")
		}
		if unavailable.Load() {
			return "", errors.New("synthetic backend detail: " + key.Load().(string))
		}
		return key.Load().(string), nil
	})
	for _, current := range []string{"synthetic-first-provider-key", "synthetic-rotated-provider-key"} {
		key.Store(current)
		if !tool.IsEnabled() {
			t.Fatal("available provider was not advertised")
		}
		if result, err := tool.Execute("{}"); err != nil || result != `{"balance":42}` {
			t.Fatalf("query failed: %q, %v", result, err)
		}
	}
	echo.Store(true)
	if result, err := tool.Execute("{}"); err == nil || result != "" || strings.Contains(err.Error(), key.Load().(string)) {
		t.Fatal("provider credential echo crossed the model boundary")
	}
	unavailable.Store(true)
	before := calls.Load()
	if tool.IsEnabled() || BalanceQueryTool(get, nil).IsEnabled() {
		t.Fatal("unavailable provider was advertised")
	}
	if result, err := tool.Execute("{}"); err == nil || result != "" || strings.Contains(err.Error(), key.Load().(string)) {
		t.Fatal("unavailable provider did not fail without exposing backend details")
	}
	if calls.Load() != before {
		t.Fatal("unavailable provider contacted the query endpoint")
	}
}
