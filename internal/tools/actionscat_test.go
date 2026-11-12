package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

// Adapt the existing lifecycle/authorization fixtures to exercise the public router.
func routedActionsCatTestTool(client *actionscat.Client, action string, scopes ...*runtimescope.Scope) Tool {
	router := ActionsCatTool(client, scopes...)
	return Tool{
		name: "actionscat/" + action,
		executeContext: func(ctx context.Context, args string) (string, error) {
			envelope, err := json.Marshal(struct {
				Action    string          `json:"action"`
				Arguments json.RawMessage `json:"arguments"`
			}{action, json.RawMessage(args)})
			if err != nil {
				return "", err
			}
			return router.ExecuteContext(ctx, string(envelope))
		},
	}
}

func TestActionsCatRouterDiscovery(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	scope := newTestScope(t, map[string]string{"ACTIONSCAT_ENDPOINT": server.URL})
	tool := ActionsCatTool(actionscat.New(scope.Getenv), scope)
	engine := &llm.Engine{Scope: scope, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool}}
	catalog := engine.ModelTools()
	if len(catalog) != 1 || catalog[0].Name != "actionscat" {
		t.Fatalf("catalog = %+v", catalog)
	}
	// The model's initial schema must not contain the large source/build/runtime schemas.
	schema, _ := json.Marshal(catalog[0].Parameters)
	if strings.Contains(string(schema), "build_spec") || strings.Contains(string(schema), "capture_env_map") {
		t.Fatalf("eager subtool schema: %s", schema)
	}
	out, err := tool.Execute(`{"action":"discover"}`)
	if err != nil {
		t.Fatal(err)
	}
	var discovery struct {
		Capabilities []string `json:"capabilities"`
		Available    bool     `json:"available"`
	}
	if err := json.Unmarshal([]byte(out), &discovery); err != nil {
		t.Fatal(err)
	}
	want := []string{"list_actions", "run_action", "get_run", "create_action", "create_version", "build_version", "get_build", "list_builds", "activate_build", "deploy_action", "create_schedule", "list_schedules", "delete_schedule", "create_matcher", "list_matchers", "delete_matcher"}
	if !reflect.DeepEqual(discovery.Capabilities, want) || !discovery.Available {
		t.Fatalf("discover = %s", out)
	}
	for _, capability := range want {
		t.Run(capability, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{"action": "discover", "capability": capability})
			out, err := tool.Execute(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			var detail struct {
				Capability  string         `json:"capability"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			}
			if err := json.Unmarshal([]byte(out), &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Capability != capability || detail.Description == "" || detail.Parameters["type"] != "object" {
				t.Fatalf("incomplete discovery: %s", out)
			}
			if len(detail.Parameters["properties"].(map[string]any)) == 0 {
				t.Fatalf("missing parameters: %s", out)
			}
			if strings.Contains(detail.Description, "actionscat_") {
				t.Fatalf("obsolete call guidance: %s", detail.Description)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("discovery contacted ActionsCat")
	}
}

func TestActionsCatRouterInvalidEnvelopeHasNoSideEffects(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	scope := newTestScope(t, map[string]string{"ACTIONSCAT_ENDPOINT": server.URL, "ADMIN_QQ_IDS": "fixture-admin"})
	tool := ActionsCatTool(actionscat.New(scope.Getenv), scope)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{ActorUserID: "fixture-admin"})
	for _, raw := range []string{
		``, `null`, `[]`, `{`, `{}`, `{"action":"missing"}`,
		`{"action":"discover","capability":"missing"}`, `{"action":"discover","arguments":{"name":"unexpected"}}`,
		`{"action":"create_action","capability":"discover","arguments":{"name":"unexpected"}}`,
		`{"action":"create_action","arguments":null}`, `{"action":"create_action","arguments":[]}`, `{"action":"create_action","arguments":"{}"}`,
		`{"action":"create_action","arguments":{"name":"unexpected"},"extra":true}`, `{"action":"create_action","arguments":{"name":"unexpected"}} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			result, err := tool.ExecuteResult(ctx, raw)
			if err == nil || result.Content != "" {
				t.Fatalf("accepted invalid envelope: %+v, %v", result, err)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid envelopes made %d requests", requests.Load())
	}
}

func TestActionsCatRouterSwitchesAndInstanceIsolation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte(`[]`)) }))
	defer server.Close()
	scope := newTestScope(t, map[string]string{"ACTIONSCAT_ENDPOINT": server.URL})
	client := actionscat.New(scope.Getenv)
	router := ActionsCatTool(client, scope)
	other := newTestScope(t, map[string]string{"ACTIONSCAT_ENDPOINT": server.URL})
	otherRouter := ActionsCatTool(actionscat.New(other.Getenv), other)
	if err := scope.Config.Update(llm.DisabledToolsKey, `["actionscat_list_actions"]`, false); err != nil {
		t.Fatal(err)
	}
	ctx := runtimescope.WithContext(context.Background(), other)
	if _, err := router.ExecuteContext(ctx, `{"action":"list_actions"}`); err == nil {
		t.Fatal("legacy disabled capability executed using another instance's context")
	}
	if _, err := router.Execute(`{"action":"discover","capability":"list_actions"}`); err == nil {
		t.Fatal("disabled capability inspected")
	}
	out, err := router.Execute(`{"action":"discover"}`)
	if err != nil || strings.Contains(out, `"list_actions"`) {
		t.Fatalf("disabled discovery = %s, %v", out, err)
	}
	if _, err := otherRouter.Execute(`{"action":"list_actions"}`); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
	if err := scope.Config.Update(llm.DisabledToolsKey, `[]`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Execute(`{"action":"list_actions"}`); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("omitted arguments not dispatched: %d", requests.Load())
	}
	if err := scope.Config.Update("ACTIONSCAT_ENABLED", "false", false); err != nil {
		t.Fatal(err)
	}
	if router.IsEnabled() {
		t.Fatal("disabled router visible")
	}
	for _, raw := range []string{`invalid-json`, `{"action":"discover"}`, `{"action":"list_actions"}`} {
		out, err := router.Execute(raw)
		assertActionsCatDisabled(t, out, err)
		result, err := router.ExecuteResult(context.Background(), raw)
		assertActionsCatDisabled(t, result.Content, err)
	}
	if requests.Load() != 2 {
		t.Fatal("disabled router contacted backend")
	}
	if !otherRouter.IsEnabled() {
		t.Fatal("disabled another instance")
	}
	if err := scope.Config.Update("ACTIONSCAT_ENABLED", "true", false); err != nil {
		t.Fatal(err)
	}
	if !router.IsEnabled() {
		t.Fatal("router not restored")
	}
	for _, client := range []*actionscat.Client{nil, actionscat.New(func(string) string { return "" })} {
		tool := ActionsCatTool(client)
		if tool.IsEnabled() {
			t.Fatal("unconfigured router exposed")
		}
		out, err := tool.Execute(`{"action":"list_actions"}`)
		if err != nil || !strings.Contains(out, "尚未配置") {
			t.Fatalf("unconfigured = %q, %v", out, err)
		}
		out, err = tool.Execute(`{"action":"discover"}`)
		if err != nil || !strings.Contains(out, `"available":false`) {
			t.Fatalf("unconfigured discovery = %s, %v", out, err)
		}
	}
	if err := scope.Config.Update("ACTIONSCAT_ENABLED", "false", false); err != nil {
		t.Fatal(err)
	}
	_, err = router.Execute(`{"action":"run_action"}`)
	if !errors.Is(err, actionscat.ErrDisabled) {
		t.Fatalf("disabled error: %v", err)
	}
}
