package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestToolHistoryMigrationPreservesV17(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			root, cfg, db := compatibilityDatabase(t, backend)
			prepareOldSchema(t, db, 17)
			populateMigrationFixture(t, db)
			compatibilityExec(t, db, `INSERT INTO chat_history VALUES ('synthetic-instance','fixture-message','qq','group','fixture-group','fixture-message','fixture-user','Fixture','user','original','original','',1,'received')`)
			compatibilityClose(t, db)
			db = compatibilityReopen(t, root, cfg)
			defer db.Close()
			assertCompatibilityVersions(t, db, SchemaVersion, SchemaVersion)
			assertMigrationFixture(t, db)
			var body string
			if err := db.SQL.QueryRow(`SELECT content FROM chat_history WHERE id='fixture-message'`).Scan(&body); err != nil || body != "original" {
				t.Fatalf("chat original changed: %q %v", body, err)
			}
			if err := db.validateFutureLayout(context.Background(), db.SQL); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestToolHistoryMigrationFailureIsAtomic(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			root, cfg, db := compatibilityDatabase(t, backend)
			prepareOldSchema(t, db, 17)
			compatibilityExec(t, db, `CREATE TABLE tool_history (sentinel TEXT)`)
			compatibilityExec(t, db, `INSERT INTO tool_history VALUES ('preserve')`)
			compatibilityClose(t, db)
			if reopened, err := OpenWithConfig(context.Background(), root, cfg); err == nil {
				reopened.Close()
				t.Fatal("conflicting table accepted")
			}
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
			if err := raw.QueryRow("SELECT version FROM schema_meta WHERE id=1").Scan(&version); err != nil || version != 17 {
				t.Fatalf("failed migration changed version %d: %v", version, err)
			}
			if err := raw.QueryRow("SELECT sentinel FROM tool_history").Scan(&value); err != nil || value != "preserve" {
				t.Fatalf("failed migration changed unrelated data %q %v", value, err)
			}
		})
	}
}
