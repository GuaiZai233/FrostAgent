package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

func TestInstancePersistenceRestartBackupAndHotBudget(t *testing.T) {
	root := t.TempDir()
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	first, err := m.Create("Persistence fixture")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := m.Create("Persistence peer")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(first.ID, true); err != nil {
		t.Fatal(err)
	}
	current := m.instances[first.ID].runtime
	session := current.Engine.SessionManager.GetOrCreate("private:synthetic")
	session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "remember after restart"})
	session.SetLastPromptTrace("actual system", "fixture-model")
	uuid := m.instances[first.ID].logger.Error(logs.SYSTEM, "persistent diagnostic")
	lifecycleRPC(t, m, first.ID, "UpdateEnvVar", map[string]string{"key": logs.LogBudgetKey, "value": "1"})
	if m.instances[first.ID].runtime != current {
		t.Fatal("budget update restarted instance")
	}
	if got := m.instances[first.ID].logger.PersistenceStatus().BudgetBytes; got != 1<<20 {
		t.Fatalf("hot budget=%d", got)
	}
	if got := m.instances[peer.ID].logger.PersistenceStatus().BudgetBytes; got != logs.DefaultPersistentBytes {
		t.Fatal("budget leaked across instances")
	}
	archive, err := m.InstanceZIP(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := m.RestoreInstanceZIP("Restored checkpoint fixture", archive)
	if err != nil {
		t.Fatal(err)
	}
	copy := m.instances[restored.ID].runtime.Engine.SessionManager.GetOrCreate("private:synthetic")
	if messages := copy.Snapshot(); len(messages) != 1 || messages[0].Content != "remember after restart" {
		t.Fatal("ZIP lost checkpoint")
	}
	if system, _ := copy.LastPromptTrace(); system != "actual system" {
		t.Fatal("ZIP lost inspection")
	}
	if err := m.Enable(first.ID, false); err != nil {
		t.Fatal(err)
	}
	m.Close()
	next, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, ok := next.instances[first.ID].logger.FindError(uuid); !ok {
		t.Fatal("disabled restart lost log")
	}
	if got := next.instances[first.ID].logger.PersistenceStatus().BudgetBytes; got != 1<<20 {
		t.Fatal("restart lost configured budget")
	}
	if history := next.instances[first.ID].runtime.Engine.SessionManager.GetOrCreate("private:synthetic").Snapshot(); len(history) != 1 {
		t.Fatal("restart lost context")
	}
	request := localSettingsRequest(http.MethodGet, "/instances/"+first.ID+"/api/persistence", nil)
	response := httptest.NewRecorder()
	next.ServeHTTP(response, request)
	var health struct{ Logs logs.PersistenceStatus }
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil || response.Code != 200 || !health.Logs.Enabled {
		t.Fatalf("health: HTTP %d %v", response.Code, err)
	}
	staleSession := next.instances[first.ID].runtime.Engine.SessionManager.GetOrCreate("private:synthetic")
	staleLogger := next.instances[first.ID].logger
	if err := next.PrepareDeletion(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := next.ConfirmDeletion(first.ID); err != nil {
		t.Fatal(err)
	}
	staleSession.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "late callback"})
	staleLogger.Info(logs.SYSTEM, "late diagnostic")
	if _, err := os.Stat(next.dir(first.ID)); !os.IsNotExist(err) {
		t.Fatal("late writer recreated deleted instance directory")
	}
}
