package instance

import (
	"FrostAgent/internal/backup"
	"FrostAgent/internal/memory"
	"os"
	"testing"
	"time"
)

func TestPostgresSchemaWriteReadAndRestoreSmoke(t *testing.T) {
	dsn := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	t.Setenv("FROSTAGENT_DB_DRIVER", "postgres")
	t.Setenv("FROSTAGENT_DB_DSN", dsn)
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Postgres Smoke Source")
	if err != nil || source.Error != "" {
		t.Fatalf("create source: %#v, %v", source, err)
	}
	if err := m.instances[source.ID].config.Update("BOT_NAME", "Postgres Smoke Bot", false); err != nil {
		t.Fatal(err)
	}
	if err := memory.NewSQLStore(m.db, source.ID).Save(memory.MemoryEntry{
		ID: "synthetic-pg-memory", Owner: "synthetic-user", Content: "Postgres smoke fact",
		Source: memory.Source("manual"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := m.RestoreInstanceZIP("Postgres Smoke Restored", archive)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Enabled || m.instances[restored.ID].config.Get("BOT_NAME") != "Postgres Smoke Bot" {
		t.Fatalf("restored Postgres instance state is wrong: %#v", restored)
	}
	result, err := backup.ExportMemories(m.db, restored.ID)
	if err != nil || len(result.PrivateEntries) != 1 || result.PrivateEntries[0].ID != "synthetic-pg-memory" {
		t.Fatalf("Postgres memory was not restored: %#v, %v", result.PrivateEntries, err)
	}
}
