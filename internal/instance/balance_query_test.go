package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
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
}

func TestBalanceQueryProviderReferenceSurvivesReopenAndUsesPublishedKey(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	var expectedKey atomic.Value
	expectedKey.Store("synthetic-initial-provider-key")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+expectedKey.Load().(string) {
			t.Error("runtime query did not read the published provider credential")
		}
		_, _ = w.Write([]byte(`{"balance":42}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	m, err := NewDatabase(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	info := create(t, m, "provider-balance")
	catalog := modelrouter.NewSQL(m.db, "")
	cfg := catalog.Draft()
	cfg.Endpoints = []modelrouter.Endpoint{{ID: "balance-provider", DisplayName: "Balance provider", BaseURL: server.URL, Enabled: true}}
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecret("balance-provider", expectedKey.Load().(string)); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"curl": "curl " + server.URL + " -H 'Authorization: Bearer {{API_KEY}}'", "providerId": "balance-provider"})
	response := rpc(t, m, info.ID, "BotStatusService/SaveBalanceQueryConfig", string(body), false)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), expectedKey.Load().(string)) {
		t.Fatalf("provider configuration failed or exposed credential: %d", response.Code)
	}
	m.Close()
	reopened, err := NewDatabase(dir)
	if err != nil {
		t.Fatal(err)
	}
	m = reopened
	engine := m.instances[info.ID].runtime.Engine
	stored, err := balancequery.Load(engine.Config.Get)
	if err != nil || stored.ProviderID != "balance-provider" || stored.APIKey != "" {
		t.Fatal("database reopen lost provider reference or copied its credential")
	}
	query := func() {
		t.Helper()
		result, err := engine.ToolRegistry[balancequery.ToolName].Execute("{}")
		if err != nil || result != `{"balance":42}` {
			t.Fatalf("runtime query failed: %q, %v", result, err)
		}
	}
	query()
	catalog = modelrouter.NewSQL(m.db, "")
	if _, err := catalog.SetDraftEndpointSecret("balance-provider", "synthetic-updated-provider-key"); err != nil {
		t.Fatal(err)
	}
	query()
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	expectedKey.Store("synthetic-updated-provider-key")
	query()
	cfg = catalog.Draft()
	cfg.Endpoints = nil
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	result, err := engine.ToolRegistry[balancequery.ToolName].Execute("{}")
	if result != "" || err == nil || strings.Contains(err.Error(), expectedKey.Load().(string)) || calls.Load() != before {
		t.Fatal("deleted provider did not fail before sending a request")
	}
	for _, tool := range engine.ModelTools() {
		if tool.Name == balancequery.ToolName {
			t.Fatal("deleted provider still advertised the balance tool")
		}
	}
}
