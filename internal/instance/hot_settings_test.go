package instance

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSQLHotSettingsKeepRunningRuntime(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Hot Runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	before := m.instances[info.ID].runtime
	update := func(key, value string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost,
			"/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar",
			strings.NewReader(`{"key":"`+key+`","value":"`+value+`"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		m.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("update %s failed: %d %s", key, response.Code, response.Body.String())
		}
		if m.instances[info.ID].runtime != before {
			t.Fatalf("update %s rebuilt the runtime", key)
		}
		if before.Scope.Context().Err() != nil {
			t.Fatalf("update %s stopped the runtime scope", key)
		}
	}
	update("BOT_NAME", "HotBot")
	if got := before.Scope.Getenv("BOT_NAME"); got != "HotBot" {
		t.Fatalf("runtime still sees old name %q", got)
	}
	update("ENABLE_QUOTE_TRACE", "false")
	update("QUOTE_TRACE_DEPTH", "3")
	update("BOT_LOOP_USER_IDS", "onebot:synthetic-bot")
	update("BOT_LOOP_MAX_TURNS", "2")
	update("BOT_LOOP_MODE", "hard")
	update("SANDBOX_ENABLED", "false")
	update("STICKER_ACTIVITY_MODE", "all_messages")
	update("SANDBOX_BASE_URL", "https://synthetic-sandbox.example")
	if got := m.sandbox.Get().BaseURL; got != "https://synthetic-sandbox.example" {
		t.Fatalf("sandbox manager still sees old endpoint %q", got)
	}
	update("MCP_CONTROL_TOKEN", "synthetic-control-token")
	if got := m.ControlPlaneGetenv()("MCP_CONTROL_TOKEN"); got != "synthetic-control-token" {
		t.Fatalf("control plane still sees old token %q", got)
	}
}

func TestSQLExtraEnvRouteRejectsManagedSettingKeys(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	request := localSettingsRequest(http.MethodPut, "/api/instances/global/extra-env",
		strings.NewReader(`{"key":"SYNTHETIC_EXTERNAL_TOKEN","value":"synthetic-secret"}`))
	response := httptest.NewRecorder()
	m.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("extra env write failed: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	m.ServeHTTP(response, localSettingsRequest(http.MethodGet, "/api/instances/global/extra-env", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "SYNTHETIC_EXTERNAL_TOKEN") {
		t.Fatalf("extra env entry unavailable: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	m.ServeHTTP(response, localSettingsRequest(http.MethodPut, "/api/instances/global/extra-env",
		strings.NewReader(`{"key":"BOT_NAME","value":"unexpected"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("managed setting key was accepted as extra env: %d", response.Code)
	}
	response = httptest.NewRecorder()
	m.ServeHTTP(response, localSettingsRequest(http.MethodPut, "/api/instances/global/extra-env",
		strings.NewReader(`{"key":"bot_name","value":"unexpected"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("case variant of managed key was accepted: %d", response.Code)
	}
	response = httptest.NewRecorder()
	m.ServeHTTP(response, localSettingsRequest(http.MethodDelete,
		"/api/instances/global/extra-env/SYNTHETIC_EXTERNAL_TOKEN", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("extra env delete failed: %d %s", response.Code, response.Body.String())
	}
}
