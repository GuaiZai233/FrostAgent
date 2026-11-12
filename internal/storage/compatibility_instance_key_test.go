package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSQLiteSchemaCompatibilityInstanceKeys(t *testing.T) {
	testCompatibilityInstanceKeys(t, SQLite)
}

func TestPostgresMigrationSchemaCompatibilityInstanceKeys(t *testing.T) {
	testCompatibilityInstanceKeys(t, Postgres)
}

func testCompatibilityInstanceKeys(t *testing.T, backend Backend) {
	for _, test := range []struct {
		name, key, action           string
		external, legacy, unrelated bool
	}{
		{name: "name-cascade", key: "name", action: "CASCADE"},
		{name: "name-no-action", key: "name", action: "NO ACTION"},
		{name: "name-restrict", key: "name", action: "RESTRICT"},
		{name: "name-set-null", key: "name", action: "SET NULL"},
		{name: "id-cascade", key: "id", action: "CASCADE"},
		{name: "id-no-action", key: "id", action: "NO ACTION"},
		{name: "uppercase-id", key: "ID", action: "CASCADE"},
		{name: "external-name-cascade", key: "name", action: "CASCADE", external: true},
		{name: "external-name-no-action", key: "name", action: "NO ACTION", external: true},
		{name: "external-name-set-null", key: "name", action: "SET NULL", external: true},
		{name: "external-id-cascade", key: "id", action: "CASCADE", external: true},
		{name: "external-id-no-action", key: "id", action: "NO ACTION", external: true},
		{name: "legacy-v15-no-backfill", key: "name", action: "CASCADE", external: true, legacy: true},
		{name: "unrelated-same-name-parent", key: "name", action: "CASCADE", external: true, unrelated: true},
	} {
		if test.external && backend != Postgres {
			continue
		}
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			compatibilitySeedComputerUse(t, db)
			version := 17
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			parent, child := "instances", "future_instance_reference"
			var moduleDB *sql.DB
			if test.external {
				var activeSchema string
				if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&activeSchema); err != nil {
					t.Fatal(err)
				}
				var moduleSchema string
				moduleSchema, moduleDB = compatibilityModuleSchema(t, config, activeSchema+`.instance"Module`)
				child = compatibilityQualifiedTable(moduleSchema, child)
				if test.unrelated {
					parent = compatibilityQualifiedTable(moduleSchema, "instances")
					compatibilityExec(t, db, `CREATE TABLE `+parent+` (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE)`)
					compatibilityExec(t, db, `INSERT INTO `+parent+` SELECT id, name FROM instances`)
				}
			}
			compatibilityExec(t, db, `CREATE TABLE `+child+` (instance_key TEXT, payload TEXT NOT NULL,
				FOREIGN KEY (instance_key) REFERENCES `+parent+` (`+test.key+`) ON UPDATE `+test.action+`)`)
			compatibilityExec(t, db, `INSERT INTO `+child+` SELECT `+test.key+`, 'synthetic-module-payload' FROM `+parent)
			before := compatibilityDefaultsSnapshot(t, db.SQL, backend)
			var moduleBefore map[string][]string
			if moduleDB != nil {
				moduleBefore = compatibilityPostgresSchemaSnapshot(t, moduleDB)
			}
			accept := strings.EqualFold(test.key, "id") || test.unrelated
			proveCompatibilityInstanceRename(t, db, child, test.action, accept)
			if after := compatibilityDefaultsSnapshot(t, db.SQL, backend); !reflect.DeepEqual(before, after) {
				t.Fatal("rename probe did not restore original data/metadata")
			}
			if moduleDB != nil && !reflect.DeepEqual(moduleBefore, compatibilityPostgresSchemaSnapshot(t, moduleDB)) {
				t.Fatal("rename probe did not restore external module data")
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, config)
			if accept {
				if err != nil {
					t.Fatalf("safe instance reference was rejected: %v", err)
				}
				expectedVersion, expectedFloor := version, 14
				if test.legacy {
					expectedVersion, expectedFloor = SchemaVersion, SchemaVersion
				}
				assertCompatibilityVersions(t, opened, expectedVersion, expectedFloor)
				proveCompatibilityInstanceRename(t, opened, child, test.action, true)
				compatibilityClose(t, opened)
			} else {
				if err == nil {
					opened.Close()
					t.Fatal("extension reference to mutable instance name was accepted")
				}
				if !strings.Contains(err.Error(), "future_instance_reference") || !strings.Contains(err.Error(), "instances") {
					t.Fatalf("startup did not identify the unsafe instance reference: %v", err)
				}
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
				t.Fatal("startup changed instances, module data, constraints or metadata")
			}
			if moduleDB != nil && !reflect.DeepEqual(moduleBefore, compatibilityPostgresSchemaSnapshot(t, moduleDB)) {
				t.Fatal("startup changed external module data or constraints")
			}
		})
	}
}

func proveCompatibilityInstanceRename(t *testing.T, db *DB, child, action string, safe bool) {
	t.Helper()
	ctx := context.Background()
	records, _, err := db.LoadInstances(ctx)
	if err != nil || len(records) != 1 {
		t.Fatalf("load rename fixture: %d records, %v", len(records), err)
	}
	original := records[0]
	renamed := original
	renamed.Name = "Synthetic renamed instance"
	var keyBefore sql.NullString
	if err := db.SQL.QueryRow(`SELECT instance_key FROM ` + child).Scan(&keyBefore); err != nil || !keyBefore.Valid {
		t.Fatalf("missing module reference before rename: %v %v", keyBefore, err)
	}
	err = db.UpdateInstance(ctx, renamed)
	if !safe && (action == "NO ACTION" || action == "RESTRICT") {
		if db.Backend == Postgres {
			var pgError *pgconn.PgError
			if !errors.As(err, &pgError) || pgError.Code != "23503" {
				t.Fatalf("rename did not fail with foreign key violation: %v", err)
			}
		} else if err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
			t.Fatalf("rename did not fail with foreign key violation: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("real instance rename failed: %v", err)
	}
	var after sql.NullString
	if err := db.SQL.QueryRow(`SELECT instance_key FROM ` + child).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if safe {
		if after != keyBefore {
			t.Fatalf("rename changed safe reference: %v -> %v", keyBefore, after)
		}
	} else if action == "CASCADE" {
		if !after.Valid || after.String != renamed.Name {
			t.Fatalf("rename did not cascade into module reference: %v", after)
		}
	} else if action == "SET NULL" && after.Valid {
		t.Fatalf("rename did not clear module reference: %v", after)
	}
	// Restore the synthetic probe with the same writer before snapshotting
	// rejected startup. SET NULL needs its module reference restored explicitly.
	if err := db.UpdateInstance(ctx, original); err != nil {
		t.Fatal(err)
	}
	if !safe && action == "SET NULL" {
		compatibilityExec(t, db, `UPDATE `+child+` SET instance_key = ?`, keyBefore.String)
	}
}
