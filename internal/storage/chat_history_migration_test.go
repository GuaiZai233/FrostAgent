package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestChatHistoryMigrationPreservesV16(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			root, cfg, db := compatibilityDatabase(t, backend)
			prepareOldSchema(t, db, 16)
			populateMigrationFixture(t, db)
			compatibilityClose(t, db)
			db = compatibilityReopen(t, root, cfg)
			defer db.Close()
			assertCompatibilityVersions(t, db, SchemaVersion, SchemaVersion)
			assertMigrationFixture(t, db)
			if err := db.validateFutureLayout(context.Background(), db.SQL); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatHistoryMigrationFailureIsAtomic(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			root, cfg, db := compatibilityDatabase(t, backend)
			prepareOldSchema(t, db, 16)
			populateMigrationFixture(t, db)
			compatibilityExec(t, db, "CREATE TABLE chat_history (sentinel TEXT)")
			compatibilityExec(t, db, "INSERT INTO chat_history VALUES ('preserve')")
			compatibilityClose(t, db)
			if opened, err := OpenWithConfig(context.Background(), root, cfg); err == nil {
				opened.Close()
				t.Fatal("conflicting table should fail migration")
			}
			// Inspect using a raw connection because normal startup must still reject it.
			driver, dsn := "pgx", cfg.DSN
			if backend == SQLite {
				driver, dsn = "sqlite", filepath.Join(root, "frostagent.db")
			}
			raw, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			var version int
			var value string
			if err := raw.QueryRow("SELECT version FROM schema_meta WHERE id=1").Scan(&version); err != nil || version != 16 {
				t.Fatalf("failed migration changed version %d: %v", version, err)
			}
			if err := raw.QueryRow("SELECT sentinel FROM chat_history").Scan(&value); err != nil || value != "preserve" {
				t.Fatalf("failed migration changed unrelated data %q %v", value, err)
			}
		})
	}
}
