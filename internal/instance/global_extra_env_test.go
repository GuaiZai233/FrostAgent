package instance

import (
	"FrostAgent/internal/backup"
	"FrostAgent/internal/envinject"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGlobalSettingsBackupRestoresExtraEnvAndKeepsLegacyImports(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	const key = "SYNTHETIC_BACKUP_PROVIDER_TOKEN"
	if err := envinject.Put(ctx, m.db, key, "exported-secret"); err != nil {
		t.Fatal(err)
	}
	export := httptest.NewRecorder()
	m.ServeHTTP(export, httptest.NewRequest(http.MethodGet, "/api/instances/global/backup/settings", nil))
	if export.Code != http.StatusOK {
		t.Fatalf("export: %d %s", export.Code, export.Body.String())
	}
	var data backup.GlobalSettings
	if err := json.Unmarshal(export.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.ExtraEnv == nil || (*data.ExtraEnv)[key] != "exported-secret" {
		t.Fatal("extra environment value was not exported")
	}
	if err := envinject.Put(ctx, m.db, key, "stale-secret"); err != nil {
		t.Fatal(err)
	}
	importSettings := func(settings backup.GlobalSettings) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/instances/global/import/settings", bytes.NewReader(body)))
		return response
	}
	if response := importSettings(data); response.Code != http.StatusOK {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	entries, err := envinject.List(ctx, m.db)
	if err != nil || len(entries) != 1 || entries[0].Value != "exported-secret" {
		t.Fatalf("extra environment was not restored: %#v %v", entries, err)
	}
	data.ExtraEnv = nil // Older global backups did not include the extra_env field.
	if err := envinject.Put(ctx, m.db, key, "current-secret"); err != nil {
		t.Fatal(err)
	}
	if response := importSettings(data); response.Code != http.StatusOK {
		t.Fatalf("legacy import: %d %s", response.Code, response.Body.String())
	}
	entries, err = envinject.List(ctx, m.db)
	if err != nil || len(entries) != 1 || entries[0].Value != "current-secret" {
		t.Fatalf("legacy import removed extra environment: %#v %v", entries, err)
	}
	if err := m.global.Update("LISTEN_ADDR", "127.0.0.1:19191", false); err != nil {
		t.Fatal(err)
	}
	invalid := map[string]string{"BOT_NAME": "unwanted"}
	data.ExtraEnv = &invalid
	if response := importSettings(data); response.Code == http.StatusOK {
		t.Fatal("managed setting was accepted as extra environment")
	}
	if m.global.Get("LISTEN_ADDR") != "127.0.0.1:19191" {
		t.Fatal("invalid extra environment changed global settings")
	}
}

func TestGlobalSettingsBackupRestoresExtraEnvOnApplyFailure(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	const key = "SYNTHETIC_ROLLBACK_PROVIDER_TOKEN"
	if err := envinject.Put(ctx, m.db, key, "before"); err != nil {
		t.Fatal(err)
	}
	extra := map[string]string{key: "after"}
	data := backup.ExportGlobalSettings(m.global)
	data.ExtraEnv = &extra
	data.Values["WS_ALLOWED_ORIGINS"] = "https://synthetic.example"
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.SetGlobalApplyHook(func() error {
		cancel()
		return errors.New("synthetic apply failure after request cancellation")
	})
	body, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/instances/global/import/settings", bytes.NewReader(body)).WithContext(requestCtx))
	if response.Code == http.StatusOK || m.global.Get("WS_ALLOWED_ORIGINS") != "" {
		t.Fatalf("failed import was not rolled back: %d %s", response.Code, response.Body.String())
	}
	entries, err := envinject.List(ctx, m.db)
	if err != nil || len(entries) != 1 || entries[0].Value != "before" {
		t.Fatalf("extra environment was not rolled back: %#v %v", entries, err)
	}
}
