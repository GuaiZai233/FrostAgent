package botstatus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

type settingsTestTool string

func (t settingsTestTool) Name() string                 { return string(t) }
func (settingsTestTool) Description() string            { return "Synthetic tool" }
func (settingsTestTool) Parameters() map[string]any     { return nil }
func (settingsTestTool) Execute(string) (string, error) { return "", nil }

func TestQQExtendCardAndRenamedToolSwitch(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	engine := &llm.Engine{Scope: scope, ToolRegistry: map[string]llm.ToolExecutor{qqextend.ToolName: tools.QQTool()}}
	if err := config.Update(llm.DisabledToolsKey, `["qq"]`, false); err != nil {
		t.Fatal(err)
	}
	if engine.ToolEnabled(qqextend.ToolName) {
		t.Fatal("renaming bypassed the previous disabled switch")
	}
	service := New(engine, "test", "")
	overview, err := service.GetOverview(context.Background(), connect.NewRequest(&v1.GetOverviewRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Msg.Tools) != 1 || overview.Msg.Tools[0].Name != qqextend.ToolName || !overview.Msg.Tools[0].Configurable || overview.Msg.Tools[0].Enabled {
		t.Fatalf("%+v", overview.Msg.Tools)
	}
	for _, enabled := range []bool{true, false, true} {
		if _, err := service.SetToolEnabled(context.Background(), connect.NewRequest(&v1.SetToolEnabledRequest{Name: qqextend.ToolName, Enabled: enabled})); err != nil {
			t.Fatal(err)
		}
		if engine.ToolEnabled(qqextend.ToolName) != enabled {
			t.Fatal("renamed tool switch did not apply")
		}
	}
}

func TestSetToolEnabledRejectsUnconfiguredBalanceQuery(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	engine := &llm.Engine{Scope: scope, ToolRegistry: map[string]llm.ToolExecutor{}}
	balanceTool := tools.BalanceQueryTool(config.Get, nil)
	engine.ToolRegistry[balanceTool.Name()] = balanceTool
	service := New(engine, "test", "")

	_, err = service.SetToolEnabled(context.Background(), connect.NewRequest(&v1.SetToolEnabledRequest{Name: balancequery.ToolName, Enabled: true}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "工具启用失败：请先点击卡片后进行配置！") {
		t.Fatalf("unconfigured enable returned %v", err)
	}

	raw, _ := json.Marshal(balancequery.Config{Curl: balancequery.DefaultCurl, APIKey: "synthetic-configured-key"})
	if err := config.Update(balancequery.ConfigKey, string(raw), false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetToolEnabled(context.Background(), connect.NewRequest(&v1.SetToolEnabledRequest{Name: balancequery.ToolName, Enabled: true})); err != nil {
		t.Fatalf("configured enable failed: %v", err)
	}
	if !engine.ToolEnabled(balancequery.ToolName) {
		t.Fatal("configured balance query was not enabled")
	}
}

func TestSetToolEnabledConcurrentUpdates(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	names := []string{"fixture-a", "fixture-b", "fixture-c"}
	engine := &llm.Engine{Scope: scope, ToolRegistry: map[string]llm.ToolExecutor{}}
	for _, name := range names {
		engine.ToolRegistry[name] = settingsTestTool(name)
	}
	service := New(engine, "test", "")
	results := make(chan error, len(names))
	for _, name := range names {
		go func() {
			_, err := service.SetToolEnabled(context.Background(), connect.NewRequest(&v1.SetToolEnabledRequest{Name: name, Enabled: false}))
			results <- err
		}()
	}
	for range names {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		if engine.ToolEnabled(name) {
			t.Errorf("concurrent update lost switch for %s", name)
		}
	}
}

func TestActionsCatLegacyDisableCanBeClearedFromOverview(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/actions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "instance.env")
	config, err := instanceconfig.Open(configPath, false)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"ACTIONSCAT_ENDPOINT": server.URL,
		llm.DisabledToolsKey:  `["actionscat_list_actions","actionscat_list_actions","actionscat_run_action","stay_silent","actionscat_unknown"]`,
	} {
		if err := config.Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	otherConfig, err := instanceconfig.Open(filepath.Join(t.TempDir(), "other.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := otherConfig.Update(llm.DisabledToolsKey, `["actionscat_list_actions"]`, false); err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	router := tools.ActionsCatTool(actionscat.New(scope.Getenv), scope)
	engine := &llm.Engine{Scope: scope, ToolRegistry: map[string]llm.ToolExecutor{router.Name(): router}}
	service := New(engine, "test", "")
	ctx := context.Background()
	if _, err := router.Execute(`{"action":"list_actions"}`); err == nil {
		t.Fatal("legacy disabled capability executed")
	}
	assertOverview := func(wantLegacy bool) {
		t.Helper()
		response, err := service.GetOverview(ctx, connect.NewRequest(&v1.GetOverviewRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		names := make(map[string]bool)
		for _, tool := range response.Msg.Tools {
			if names[tool.Name] {
				t.Fatalf("duplicate overview entry: %s", tool.Name)
			}
			names[tool.Name] = true
			if tool.Name != "actionscat" && (tool.Enabled || !tool.Available || tool.Description == "") {
				t.Fatalf("legacy recovery control unusable: %+v", tool)
			}
		}
		if names["actionscat_list_actions"] != wantLegacy || !names["actionscat_run_action"] || !names["actionscat"] || names["actionscat_unknown"] || names["stay_silent"] {
			t.Fatalf("overview entries: %v", names)
		}
		if model := engine.ModelTools(); len(model) != 1 || model[0].Name != "actionscat" {
			t.Fatalf("legacy setting exposed to model: %+v", model)
		}
		if _, registered := engine.ToolRegistry["actionscat_list_actions"]; registered {
			t.Fatal("legacy execution entry registered")
		}
	}
	assertOverview(true)
	for range 2 {
		if _, err := service.SetToolEnabled(ctx, connect.NewRequest(&v1.SetToolEnabledRequest{Name: "actionscat_list_actions", Enabled: true})); err != nil {
			t.Fatalf("console could not re-enable capability: %v", err)
		}
	}
	assertOverview(false)
	if got := config.Get(llm.DisabledToolsKey); got != `["actionscat_run_action","actionscat_unknown","stay_silent"]` {
		t.Fatalf("clearing legacy switch changed unrelated settings: %s", got)
	}
	if got := otherConfig.Get(llm.DisabledToolsKey); got != `["actionscat_list_actions"]` {
		t.Fatalf("clearing legacy switch changed another instance: %s", got)
	}
	out, err := router.Execute(`{"action":"discover"}`)
	if err != nil || !strings.Contains(out, `"list_actions"`) || strings.Contains(out, `"run_action"`) {
		t.Fatalf("capabilities after recovery: %s, %v", out, err)
	}
	if _, err := router.Execute(`{"action":"list_actions"}`); err != nil {
		t.Fatalf("re-enabled capability refused: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want only the recovered list call", requests.Load())
	}
	reopened, err := instanceconfig.Open(configPath, false)
	if err != nil || reopened.Get(llm.DisabledToolsKey) != config.Get(llm.DisabledToolsKey) {
		t.Fatalf("recovery did not persist: %v", err)
	}
	for _, request := range []*v1.SetToolEnabledRequest{
		{Name: "actionscat_list_actions", Enabled: false},
		{Name: "actionscat_unknown", Enabled: true},
		{Name: "unregistered", Enabled: true},
	} {
		if _, err := service.SetToolEnabled(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("unsupported legacy update %+v returned %v", request, err)
		}
	}
	// Cleanup remains available while the router is disabled or unconfigured.
	if _, err := service.SetToolEnabled(ctx, connect.NewRequest(&v1.SetToolEnabledRequest{Name: "actionscat", Enabled: false})); err != nil {
		t.Fatal(err)
	}
	if err := config.Update("ACTIONSCAT_ENABLED", "false", false); err != nil {
		t.Fatal(err)
	}
	if err := config.Update("ACTIONSCAT_ENDPOINT", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetToolEnabled(ctx, connect.NewRequest(&v1.SetToolEnabledRequest{Name: "actionscat_run_action", Enabled: true})); err != nil {
		t.Fatal(err)
	}
	if router.IsEnabled() || engine.ToolEnabled("actionscat") || requests.Load() != 1 {
		t.Fatal("clearing a legacy switch enabled the integration or called its backend")
	}
	if err := config.Update(llm.DisabledToolsKey, `invalid-json`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetToolEnabled(ctx, connect.NewRequest(&v1.SetToolEnabledRequest{Name: "actionscat_list_actions", Enabled: true})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("malformed settings were silently replaced: %v", err)
	}
	if config.Get(llm.DisabledToolsKey) != `invalid-json` {
		t.Fatal("malformed settings overwritten")
	}
	delete(engine.ToolRegistry, router.Name())
	if _, err := service.SetToolEnabled(ctx, connect.NewRequest(&v1.SetToolEnabledRequest{Name: "actionscat_list_actions", Enabled: true})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("absent router accepted legacy switch: %v", err)
	}
}
