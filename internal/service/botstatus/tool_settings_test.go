package botstatus

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
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
