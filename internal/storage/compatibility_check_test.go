package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSQLiteSchemaCompatibilityCheckConstraints(t *testing.T) {
	exerciseCheckConstraintCompatibility(t, SQLite)
}

func TestPostgresMigrationSchemaCompatibilityCheckConstraints(t *testing.T) {
	exerciseCheckConstraintCompatibility(t, Postgres)
}

func exerciseCheckConstraintCompatibility(t *testing.T, backend Backend) {
	t.Helper()
	ctx := context.Background()
	for _, test := range []struct {
		name, columnCheck, tableCheck string
		unsafe                        bool
	}{
		{name: "column-check", columnCheck: "CHECK (enabled = 1)", unsafe: true},
		{name: "named-table-check", tableCheck: `, CONSTRAINT synthetic_enabled_check CHECK (enabled = 1)`, unsafe: true},
		{name: "comment-separated-check", tableCheck: ", cHeCk /* synthetic constraint */\n(enabled = 1)", unsafe: true},
		{name: "quoted-name-literal-and-comments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			if backend == SQLite {
				// All referencing tables are empty, so this recreates only the
				// disposable parent while preserving their original foreign keys.
				compatibilityExec(t, db, `DROP TABLE instances`)
				compatibilityExec(t, db, `CREATE TABLE instances (
					id TEXT PRIMARY KEY, name TEXT NOT NULL CONSTRAINT "CHECK" UNIQUE, created_at TEXT NOT NULL,
					-- CHECK (enabled = 1) in a comment is not a constraint.
					enabled INTEGER NOT NULL DEFAULT 0 `+test.columnCheck+`, deleting INTEGER NOT NULL DEFAULT 0,
					error TEXT NOT NULL DEFAULT 'CHECK (enabled = 1) ''quoted text''',
					credential_targets_json TEXT NOT NULL DEFAULT '[]'
					/* CHECK (deleting = 1) */ `+test.tableCheck+`
				)`)
			} else {
				compatibilityExec(t, db, `ALTER TABLE instances ALTER COLUMN error SET DEFAULT 'CHECK (enabled = 1) ''quoted text'''`)
				if test.unsafe {
					clause := test.columnCheck
					if clause == "" {
						clause = strings.TrimPrefix(test.tableCheck, ", ")
					}
					compatibilityExec(t, db, `ALTER TABLE instances ADD `+clause)
				}
			}
			compatibilityExec(t, db, `INSERT INTO instances (id, name, created_at, enabled)
				VALUES ('synthetic-check-instance', 'Synthetic check', '2026-01-01T00:00:00Z', 1)`)
			compatibilityExec(t, db, `INSERT INTO settings (scope, instance_id, key, value)
				VALUES ('instance', 'synthetic-check-instance', 'synthetic-setting', 'preserve exactly')`)
			compatibilityMetadata(t, db, 16, true, 14)
			before := compatibilitySnapshot(t, db.SQL, backend)
			definition := compatibilityInstanceDefinition(t, db.SQL, backend)
			if test.unsafe {
				// Reproduce the review's ordinary-write failure independently of
				// startup validation; neither failed write may alter existing data.
				if err := db.CreateInstance(ctx, InstanceRecord{
					ID: "synthetic-new-instance", Name: "Synthetic new", CreatedAt: time.Now(),
				}, 2); err == nil || !strings.Contains(strings.ToLower(err.Error()), "check") {
					t.Fatalf("fixture did not reject disabled instance creation through CHECK: %v", err)
				}
				records, _, err := db.LoadInstances(ctx)
				if err != nil || len(records) != 1 {
					t.Fatalf("load fixture instance: records=%d err=%v", len(records), err)
				}
				records[0].Enabled = false
				if err := db.UpdateInstance(ctx, records[0]); err == nil || !strings.Contains(strings.ToLower(err.Error()), "check") {
					t.Fatalf("fixture did not reject disabled instance update through CHECK: %v", err)
				}
				if after := compatibilitySnapshot(t, db.SQL, backend); !reflect.DeepEqual(before, after) {
					t.Fatal("failed fixture writes changed database rows")
				}
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(ctx, root, config)
			if !test.unsafe {
				if err != nil {
					t.Fatalf("CHECK in quoted names, literals or comments was misidentified: %v", err)
				}
				defer opened.Close()
				assertCompatibilityVersions(t, opened, 16, 14)
				if after := compatibilitySnapshot(t, opened.SQL, backend); !reflect.DeepEqual(before, after) {
					t.Fatal("compatible startup changed metadata or rows")
				}
				if err := opened.CreateInstance(ctx, InstanceRecord{
					ID: "synthetic-new-instance", Name: "Synthetic new", CreatedAt: time.Now(),
				}, 2); err != nil {
					t.Fatalf("compatible ordinary write failed: %v", err)
				}
				return
			}
			if err == nil {
				opened.Close()
				t.Fatal("future core CHECK constraint was accepted")
			}
			if !strings.Contains(err.Error(), "instances") || !strings.Contains(strings.ToLower(err.Error()), "check") {
				t.Fatalf("startup did not identify the incompatible CHECK: %v", err)
			}
			driver, dsn := "sqlite", filepath.Join(root, "frostagent.db")
			if backend == Postgres {
				driver, dsn = "pgx", config.DSN
			}
			probe, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := compatibilitySnapshot(t, probe, backend); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected startup changed metadata or database rows")
			}
			if after := compatibilityInstanceDefinition(t, probe, backend); after != definition {
				t.Fatal("rejected startup changed the table constraints")
			}
		})
	}
}

func compatibilityInstanceDefinition(t *testing.T, db *sql.DB, backend Backend) string {
	t.Helper()
	query := `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'instances'`
	if backend == Postgres {
		query = `SELECT string_agg(pg_catalog.pg_get_constraintdef(oid), E'\n' ORDER BY conname)
			FROM pg_catalog.pg_constraint WHERE conrelid = pg_catalog.to_regclass('instances')`
	}
	var definition string
	if err := db.QueryRow(query).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	return definition
}
