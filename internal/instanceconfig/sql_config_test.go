package instanceconfig

import (
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLSettingsIgnoreEnvAndLegacyFile(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "settings.db"))
	t.Setenv("LISTEN_ADDR", "external-value")
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateInstance(context.Background(), storage.InstanceRecord{
		ID: "test-instance", Name: "Test", CreatedAt: time.Now(),
	}, 2); err != nil {
		t.Fatal(err)
	}
	if err := InitializeInstanceSettings(db, "test-instance"); err != nil {
		t.Fatal(err)
	}
	global, err := OpenDatabase(db, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := global.Get("LISTEN_ADDR"); got != "" {
		t.Fatalf("process environment overrode SQL: %q", got)
	}
	if err := global.Update("LISTEN_ADDR", "127.0.0.1:9999", false); err != nil {
		t.Fatal(err)
	}
	instance, err := OpenDatabase(db, "test-instance", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := instance.Get("BOT_NAME"); got == "" {
		t.Fatal("instance defaults missing")
	}
	if err := instance.Update("BOT_NAME", "Test Bot", false); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDatabase(db, "test-instance", false)
	if err != nil || reopened.Get("BOT_NAME") != "Test Bot" {
		t.Fatalf("SQL setting not recovered: %q, %v", reopened.Get("BOT_NAME"), err)
	}
	if _, err := reopened.Raw(); err == nil {
		t.Fatal("raw .env editor remained available")
	}
}
