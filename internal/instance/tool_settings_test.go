package instance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/timesleep"
)

func TestTimeSleepRegistrationAndInstanceSettings(t *testing.T) {
	m := testManager(t)
	a, b := create(t, m, "sleep-a"), create(t, m, "sleep-b")
	engine := m.instances[a.ID].runtime.Engine
	tool, ok := engine.ToolRegistry[timesleep.ToolName]
	if !ok {
		t.Fatal("time_sleep not registered")
	}
	response := rpc(t, m, a.ID, "BotStatusService/GetOverview", "{}", false)
	var overview struct {
		Tools []struct {
			Name         string
			Configurable bool
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	configurable := false
	for _, item := range overview.Tools {
		if item.Name == timesleep.ToolName {
			configurable = item.Configurable
		}
	}
	if !configurable {
		t.Fatal("time_sleep has no configuration card")
	}
	response = rpc(t, m, a.ID, "SettingsService/UpdateEnvVar", `{"key":"TIME_SLEEP_TIMEOUT_SECONDS","value":"0.001"}`, false)
	if response.Code != http.StatusOK {
		t.Fatalf("save timeout: %d %s", response.Code, response.Body.String())
	}
	if engine != m.instances[a.ID].runtime.Engine {
		t.Fatal("timeout setting restarted runtime")
	}
	_, err := tool.(core.StructuredToolExecutor).ExecuteResult(context.Background(), `{"seconds":1}`)
	if !errors.Is(err, timesleep.ErrTimeout) {
		t.Fatalf("runtime ignored live timeout: %v", err)
	}
	if m.instances[b.ID].runtime.Engine.Config.Get(timesleep.TimeoutKey) != "" {
		t.Fatal("timeout leaked across instances")
	}
	response = rpc(t, m, a.ID, "BotStatusService/SetToolEnabled", `{"name":"time_sleep","enabled":false}`, false)
	if response.Code != http.StatusOK || engine.ToolEnabled(timesleep.ToolName) {
		t.Fatal("sleep switch failed")
	}
	if err := m.Enable(a.ID, true); err != nil {
		t.Fatal(err)
	}
	rebuilt := m.instances[a.ID].runtime.Engine
	if rebuilt.Config.Get(timesleep.TimeoutKey) != "0.001" || rebuilt.ToolEnabled(timesleep.ToolName) {
		t.Fatal("runtime rebuild lost sleep configuration")
	}
}

func TestToolSwitchPersistsAndKeepsRegisteredCard(t *testing.T) {
	m := testManager(t)
	a, b := create(t, m, "tools-a"), create(t, m, "tools-b")
	call := func(id, method, body string) {
		t.Helper()
		w := rpc(t, m, id, "BotStatusService/"+method, body, false)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
	}
	call(a.ID, "SetToolEnabled", `{"name":"send_message","enabled":false}`)
	if m.instances[a.ID].runtime.Engine.ToolEnabled("send_message") {
		t.Fatal("switch did not update runtime")
	}
	if !m.instances[b.ID].runtime.Engine.ToolEnabled("send_message") {
		t.Fatal("switch affected another instance")
	}
	w := rpc(t, m, a.ID, "BotStatusService/GetOverview", "{}", false)
	var overview struct {
		Tools []struct {
			Name    string
			Enabled bool
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range overview.Tools {
		if tool.Name == "send_message" {
			found = true
			if tool.Enabled {
				t.Fatal("disabled card is checked")
			}
		}
	}
	if !found {
		t.Fatal("disabled tool disappeared from cards")
	}
	if err := m.Enable(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if m.instances[a.ID].runtime.Engine.ToolEnabled("send_message") {
		t.Fatal("runtime rebuild lost switch")
	}
	call(a.ID, "SetToolEnabled", `{"name":"send_message","enabled":true}`)
	w = rpc(t, m, a.ID, "BotStatusService/SetToolEnabled", `{"name":"unregistered","enabled":false}`, false)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown tool: %d", w.Code)
	}
}

func TestToolSwitchSurvivesDatabaseReopen(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	dir := t.TempDir()
	m, err := NewDatabase(dir)
	if err != nil {
		t.Fatal(err)
	}
	i := create(t, m, "persistent-tools")
	configBody, _ := json.Marshal(map[string]any{"curl": balancequery.DefaultCurl, "apiKey": "synthetic-persistent-balance-key"})
	configured := rpc(t, m, i.ID, "BotStatusService/SaveBalanceQueryConfig", string(configBody), false)
	if configured.Code != http.StatusOK {
		m.Close()
		t.Fatalf("configure balance: %d %s", configured.Code, configured.Body.String())
	}
	w := rpc(t, m, i.ID, "BotStatusService/SetToolEnabled", `{"name":"stay_silent","enabled":false}`, false)
	if w.Code != http.StatusOK {
		m.Close()
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	m.Close()
	reopened, err := NewDatabase(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.instances[i.ID].runtime.Engine.ToolEnabled("stay_silent") {
		t.Fatal("database reopen lost tool switch")
	}
	if !balancequery.Configured(reopened.instances[i.ID].runtime.Engine.Config.Get) {
		t.Fatal("database reopen lost balance configuration")
	}
}
