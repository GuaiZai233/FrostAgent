package storage

import (
	"context"
	"testing"
)

func assertSkillsEmbeddingSchema(t *testing.T, db *DB) {
	t.Helper()
	for _, query := range []string{
		"SELECT COUNT(*) FROM skills",
		"SELECT COUNT(max_input_tokens) FROM provider_models",
		"SELECT summary_dirty FROM memory_entries WHERE id = 'memory-1'",
	} {
		var got int
		if err := db.SQL.QueryRow(query).Scan(&got); err != nil || got != 0 {
			t.Fatalf("migrated schema %s: got %d, error %v", query, got, err)
		}
	}
}

func TestSQLiteSkillsMigrationRollsBackAndRetries(t *testing.T) {
	exerciseSkillsMigrationRollback(t, SQLite)
}

func TestPostgresMigrationSkillsRollsBackAndRetries(t *testing.T) {
	exerciseSkillsMigrationRollback(t, Postgres)
}

func exerciseSkillsMigrationRollback(t *testing.T, backend Backend) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	config := Config{Backend: backend}
	if backend == Postgres {
		config = postgresMigrationConfig(t)
	}
	db, err := OpenWithConfig(ctx, root, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	prepareOldSchema(t, db, 13)
	populateMigrationFixture(t, db)

	var step migration
	for _, candidate := range migrations {
		if candidate.version == 14 {
			step = candidate
			step.statements = append([]string(nil), candidate.statements...)
			break
		}
	}
	if len(step.statements) == 0 {
		t.Fatal("Skills v14 migration missing")
	}
	step.statements = append(step.statements, "THIS IS NOT SQL")
	if err := db.applyMigration(ctx, step); err == nil {
		t.Fatal("broken Skills migration unexpectedly succeeded")
	}
	var version int
	if err := db.SQL.QueryRow("SELECT version FROM schema_meta WHERE id = 1").Scan(&version); err != nil || version != 13 {
		t.Fatalf("failed migration advanced schema: %d %v", version, err)
	}
	var count int
	if err := db.SQL.QueryRow("SELECT COUNT(*) FROM skills").Scan(&count); err == nil {
		t.Fatal("failed migration retained Skills table")
	}
	assertMigrationFixture(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		db, err = OpenWithConfig(ctx, root, config)
		if err != nil {
			t.Fatal(err)
		}
		assertMigrationFixture(t, db)
		assertSkillsEmbeddingSchema(t, db)
		if err := db.SQL.QueryRow("SELECT version FROM schema_meta WHERE id = 1").Scan(&version); err != nil || version != SchemaVersion {
			t.Fatalf("retry schema version: %d %v", version, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
