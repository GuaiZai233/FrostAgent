package backup

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
)

func TestSettingsImportIsAtomicAndIgnoresUploadedSecrets(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "import.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const id = "test-instance"
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES (?, ?, ?)`, id, "Test", "now"); err != nil {
		t.Fatal(err)
	}
	settings, err := instanceconfig.OpenDatabase(db, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.Update("BOT_NAME", "Original", false); err != nil {
		t.Fatal(err)
	}
	if err := settings.Update("UPSTREAM_API_KEY", "kept-secret", false); err != nil {
		t.Fatal(err)
	}
	exported, err := ExportSettings(db, id)
	if err != nil {
		t.Fatal(err)
	}
	exported.Values["BOT_NAME"] = "Imported"
	exported.Values["UPSTREAM_API_KEY"] = "uploaded-secret"
	invalid := exported
	invalid.ModelRouter = modelrouter.Configuration{Version: 1,
		Endpoints: []modelrouter.Endpoint{{ID: "invalid"}}}
	if err := ImportSettings(db, id, invalid); err == nil {
		t.Fatal("invalid model configuration was accepted")
	}
	settings, err = instanceconfig.OpenDatabase(db, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.Get("BOT_NAME"); got != "Original" {
		t.Fatalf("failed import partially changed settings: %q", got)
	}
	if err := ImportSettings(db, id, exported); err != nil {
		t.Fatal(err)
	}
	settings, err = instanceconfig.OpenDatabase(db, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.Get("BOT_NAME"); got != "Imported" {
		t.Fatalf("non-secret setting was not imported: %q", got)
	}
	if got := settings.Get("UPSTREAM_API_KEY"); got != "kept-secret" {
		t.Fatalf("uploaded secret changed the database: %q", got)
	}
}
