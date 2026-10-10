package instance

import (
	"FrostAgent/internal/backup"
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/proactive"
	"FrostAgent/internal/security"
	"FrostAgent/internal/sticker"
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGlobalSecurityRestoreSerializesAccessMutation(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	principal, err := security.NewPrincipal("qq", "synthetic-user")
	if err != nil {
		t.Fatal(err)
	}
	loaded := make(chan struct{})
	release := make(chan struct{})
	m.security.Access.SetBeforeSaveHook(func() {
		close(loaded)
		<-release
	})
	mutationDone := make(chan error, 1)
	go func() { mutationDone <- m.security.Access.Lock(principal, "synthetic reason") }()
	select {
	case <-loaded:
	case <-time.After(3 * time.Second):
		t.Fatal("access mutation did not reach save boundary")
	}
	body, err := json.Marshal(backup.GlobalSecurity{FormatVersion: backup.FormatVersion})
	if err != nil {
		t.Fatal(err)
	}
	responseDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/instances/global/import/security", bytes.NewReader(body)))
		responseDone <- response
	}()
	select {
	case response := <-responseDone:
		t.Fatalf("restore crossed in-flight access mutation: %d %s", response.Code, response.Body.String())
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}
	response := <-responseDone
	if response.Code != http.StatusOK {
		t.Fatalf("restore failed: %d %s", response.Code, response.Body.String())
	}
	m.security.Access.SetBeforeSaveHook(nil)
	locked, _, err := m.security.Access.IsLocked(principal)
	if err != nil || locked {
		t.Fatalf("stale mutation overwrote restored security state: %t, %v", locked, err)
	}
}

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

func TestSQLCopyReplacesSettingsWithoutSecrets(t *testing.T) {
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
	target, err := m.Create("Target")
	if err != nil || target.Error != "" {
		t.Fatalf("create target: %#v, %v", target, err)
	}
	if err := m.instances[source.ID].config.Update("BOT_NAME", "Copied Bot", false); err != nil {
		t.Fatal(err)
	}
	if err := m.instances[source.ID].config.Update("UPSTREAM_API_KEY", "source-secret", false); err != nil {
		t.Fatal(err)
	}
	sourceSettings, err := backup.ExportSettings(m.db, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceSettings.ModelRouter.Endpoints = []modelrouter.Endpoint{{ID: "source-endpoint", DisplayName: "Source", BaseURL: "https://example.invalid/v1", Enabled: true}}
	sourceSettings.ModelRouter.Models = []modelrouter.Model{{ID: "source-model", DisplayName: "Source Model", EndpointID: "source-endpoint", UpstreamModel: "synthetic-model", Enabled: true}}
	if err := backup.ImportSettings(m.db, source.ID, sourceSettings); err != nil {
		t.Fatal(err)
	}
	if err := m.Copy(target.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if got := m.instances[target.ID].config.Get("BOT_NAME"); got != "Copied Bot" {
		t.Fatalf("non-secret setting was not copied: %q", got)
	}
	if got := m.instances[target.ID].config.Get("UPSTREAM_API_KEY"); got == "source-secret" {
		t.Fatal("source secret was copied")
	}
	copied, err := backup.ExportSettings(m.db, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied.ModelRouter.Endpoints) != 1 || copied.ModelRouter.Endpoints[0].ID == "source-endpoint" ||
		len(copied.ModelRouter.Models) != 1 || copied.ModelRouter.Models[0].EndpointID != copied.ModelRouter.Endpoints[0].ID {
		t.Fatalf("copied endpoint references were not remapped: %#v", copied.ModelRouter)
	}
	if err := m.ImportSettings(target.ID, copied); err != nil {
		t.Fatalf("reimporting target settings changed its own endpoint ID: %v", err)
	}
	reimported, err := backup.ExportSettings(m.db, target.ID)
	if err != nil || len(reimported.ModelRouter.Endpoints) != 1 ||
		reimported.ModelRouter.Endpoints[0].ID != copied.ModelRouter.Endpoints[0].ID {
		t.Fatalf("same-instance settings import did not preserve endpoint identity: %#v, %v", reimported.ModelRouter, err)
	}
}

func TestGlobalSettingsBackupImportIsRedactedAndAtomic(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.global.Update("LISTEN_ADDR", "127.0.0.1:9191", false); err != nil {
		t.Fatal(err)
	}
	if err := m.global.Update("ALCYONE_SERVICE_TOKEN", "synthetic-secret", false); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/instances/global/backup/settings", nil)
	response := httptest.NewRecorder()
	m.api(response, req)
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("synthetic-secret")) {
		t.Fatalf("unsafe global export: %d, %s", response.Code, response.Body.String())
	}
	var exported backup.GlobalSettings
	if err := json.Unmarshal(response.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	exported.Values["LISTEN_ADDR"] = "127.0.0.1:9292"
	if err := backup.ImportGlobalSettings(m.global, exported); err != nil {
		t.Fatal(err)
	}
	if m.global.Get("LISTEN_ADDR") != "127.0.0.1:9292" || m.global.Get("ALCYONE_SERVICE_TOKEN") != "synthetic-secret" {
		t.Fatal("global import lost a non-secret or overwrote a secret")
	}
	exported.Values["LISTEN_ADDR"] = "127.0.0.1:9393"
	exported.Values["UNCLASSIFIED"] = "value"
	if err := backup.ImportGlobalSettings(m.global, exported); err == nil {
		t.Fatal("invalid global key was accepted")
	}
	if m.global.Get("LISTEN_ADDR") != "127.0.0.1:9292" {
		t.Fatal("failed global import changed settings")
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

func TestFailedSQLDeletionRestoresStickerFilesAndPendingBackup(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	root := t.TempDir()
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.Create("Pending With Sticker")
	if err != nil || info.Error != "" {
		t.Fatalf("create: %#v, %v", info, err)
	}
	imageDir := filepath.Join(root, "instance_"+info.ID, "sticker")
	stickers, err := sticker.NewSQLStore(m.db, info.ID, imageDir)
	if err != nil {
		t.Fatal(err)
	}
	image := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := stickers.Add("synthetic-sticker", "image.png", image); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmDeletion(info.ID); err == nil {
		t.Fatal("closed database accepted deletion")
	}
	if got, err := os.ReadFile(filepath.Join(imageDir, "image.png")); err != nil || !bytes.Equal(got, image) {
		t.Fatalf("failed SQL deletion lost sticker bytes: %v", err)
	}
	m.Close()
	m, err = NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.Close() }()
	list, _ := m.List()
	if len(list) != 1 || !list[0].Deleting {
		t.Fatalf("pending deletion was not recovered: %#v", list)
	}
	archive, err := m.InstanceZIP(info.ID)
	if err != nil || len(archive) == 0 {
		t.Fatalf("pending backup was lost: %v", err)
	}
	if !bytes.Contains(archive, []byte("image.png")) {
		t.Fatal("pending backup omitted sticker image")
	}
	stage, err := m.createDeletionStage(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(m.dir(info.ID), filepath.Join(stage, "instance")); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(imageDir, "image.png")); err != nil || !bytes.Equal(got, image) {
		t.Fatalf("restart did not recover staged sticker bytes: %v", err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("recovered deletion stage remains: %v", err)
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

func TestSQLGlobalSettingWaitsForApplyAndRollsBackFailure(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Global Rollback")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.global.Update("SECURITY_GATEWAY_TIMEOUT", "12s", false); err != nil {
		t.Fatal(err)
	}
	applyCount := 0
	m.SetGlobalApplyHook(func() error {
		applyCount++
		if applyCount == 1 {
			return errors.New("synthetic listener failure")
		}
		return m.ApplyGlobalSettings()
	})
	request := httptest.NewRequest(http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar",
		strings.NewReader(`{"key":"WS_ALLOWED_ORIGINS","value":"https://trusted.example"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	m.ServeHTTP(response, request)
	if response.Code == http.StatusOK || strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("failed hot apply reported success: %d %s", response.Code, response.Body.String())
	}
	if applyCount != 2 || m.global.Get("WS_ALLOWED_ORIGINS") != "" || m.global.Get("SECURITY_GATEWAY_TIMEOUT") != "12s" {
		t.Fatalf("global setting was not restored after failed apply: calls=%d value=%q", applyCount, m.global.Get("WS_ALLOWED_ORIGINS"))
	}
	request = httptest.NewRequest(http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar",
		strings.NewReader(`{"key":"WS_ALLOWED_ORIGINS","value":"https://trusted.example"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	m.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) ||
		applyCount != 3 || m.global.Get("WS_ALLOWED_ORIGINS") != "https://trusted.example" {
		t.Fatalf("successful hot apply did not complete before response: %d %s calls=%d", response.Code, response.Body.String(), applyCount)
	}
	originRequest := httptest.NewRequest(http.MethodGet, "http://local.example/ws/frostagent", nil)
	originRequest.Header.Set("Origin", "https://trusted.example")
	if !m.instances[info.ID].runtime.Scope.CheckOrigin(originRequest) {
		t.Fatal("new WebSocket Origin setting did not reach running instance")
	}
}

func TestSQLGlobalSettingsImportReportsApplyFailure(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.global.Update("SECURITY_CLASSIFIER_TIMEOUT", "15s", false); err != nil {
		t.Fatal(err)
	}
	applyCount := 0
	m.SetGlobalApplyHook(func() error {
		applyCount++
		if applyCount == 1 {
			return errors.New("synthetic listener failure")
		}
		return m.ApplyGlobalSettings()
	})
	data := backup.ExportGlobalSettings(m.global)
	data.Values["WS_ALLOWED_ORIGINS"] = "https://trusted.example"
	body, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/instances/global/import/settings", bytes.NewReader(body)))
	if response.Code == http.StatusOK || strings.Contains(response.Body.String(), `"success":true`) ||
		applyCount != 2 || m.global.Get("WS_ALLOWED_ORIGINS") != "" || m.global.Get("SECURITY_CLASSIFIER_TIMEOUT") != "15s" {
		t.Fatalf("failed import was not rolled back: status=%d body=%s calls=%d", response.Code, response.Body.String(), applyCount)
	}
}

func TestSQLProactiveSettingsApplyAndExport(t *testing.T) {
	t.Setenv("PROACTIVE_REPLY_PROBABILITY", "1")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Proactive SQL")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, setting := range []struct{ key, value string }{
		{"PROACTIVE_REPLY_PROBABILITY", "0.25"},
		{"ENABLE_PROACTIVE_REPLY", "true"},
		{"PROACTIVE_REPLY_GROUP_WHITELIST", "synthetic-group"},
		{"ENABLE_PROACTIVE_REPLY_WHITELIST", "true"},
	} {
		body, _ := json.Marshal(map[string]string{"key": setting.key, "value": setting.value})
		request := httptest.NewRequest(http.MethodPost,
			"/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		m.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("update %s: %d %s", setting.key, response.Code, response.Body.String())
		}
	}
	if got := proactive.GetProbability(m.instances[info.ID].runtime.Scope.Getenv); got != 0.25 {
		t.Fatalf("running instance used process environment or stale proactive settings: %v", got)
	}
	if scope := m.instances[info.ID].runtime.Scope.Getenv; !proactive.IsGroupAllowed(scope, "synthetic-group", "qq") ||
		proactive.IsGroupAllowed(scope, "another-group", "qq") {
		t.Fatal("running instance did not apply proactive whitelist settings")
	}
	exported, err := backup.ExportSettings(m.db, info.ID)
	if err != nil || exported.Values["PROACTIVE_REPLY_PROBABILITY"] != "0.25" ||
		exported.Values["ENABLE_PROACTIVE_REPLY"] != "true" ||
		exported.Values["PROACTIVE_REPLY_GROUP_WHITELIST"] != "synthetic-group" ||
		exported.Values["ENABLE_PROACTIVE_REPLY_WHITELIST"] != "true" {
		t.Fatalf("proactive SQL settings were not exported: %#v, %v", exported.Values, err)
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
	previousSession := previousRuntime.Engine.SessionManager.GetOrCreate("group:synthetic-group")
	previousSession.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "before global change"})
	previousGroupStore, err := previousRuntime.Engine.GroupManager.GetGroupStore("synthetic-group")
	if err != nil {
		t.Fatal(err)
	}
	previousSession.SetGroupStore(previousGroupStore)
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
	currentRuntime := m.instances[info.ID].runtime
	if session := currentRuntime.Engine.SessionManager.GetOrCreate("group:synthetic-group"); session != previousSession || len(session.Messages()) != 1 || session.Scope() != currentRuntime.Scope {
		t.Fatal("global setting update lost or failed to rebind the active conversation")
	}
	groupStore, err := currentRuntime.Engine.GroupManager.GetGroupStore("synthetic-group")
	if err != nil || groupStore != previousGroupStore || previousSession.GroupStore() != groupStore {
		t.Fatalf("group store lock was not retained with the session: %v", err)
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
	previousSession := m.instances[info.ID].runtime.Engine.SessionManager.GetOrCreate("private:synthetic-user")
	previousSession.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "before instance change"})
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
	currentRuntime := m.instances[info.ID].runtime
	if session := currentRuntime.Engine.SessionManager.GetOrCreate("private:synthetic-user"); session != previousSession || len(session.Messages()) != 1 || session.Scope() != currentRuntime.Scope {
		t.Fatal("instance setting update lost or failed to rebind the active conversation")
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
