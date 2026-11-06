package instance

import (
	"encoding/json"
	"net/http"
	"testing"
)

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
}
