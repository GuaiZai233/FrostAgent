package storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresMigrationSchemaCompatibilityCrossSchemaReferences(t *testing.T) {
	for _, test := range []struct {
		name, parent, child, action string
		legacy, unrelated, accept   bool
	}{
		{name: "scope-cascade", parent: "memory_embedding_scopes"},
		{name: "vector-cascade", parent: "memory_embeddings"},
		{name: "settings-cascade", parent: "settings"},
		{name: "same-name-native-cache", parent: "memory_embedding_scopes", child: "memory_embeddings"},
		{name: "same-name-core-table", parent: "memory_embedding_scopes", child: "settings"},
		{name: "legacy-v15-no-backfill", parent: "memory_embedding_scopes", legacy: true},
		{name: "scope-no-action", parent: "memory_embedding_scopes", action: "NO ACTION"},
		{name: "instance-only-reference", parent: "instances", accept: true},
		{name: "unrelated-same-name-parent", parent: "memory_embedding_scopes", unrelated: true, accept: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, Postgres)
			compatibilitySeedComputerUse(t, db)
			seedCompatibilityEmbeddingCaches(t, db, false)
			version := 16
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			var activeSchema string
			if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&activeSchema); err != nil {
				t.Fatal(err)
			}
			// Punctuation and quoting must not change which relation a foreign
			// key points to, or make the catalog scan miss the external table.
			moduleSchema, moduleDB := compatibilityModuleSchema(t, config, activeSchema+`.module"Data`)
			parent := compatibilityQualifiedTable(activeSchema, test.parent)
			columns := "instance_id,scope_type,platform,group_id,owner"
			definitions := "instance_id TEXT NOT NULL, scope_type TEXT NOT NULL, platform TEXT NOT NULL, group_id TEXT NOT NULL, owner TEXT NOT NULL"
			switch test.parent {
			case "memory_embeddings":
				columns += ",memory_id"
				definitions += ", memory_id TEXT NOT NULL"
			case "settings":
				columns = "scope,instance_id,key"
				definitions = "scope TEXT NOT NULL, instance_id TEXT NOT NULL, key TEXT NOT NULL"
			case "instances":
				columns = "id"
				definitions = "id TEXT NOT NULL"
			}
			if test.unrelated {
				unrelatedParent := compatibilityQualifiedTable(moduleSchema, test.parent)
				compatibilityExec(t, db, `CREATE TABLE `+unrelatedParent+` (`+definitions+`, PRIMARY KEY (`+columns+`))`)
				compatibilityExec(t, db, `INSERT INTO `+unrelatedParent+` SELECT `+columns+` FROM `+parent)
				parent = unrelatedParent
			}
			childName := test.child
			if childName == "" {
				childName = "future_extension"
			}
			child := compatibilityQualifiedTable(moduleSchema, childName)
			action := test.action
			if action == "" {
				action = "CASCADE"
			}
			compatibilityExec(t, db, `CREATE TABLE `+child+` (`+definitions+`, payload TEXT NOT NULL,
				FOREIGN KEY (`+columns+`) REFERENCES `+parent+` (`+columns+`) ON DELETE `+action+`)`)
			compatibilityExec(t, db, `INSERT INTO `+child+` SELECT `+columns+`, 'synthetic-unknown-module-data' FROM `+parent)
			before := compatibilityPostgresSchemaSnapshot(t, db.SQL)
			moduleBefore := compatibilityPostgresSchemaSnapshot(t, moduleDB)
			if !test.accept {
				proveCompatibilityCrossSchemaEffect(t, db, parent, child, action)
				if after := compatibilityPostgresSchemaSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("rolled-back fixture changed active-schema data or constraints")
				}
				if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
					t.Fatal("rolled-back fixture changed external-schema data or constraints")
				}
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, config)
			if test.accept {
				if err != nil {
					t.Fatalf("safe cross-schema relationship was rejected: %v", err)
				}
				defer opened.Close()
				assertCompatibilityVersions(t, opened, version, 14)
				if after := compatibilityPostgresSchemaSnapshot(t, opened.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("compatible startup changed active-schema data or constraints")
				}
				compatibilityUpdateInstance(t, opened)
				assertCompatibilityJSON(t, opened)
				if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
					t.Fatal("compatible startup or instance update changed external-schema data or constraints")
				}
				return
			}
			if err == nil {
				opened.Close()
				t.Fatal("cross-schema dependency on a mutable table was accepted")
			}
			if !strings.Contains(err.Error(), childName) || !strings.Contains(err.Error(), test.parent) {
				t.Fatalf("startup did not identify the unsafe cross-schema dependency: %v", err)
			}
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected startup changed active-schema data, constraints or compatibility metadata")
			}
			if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
				t.Fatal("rejected startup changed external-schema data or constraints")
			}
		})
	}
}

func TestPostgresMigrationSchemaCompatibilitySearchPathReference(t *testing.T) {
	root, config, db := compatibilityDatabase(t, Postgres)
	compatibilitySeedComputerUse(t, db)
	seedCompatibilityEmbeddingCaches(t, db, false)
	compatibilityMetadata(t, db, 16, true, 14)
	var activeSchema string
	if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&activeSchema); err != nil {
		t.Fatal(err)
	}
	moduleSchema, moduleDB := compatibilityModuleSchema(t, config, activeSchema+`.module"Data`)
	parent := compatibilityQualifiedTable(moduleSchema, "memory_embedding_scopes")
	child := compatibilityQualifiedTable(activeSchema, "future_extension")
	compatibilityExec(t, db, `ALTER TABLE `+compatibilityQualifiedTable(activeSchema, "memory_embedding_scopes")+
		` SET SCHEMA "`+strings.ReplaceAll(moduleSchema, `"`, `""`)+`"`)
	compatibilityExec(t, db, `CREATE TABLE `+child+` (
		instance_id TEXT NOT NULL, scope_type TEXT NOT NULL, platform TEXT NOT NULL,
		group_id TEXT NOT NULL, owner TEXT NOT NULL, payload TEXT NOT NULL,
		FOREIGN KEY (instance_id,scope_type,platform,group_id,owner)
			REFERENCES `+parent+` (instance_id,scope_type,platform,group_id,owner) ON DELETE CASCADE)`)
	compatibilityExec(t, db, `INSERT INTO `+child+`
		SELECT instance_id,scope_type,platform,group_id,owner, 'synthetic-search-path-data' FROM `+parent)
	before := compatibilityPostgresSchemaSnapshot(t, db.SQL)
	moduleBefore := compatibilityPostgresSchemaSnapshot(t, moduleDB)
	proveCompatibilityCrossSchemaEffect(t, db, parent, child, "CASCADE")
	if after := compatibilityPostgresSchemaSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
		t.Fatal("rolled-back search-path fixture changed active-schema data or constraints")
	}
	if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
		t.Fatal("rolled-back search-path fixture changed external-schema data or constraints")
	}
	compatibilityClose(t, db)

	// The v14 writer resolves scopes through its fallback search path, while
	// both dependent tables remain in current_schema(). Check relation identity
	// rather than assuming every dangerous dependency has an external child.
	parsed, err := url.Parse(config.DSN)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", `"`+strings.ReplaceAll(activeSchema, `"`, `""`)+`","`+strings.ReplaceAll(moduleSchema, `"`, `""`)+`"`)
	parsed.RawQuery = query.Encode()
	reopenedConfig := Config{Backend: Postgres, DSN: parsed.String()}
	opened, err := OpenWithConfig(context.Background(), root, reopenedConfig)
	if err == nil {
		opened.Close()
		t.Fatal("active-schema dependency on a mutable search-path target was accepted")
	}
	if !strings.Contains(err.Error(), "memory_embedding_scopes") ||
		(!strings.Contains(err.Error(), "future_extension") && !strings.Contains(err.Error(), "memory_embeddings")) {
		t.Fatalf("startup did not identify the unsafe search-path dependency: %v", err)
	}
	// Keep the original search path for snapshots so PostgreSQL renders
	// constraint definitions identically before and after the rejected startup.
	probe, err := sql.Open("pgx", config.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected startup changed active-schema data, constraints or compatibility metadata")
	}
	if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
		t.Fatal("rejected startup changed external-schema data or constraints")
	}
}

func compatibilityQualifiedTable(schema, table string) string {
	return `"` + strings.ReplaceAll(schema, `"`, `""`) + `"."` + strings.ReplaceAll(table, `"`, `""`) + `"`
}

func compatibilityModuleSchema(t *testing.T, config Config, schema string) (string, *sql.DB) {
	t.Helper()
	parsed, err := url.Parse(config.DSN)
	if err != nil {
		t.Fatal(err)
	}
	quoted := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
	query := parsed.Query()
	query.Set("search_path", quoted)
	parsed.RawQuery = query.Encode()
	probe, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Exec(`CREATE SCHEMA ` + quoted); err != nil {
		probe.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := probe.Exec(`DROP SCHEMA ` + quoted + ` CASCADE`); err != nil {
			t.Errorf("remove disposable module schema: %v", err)
		}
		probe.Close()
	})
	return schema, probe
}

func proveCompatibilityCrossSchemaEffect(t *testing.T, db *DB, parent, child, action string) {
	t.Helper()
	var before int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM ` + child).Scan(&before); err != nil || before == 0 {
		t.Fatalf("cross-schema fixture has no dependent rows: count=%d err=%v", before, err)
	}
	tx, err := db.SQL.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM ` + parent)
	if action == "NO ACTION" {
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "23503" {
			t.Fatalf("fixture did not block old writer through foreign key: %v", err)
		}
	} else {
		if err != nil {
			t.Fatal(err)
		}
		var after int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ` + child).Scan(&after); err != nil || after != 0 {
			t.Fatalf("fixture did not cascade-delete external data: count=%d err=%v", after, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func compatibilityPostgresSchemaSnapshot(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	snapshot := compatibilitySnapshot(t, db, Postgres)
	rows, err := db.Query(`SELECT r.relname, c.conname, pg_catalog.pg_get_constraintdef(c.oid)
		FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_class r ON r.oid = c.conrelid
		JOIN pg_catalog.pg_namespace n ON n.oid = r.relnamespace
		WHERE n.nspname = current_schema() ORDER BY r.relname, c.conname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, name, definition string
		if err := rows.Scan(&table, &name, &definition); err != nil {
			t.Fatal(err)
		}
		snapshot[table+":constraints"] = append(snapshot[table+":constraints"], name+":"+definition)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
