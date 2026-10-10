package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

func postgresMigrationConfig(t *testing.T) Config {
	t.Helper()
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset; CI postgres-migrations job supplies a real database")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("PostgreSQL test DSN must be a postgres URI: %v", err)
	}
	schema := fmt.Sprintf("fa_migration_%d", time.Now().UnixNano())
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("remove migration test schema: %v", err)
		}
		admin.Close()
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return Config{Backend: Postgres, DSN: parsed.String()}
}

func TestPostgresMigrationV7AndV8PreserveData(t *testing.T) {
	ctx := context.Background()
	for _, previous := range []int{7, 8} {
		t.Run(fmt.Sprint(previous), func(t *testing.T) {
			config := postgresMigrationConfig(t)
			db, err := OpenWithConfig(ctx, t.TempDir(), config)
			if err != nil {
				t.Fatal(err)
			}
			populateMigrationFixture(t, db)
			prepareOldSchema(t, db, previous)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			for start := range 2 {
				db, err = OpenWithConfig(ctx, t.TempDir(), config)
				if err != nil {
					t.Fatalf("startup %d: %v", start, err)
				}
				assertMigrationFixture(t, db)
				var version int
				if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil || version != SchemaVersion {
					t.Fatalf("schema version = %d, %v", version, err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPostgresMigrationFailureRetryAndFutureReject(t *testing.T) {
	ctx := context.Background()
	config := postgresMigrationConfig(t)
	db, err := OpenWithConfig(ctx, t.TempDir(), config)
	if err != nil {
		t.Fatal(err)
	}
	populateMigrationFixture(t, db)
	prepareOldSchema(t, db, 7)
	if err := db.applyMigration(ctx, migration{8, []string{
		`CREATE INDEX memory_source_message_idx ON memory_entries(instance_id, source_message_id)`,
		`THIS IS NOT SQL`,
	}}); err == nil {
		t.Fatal("broken PostgreSQL migration unexpectedly succeeded")
	}
	var version int
	if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil || version != 7 {
		t.Fatalf("failed PostgreSQL migration advanced version: %d, %v", version, err)
	}
	assertMigrationFixture(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenWithConfig(ctx, t.TempDir(), config)
	if err != nil {
		t.Fatalf("retry PostgreSQL migration: %v", err)
	}
	assertMigrationFixture(t, db)
	if _, err := db.SQL.Exec(db.Bind(`UPDATE schema_meta SET version = ? WHERE id = 1`), SchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if db, err = OpenWithConfig(ctx, t.TempDir(), config); err == nil {
		db.Close()
		t.Fatal("future PostgreSQL schema was loaded")
	}
	probe, err := sql.Open("pgx", config.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := probe.QueryRow(`SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil || version != SchemaVersion+1 {
		t.Fatalf("future PostgreSQL schema was modified: %d, %v", version, err)
	}
	var count int
	if err := probe.QueryRow(`SELECT COUNT(*) FROM instances WHERE id = 'synthetic-instance'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("future PostgreSQL data was modified: %d, %v", count, err)
	}
}
