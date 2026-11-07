package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMigrationSchemaCompatibilityDefaults(t *testing.T) {
	testSchemaCompatibilityDefaults(t, SQLite)
}

func TestPostgresMigrationSchemaCompatibilityDefaults(t *testing.T) {
	testSchemaCompatibilityDefaults(t, Postgres)
}

func testSchemaCompatibilityDefaults(t *testing.T, backend Backend) {
	for _, test := range []struct {
		name, column, expression string
		legacy, accept           bool
	}{
		{name: "scene-null", column: "scene", expression: "NULL"},
		{name: "relation-null", column: "relation", expression: "NULL"},
		{name: "scene-missing", column: "scene"},
		{name: "relation-missing", column: "relation"},
		{name: "scene-nonempty", column: "scene", expression: "'synthetic-default'"},
		{name: "legacy-v15-no-backfill", column: "relation", expression: "NULL", legacy: true},
		{name: "native-defaults", accept: true},
		{name: "parenthesized-empty", column: "scene", expression: "(( '' ))", accept: true},
		{name: "explicitly-written-column", column: "error", expression: "'synthetic-unused-default'", accept: true},
		{name: "independent-extension", accept: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			compatibilitySeedComputerUse(t, db)
			version := 16
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			if test.name == "independent-extension" {
				compatibilityExec(t, db, `CREATE TABLE future_defaults (id TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT 'synthetic-default')`)
				compatibilityExec(t, db, `INSERT INTO future_defaults (id) VALUES ('synthetic-extension')`)
			}
			if test.column != "" {
				table := "dialogues"
				if test.column == "error" {
					table = "instances"
				}
				if backend == Postgres {
					change := "DROP DEFAULT"
					if test.expression != "" {
						change = "SET DEFAULT " + test.expression
					}
					compatibilityExec(t, db, `ALTER TABLE `+table+` ALTER COLUMN `+test.column+` `+change)
				} else {
					// Rebuild only this disposable table, preserving every row and
					// constraint. No application table references dialogues.
					var statement string
					if err := db.SQL.QueryRow(`SELECT sql FROM sqlite_master WHERE name = ?`, table).Scan(&statement); err != nil {
						t.Fatal(err)
					}
					old := test.column + " TEXT NOT NULL DEFAULT ''"
					replacement := test.column + " TEXT NOT NULL"
					if test.expression != "" {
						replacement += " DEFAULT " + test.expression
					}
					if strings.Count(statement, old) != 1 {
						t.Fatalf("fixture default not found: %s", statement)
					}
					statement = strings.Replace(statement, old, replacement, 1)
					// instances has inbound foreign keys, so mutate its unused
					// default in a disposable fixture with FK enforcement off.
					compatibilityExec(t, db, `PRAGMA foreign_keys = OFF`)
					compatibilityExec(t, db, strings.Replace(statement, "CREATE TABLE "+table, "CREATE TABLE compatibility_new_defaults", 1))
					compatibilityExec(t, db, `INSERT INTO compatibility_new_defaults SELECT * FROM `+table)
					compatibilityExec(t, db, `DROP TABLE `+table)
					compatibilityExec(t, db, `ALTER TABLE compatibility_new_defaults RENAME TO `+table)
					compatibilityExec(t, db, `PRAGMA foreign_keys = ON`)
				}
			}
			before := compatibilityDefaultsSnapshot(t, db.SQL, backend)
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, config)
			if test.accept {
				if err != nil {
					t.Fatalf("safe default layout was rejected: %v", err)
				}
				defer opened.Close()
				assertCompatibilityVersions(t, opened, version, 14)
				if after := compatibilityDefaultsSnapshot(t, opened.SQL, backend); !reflect.DeepEqual(before, after) {
					t.Fatal("compatible startup changed data, defaults or metadata")
				}
				compatibilityUpdateInstance(t, opened)
				assertCompatibilityJSON(t, opened)
				return
			}
			if err == nil {
				opened.Close()
				t.Fatal("incompatible omitted-column default was accepted")
			}
			if !strings.Contains(err.Error(), "dialogues") || !strings.Contains(err.Error(), test.column) || !strings.Contains(err.Error(), "default") {
				t.Fatalf("startup did not identify the incompatible default: %v", err)
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
			if after := compatibilityDefaultsSnapshot(t, probe, backend); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected startup changed data, defaults or compatibility metadata")
			}
		})
	}
}

func compatibilityDefaultsSnapshot(t *testing.T, db *sql.DB, backend Backend) map[string][]string {
	t.Helper()
	if backend == Postgres {
		return compatibilityPostgresSchemaSnapshot(t, db)
	}
	snapshot := compatibilitySnapshot(t, db, backend)
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, definition string
		if err := rows.Scan(&table, &definition); err != nil {
			t.Fatal(err)
		}
		snapshot[table+":definition"] = []string{definition}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
