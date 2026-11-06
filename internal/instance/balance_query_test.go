package instance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
)

func TestBalanceQueryManagementKeepsSecretsOnServerAndIsolatesInstances(t *testing.T) {
	m := testManager(t)
	a, b := create(t, m, "balance-a"), create(t, m, "balance-b")
	const key = "synthetic-management-balance-key"
	call := func(instanceID, method string, body any, status int) string {
		t.Helper()
		raw, _ := json.Marshal(body)
		response := rpc(t, m, instanceID, "BotStatusService/"+method, string(raw), false)
		if response.Code != status {
			t.Fatalf("%s: %d %s", method, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), key) {
			t.Fatal("management response exposed API key")
		}
		return response.Body.String()
	}
	initial := call(a.ID, "GetBalanceQueryConfig", map[string]any{}, http.StatusOK)
	if !strings.Contains(initial, "api.deepseek.com/user/balance") {
		t.Fatal("missing DeepSeek default")
	}
	call(a.ID, "SaveBalanceQueryConfig", map[string]any{"curl": balancequery.DefaultCurl, "apiKey": key}, http.StatusOK)
	engine := m.instances[a.ID].runtime.Engine
	if !balancequery.Configured(engine.Config.Get) || balancequery.Configured(m.instances[b.ID].runtime.Engine.Config.Get) {
		t.Fatal("configuration leaked across instances")
	}
	toolFound := false
	for _, tool := range engine.ModelTools() {
		if tool.Name == balancequery.ToolName {
			toolFound = true
			raw, _ := json.Marshal(tool)
			if strings.Contains(string(raw), key) || strings.Contains(string(raw), "api.deepseek.com") {
				t.Fatal("request material exposed in model schema")
			}
		}
	}
	if !toolFound {
		t.Fatal("configured query not advertised")
	}
	overview := call(a.ID, "GetOverview", map[string]any{}, http.StatusOK)
	var data struct {
		Tools []struct {
			Name                    string
			Configurable, Available bool
		}
	}
	if err := json.Unmarshal([]byte(overview), &data); err != nil {
		t.Fatal(err)
	}
	for _, tool := range data.Tools {
		if tool.Name == balancequery.ToolName && (!tool.Configurable || !tool.Available) {
			t.Fatal("query card is not configurable and available")
		}
	}
	call(a.ID, "SaveBalanceQueryConfig", map[string]any{"curl": balancequery.DefaultCurl}, http.StatusOK)
	cfg, _ := balancequery.Load(engine.Config.Get)
	if cfg.APIKey != key {
		t.Fatal("empty key field did not preserve existing secret")
	}
	call(a.ID, "SaveBalanceQueryConfig", map[string]any{"curl": balancequery.DefaultCurl + " -L"}, http.StatusBadRequest)
	call(a.ID, "GetBalanceQueryConfig", map[string]any{}, http.StatusOK)
	settings := rpc(t, m, a.ID, "SettingsService/ListEnvVars", "{}", false)
	if settings.Code != http.StatusOK || strings.Contains(settings.Body.String(), key) {
		t.Fatal("generic settings list exposed balance credentials")
	}
	call(a.ID, "SaveBalanceQueryConfig", map[string]any{"curl": balancequery.DefaultCurl, "clearApiKey": true}, http.StatusOK)
	if balancequery.Configured(engine.Config.Get) {
		t.Fatal("clear did not remove key")
	}
	for _, tool := range engine.ModelTools() {
		if tool.Name == balancequery.ToolName {
			t.Fatal("cleared query still advertised")
		}
	}
}
