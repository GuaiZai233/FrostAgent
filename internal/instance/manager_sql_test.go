package instance

import (
	"FrostAgent/internal/backup"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/sticker"
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatabaseManagerIgnoresLegacyFilesAndRecoversInstance(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "instances.json"), []byte(`{"version":1,"next_number":9,"instances":[{"id":"deadbeef","name":"Legacy"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := m.List(); len(entries) != 0 {
		t.Fatalf("legacy JSON instances were loaded: %#v", entries)
	}
	info, err := m.Create("SQL Test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Error != "" {
		t.Fatalf("new database instance failed to initialize: %s", info.Error)
	}
	if err := m.instances[info.ID].config.Update("BOT_NAME", "Database Bot", false); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	entries, next := m.List()
	if len(entries) != 1 || entries[0].ID != info.ID || next != 2 {
		t.Fatalf("SQL instance registry was not recovered: %#v, %d", entries, next)
	}
	if got := m.instances[info.ID].config.Get("BOT_NAME"); got != "Database Bot" {
		t.Fatalf("SQL setting was not recovered: %q", got)
	}
	for _, name := range []string{".env", "brain.json", "model_router.json", "mcp_servers.json", "dialogue.yml"} {
		if _, err := os.Stat(filepath.Join(root, "instance_"+info.ID, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy runtime file %s was created: %v", name, err)
		}
	}
}

func TestPendingDeletionSurvivesRestartAndRemovesAllInstanceData(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	root := t.TempDir()
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.Create("To Delete")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	if err := m.instances[info.ID].config.Update("BOT_NAME", "Delete Me", false); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != ErrDeleting {
		t.Fatalf("pending instance was enabled: %v", err)
	}
	m.Close()

	m, err = NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	list, _ := m.List()
	if len(list) != 1 || !list[0].Deleting || list[0].Enabled {
		t.Fatalf("pending deletion was not recovered: %#v", list)
	}
	archive, err := m.InstanceZIP(info.ID)
	if err != nil || len(archive) == 0 {
		t.Fatalf("backup unavailable during pending deletion: %v", err)
	}
	if err := m.ConfirmDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = m.List()
	if len(list) != 0 {
		t.Fatalf("deleted instance remains in registry: %#v", list)
	}
	var count int
	if err := m.db.SQL.QueryRow(`SELECT COUNT(*) FROM settings WHERE instance_id = ?`, info.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("instance settings remain: %d, %v", count, err)
	}
	if _, err := os.Stat(filepath.Join(root, "instance_"+info.ID)); !os.IsNotExist(err) {
		t.Fatalf("instance directory remains: %v", err)
	}
}

func TestPendingDeletionCanBeCancelled(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Keep")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	if err := m.PrepareDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := m.List()
	if len(list) != 1 || list[0].Deleting {
		t.Fatalf("cancelled deletion remains pending: %#v", list)
	}
	if m.instances[info.ID].runtime == nil {
		t.Fatal("management runtime was not restored")
	}
}

func TestSQLInstanceSettingAppliesWithoutManualRestart(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Hot Settings")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar",
		strings.NewReader(`{"key":"AGENT_MAX_ITERATIONS","value":"60"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	m.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("setting update failed: %d %s", response.Code, response.Body.String())
	}
	if got := m.instances[info.ID].runtime.Engine.EffectiveMaxIterations(); got != 60 {
		t.Fatalf("setting was not applied to running instance: got %d", got)
	}
	list, _ := m.List()
	if len(list) != 1 || list[0].RestartRequired {
		t.Fatalf("SQL setting still requests manual restart: %#v", list)
	}
}

func TestSQLQuickBackupHTTP(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Quick Backup")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	for _, part := range []struct{ path, mediaType string }{
		{"settings", "application/json"},
		{"memories", "application/json"},
		{"summaries", "application/json"},
		{"stickers", "application/zip"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/instances/"+info.ID+"/backup/"+part.path, nil)
		response := httptest.NewRecorder()
		m.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), part.mediaType) || response.Body.Len() == 0 {
			t.Fatalf("quick backup %s: code=%d type=%s body=%s", part.path, response.Code, response.Header().Get("Content-Type"), response.Body.String())
		}
	}
}

func TestSQLGlobalSettingsRefreshSharedDependencies(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Global Hot")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	previousRuntime := m.instances[info.ID].runtime
	previousBilling := m.billing.Load()
	if err := m.global.Update("ALCYONE_TIMEOUT", "8s", false); err != nil {
		t.Fatal(err)
	}
	if err := m.global.Update("MCP_CONTROL_TOKEN", "synthetic-token", false); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyGlobalSettings(); err != nil {
		t.Fatal(err)
	}
	if m.billing.Load() == previousBilling || m.instances[info.ID].runtime == previousRuntime {
		t.Fatal("global dependencies or running instance were not refreshed")
	}
	if got := m.ControlPlaneGetenv()("MCP_CONTROL_TOKEN"); got != "synthetic-token" {
		t.Fatal("control-plane token did not change immediately")
	}
}

func TestSQLSettingsImportRebuildsActiveInstance(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Import Settings")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	settings, err := backup.ExportSettings(m.db, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Values["AGENT_MAX_ITERATIONS"] = "44"
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/instances/"+info.ID+"/import/settings", bytes.NewReader(data))
	response := httptest.NewRecorder()
	m.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("settings import failed: %d %s", response.Code, response.Body.String())
	}
	if got := m.instances[info.ID].runtime.Engine.EffectiveMaxIterations(); got != 44 {
		t.Fatalf("imported setting was not applied: %d", got)
	}
}

func TestSQLMemoryImportMergesThroughHTTP(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Memory Import")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data := backup.Memories{FormatVersion: backup.FormatVersion,
		PrivateEntries: []memory.MemoryEntry{{ID: "synthetic-memory", Owner: "synthetic-user",
			Content: "synthetic fact", Source: memory.Source("manual"), CreatedAt: now, UpdatedAt: now}}}
	for run := 0; run < 2; run++ {
		body, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/instances/"+info.ID+"/import/memories", bytes.NewReader(body))
		response := httptest.NewRecorder()
		m.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("memory import %d failed: %d %s", run, response.Code, response.Body.String())
		}
		if run == 1 && !strings.Contains(response.Body.String(), `"skipped":1`) {
			t.Fatalf("duplicate memory ID was not skipped: %s", response.Body.String())
		}
	}
	exported, err := backup.ExportMemories(m.db, info.ID)
	if err != nil || len(exported.PrivateEntries) != 1 {
		t.Fatalf("memory merge did not persist exactly one entry: %#v, %v", exported, err)
	}
}

func TestSQLFullZIPRestoresNewDisabledInstance(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	root := t.TempDir()
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Source")
	if err != nil || source.Error != "" {
		t.Fatalf("create source: %#v, %v", source, err)
	}
	if err := m.instances[source.ID].config.Update("BOT_NAME", "Restorable Bot", false); err != nil {
		t.Fatal(err)
	}
	if err := m.instances[source.ID].config.Update("UPSTREAM_API_KEY", "synthetic-secret", false); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := memory.NewSQLStore(m.db, source.ID).Save(memory.MemoryEntry{
		ID: "synthetic-memory", Owner: "synthetic-user", Content: "remember this",
		Source: memory.Source("manual"), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	summaries, err := groupsummary.NewSQLStore(m.db, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := summaries.Upsert("group:123", "synthetic summary", 0); err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(root, "instance_"+source.ID, "sticker")
	stickers, err := sticker.NewSQLStore(m.db, source.ID, imageDir)
	if err != nil {
		t.Fatal(err)
	}
	image := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := stickers.Add("synthetic-sticker", "picture.png", image); err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := m.RestoreInstanceZIP("Restored", archive)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID == source.ID || restored.Enabled || restored.Name != "Restored" {
		t.Fatalf("restore target identity/state is wrong: %#v", restored)
	}
	if got := m.instances[restored.ID].config.Get("BOT_NAME"); got != "Restorable Bot" {
		t.Fatalf("setting not restored: %q", got)
	}
	if got := m.instances[restored.ID].config.Get("UPSTREAM_API_KEY"); got != "" {
		t.Fatalf("secret was copied into restored instance: %q", got)
	}
	memories, err := backup.ExportMemories(m.db, restored.ID)
	if err != nil || len(memories.PrivateEntries) != 1 {
		t.Fatalf("memory not restored: %#v, %v", memories, err)
	}
	groupSummaries, err := backup.ExportSummaries(m.db, restored.ID)
	if err != nil || len(groupSummaries.Records) != 1 {
		t.Fatalf("summary not restored: %#v, %v", groupSummaries, err)
	}
	storedImage, err := os.ReadFile(filepath.Join(root, "instance_"+restored.ID, "sticker", "picture.png"))
	if err != nil || !bytes.Equal(storedImage, image) {
		t.Fatalf("sticker image not restored: %v", err)
	}
}

func TestSQLFullZIPFailureRemovesNewInstance(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Source")
	if err != nil || source.Error != "" {
		t.Fatalf("create source: %#v, %v", source, err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var tampered bytes.Buffer
	writer := zip.NewWriter(&tampered)
	for _, member := range reader.File {
		stream, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if member.Name == "setting.json" {
			var settings backup.Settings
			if err := json.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			settings.ModelRouter.Version = 99
			data, err = json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
		}
		out, err := writer.Create(member.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RestoreInstanceZIP("Failed Restore", tampered.Bytes()); err == nil {
		t.Fatal("invalid settings were restored")
	}
	list, _ := m.List()
	if len(list) != 1 || list[0].ID != source.ID {
		t.Fatalf("failed restore left a partial instance: %#v", list)
	}
}
