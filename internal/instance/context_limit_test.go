package instance

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSQLMaxContextMessagesRejectsUnusableValues(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Context Limit")
	if err != nil {
		t.Fatal(err)
	}
	initialLimit := m.instances[info.ID].config.Get("MAX_CONTEXT_MESSAGES")
	for _, value := range []string{"1", "3", "invalid"} {
		request := httptest.NewRequest(http.MethodPost,
			"/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar",
			strings.NewReader(`{"key":"MAX_CONTEXT_MESSAGES","value":"`+value+`"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		m.ServeHTTP(response, request)
		if !strings.Contains(response.Body.String(), "保留消息条数必须是不小于 4 的整数") ||
			strings.Contains(response.Body.String(), `"success":true`) ||
			m.instances[info.ID].config.Get("MAX_CONTEXT_MESSAGES") != initialLimit {
			t.Fatalf("invalid message limit %q was accepted: %d %s", value, response.Code, response.Body.String())
		}
	}
}
