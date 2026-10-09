package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteSchemaVersionRebuildsBeforeV1(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "frostagent.db"))
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO instances(id, name, created_at, enabled) VALUES ('instance-a', 'A', 'now', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `UPDATE schema_meta SET version = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("incompatible pre-v1 data was retained: %d instances", count)
	}
	if err := db.SQL.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", count, SchemaVersion)
	}
}

func TestSQLiteRejectsMissingParentInstance(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "frostagent.db"))
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.SQL.Exec(`INSERT INTO memory_entries (
		instance_id, scope_type, platform, group_id, id, owner, content, source, created_at, updated_at
	) VALUES ('missing', 'private', '', '', 'm1', 'user', 'content', 'manual', 'now', 'now')`)
	if err == nil {
		t.Fatalf("memory without an instance was accepted: %v", err)
	}
}
