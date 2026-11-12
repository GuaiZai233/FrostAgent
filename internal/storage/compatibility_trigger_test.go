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

func TestPostgresMigrationSchemaCompatibilitySearchPathTriggers(t *testing.T) {
	for _, test := range []struct {
		name, table, effect                      string
		legacy, shadowed, outsidePath, noTrigger bool
	}{
		{name: "scope-row-blocks-delete", table: "memory_embedding_scopes", effect: "block"},
		{name: "scope-statement-mutates-module", table: "memory_embedding_scopes", effect: "mutate"},
		{name: "legacy-v15-no-backfill", table: "memory_embedding_scopes", effect: "block", legacy: true},
		{name: "core-counters", table: "counters", effect: "block"},
		{name: "metadata-target", table: "schema_meta", effect: "block"},
		{name: "shadowed-same-name", table: "memory_embedding_scopes", effect: "mutate", shadowed: true},
		{name: "unrelated-outside-search-path", table: "memory_embedding_scopes", effect: "block", shadowed: true, outsidePath: true},
		{name: "fallback-without-trigger", table: "memory_embedding_scopes", noTrigger: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, Postgres)
			compatibilitySeedComputerUse(t, db)
			seedCompatibilityEmbeddingCaches(t, db, false)
			// Remove the vector child so the separate cross-schema FK guard
			// cannot mask a missing trigger check on the scope target.
			compatibilityExec(t, db, `DROP TABLE memory_embeddings`)
			version := 17
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			if test.table == "counters" {
				compatibilityExec(t, db, `INSERT INTO counters VALUES ('synthetic-counter', 1)`)
			}
			var activeSchema string
			if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&activeSchema); err != nil {
				t.Fatal(err)
			}
			moduleSchema, moduleDB := compatibilityModuleSchema(t, config, activeSchema+`.trigger"Data`)
			moduleTable := compatibilityQualifiedTable(moduleSchema, test.table)
			if test.shadowed {
				compatibilityExec(t, db, `CREATE TABLE `+moduleTable+` AS TABLE `+test.table)
			} else {
				compatibilityExec(t, db, `ALTER TABLE `+test.table+` SET SCHEMA "`+strings.ReplaceAll(moduleSchema, `"`, `""`)+`"`)
			}
			payloadTable := compatibilityQualifiedTable(moduleSchema, "future_payload")
			compatibilityExec(t, db, `CREATE TABLE `+payloadTable+` (payload TEXT NOT NULL)`)
			compatibilityExec(t, db, `INSERT INTO `+payloadTable+` VALUES ('synthetic-preserved-payload')`)
			if !test.noTrigger {
				function := compatibilityQualifiedTable(moduleSchema, "future_trigger")
				body := `RAISE EXCEPTION 'synthetic trigger blocks old writer'; RETURN OLD;`
				level := "ROW"
				if test.effect == "mutate" {
					body = `UPDATE ` + payloadTable + ` SET payload = 'changed-by-trigger'; RETURN NULL;`
					level = "STATEMENT"
				}
				compatibilityExec(t, db, `CREATE FUNCTION `+function+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN `+body+` END; $$`)
				compatibilityExec(t, db, `CREATE TRIGGER future_delete BEFORE DELETE ON `+moduleTable+` FOR EACH `+level+` EXECUTE FUNCTION `+function+`() `)
			}
			before := compatibilityPostgresSchemaSnapshot(t, db.SQL)
			moduleBefore := compatibilityPostgresSchemaSnapshot(t, moduleDB)
			parsed, err := url.Parse(config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			searchPath := `"` + strings.ReplaceAll(activeSchema, `"`, `""`) + `"`
			if !test.outsidePath {
				searchPath += `,"` + strings.ReplaceAll(moduleSchema, `"`, `""`) + `"`
			}
			query.Set("search_path", searchPath)
			parsed.RawQuery = query.Encode()
			reopenConfig := Config{Backend: Postgres, DSN: parsed.String()}
			writer, err := sql.Open("pgx", reopenConfig.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if !test.shadowed && !test.noTrigger {
				// Probe an unqualified write to the resolved target. For scopes
				// this reproduces memory import's DELETE, not the full import API.
				proveCompatibilityTriggerEffect(t, writer, test.table, payloadTable, test.effect)
				if after := compatibilityPostgresSchemaSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("rolled-back trigger probe changed active schema")
				}
				if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
					t.Fatal("rolled-back trigger probe changed external schema")
				}
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, reopenConfig)
			if test.shadowed || test.noTrigger {
				if err != nil {
					t.Fatalf("unrelated trigger or safe fallback target was rejected: %v", err)
				}
				defer opened.Close()
				expectedVersion, expectedFloor := version, 14
				if test.legacy {
					expectedVersion, expectedFloor = SchemaVersion, SchemaVersion
				}
				assertCompatibilityVersions(t, opened, expectedVersion, expectedFloor)
				// A real core write and the import's scope-delete probe must not
				// fire a trigger on the external same-name relation.
				if err := opened.CreateInstance(context.Background(), compatibilityArbiterInstance(), 73); err != nil {
					t.Fatal(err)
				}
				tx, err := opened.SQL.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.Exec(`DELETE FROM memory_embedding_scopes WHERE instance_id = 'synthetic-instance'`); err != nil {
					t.Fatal(err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					opened.Close()
					t.Fatal("trigger on a search-path writer target was accepted")
				}
				if !strings.Contains(err.Error(), "trigger") {
					t.Fatalf("startup did not reject the unsafe trigger: %v", err)
				}
			}
			// Use the original search_path for stable catalog definitions and
			// include trigger enablement/function bodies in the schema snapshot.
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if !test.shadowed && !test.noTrigger {
				if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
					t.Fatal("rejected startup changed active schema or compatibility metadata")
				}
			}
			if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
				t.Fatal("startup or ordinary writes changed external data, constraints or triggers")
			}
		})
	}
}

func proveCompatibilityTriggerEffect(t *testing.T, db *sql.DB, table, payloadTable, effect string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM ` + table)
	if effect == "block" {
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "P0001" || !strings.Contains(pgError.Message, "synthetic trigger") {
			t.Fatalf("fixture did not block the old writer through a trigger: %v", err)
		}
	} else {
		if err != nil {
			t.Fatal(err)
		}
		var payload string
		if err := tx.QueryRow(`SELECT payload FROM ` + payloadTable).Scan(&payload); err != nil || payload != "changed-by-trigger" {
			t.Fatalf("fixture did not mutate module data: payload=%q err=%v", payload, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
