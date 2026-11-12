package storage

import (
	"context"
	"database/sql"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestPostgresMigrationSchemaCompatibilityRewriteRules(t *testing.T) {
	for _, test := range []struct {
		name, table, effect                     string
		fallback, legacy, shadowed, outsidePath bool
		view                                    bool
	}{
		{name: "current-scope-also-delete", table: "memory_embedding_scopes", effect: "delete"},
		{name: "current-scope-instead-nothing", table: "memory_embedding_scopes", effect: "suppress"},
		{name: "fallback-scope-also-update", table: "memory_embedding_scopes", effect: "update", fallback: true},
		{name: "fallback-scope-instead-nothing", table: "memory_embedding_scopes", effect: "suppress", fallback: true},
		{name: "current-core-on-update", table: "counters", effect: "update"},
		{name: "fallback-metadata-on-update", table: "schema_meta", effect: "update", fallback: true},
		{name: "legacy-current-no-backfill", table: "memory_embedding_scopes", effect: "delete", legacy: true},
		{name: "legacy-fallback-no-backfill", table: "memory_embedding_scopes", effect: "update", legacy: true, fallback: true},
		{name: "shadowed-same-name", table: "memory_embedding_scopes", effect: "update", shadowed: true},
		{name: "unrelated-outside-search-path", table: "memory_embedding_scopes", effect: "delete", shadowed: true, outsidePath: true},
		{name: "ordinary-view-return-rule", view: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, Postgres)
			compatibilitySeedComputerUse(t, db)
			seedCompatibilityEmbeddingCaches(t, db, false)
			// Isolate rewrite rules from the existing cross-schema FK guard.
			compatibilityExec(t, db, `DROP TABLE memory_embeddings`)
			version := 17
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			compatibilityExec(t, db, `INSERT INTO counters VALUES ('synthetic-counter', 1)`)
			var activeSchema string
			if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&activeSchema); err != nil {
				t.Fatal(err)
			}
			moduleSchema, moduleDB := compatibilityModuleSchema(t, config, activeSchema+`.rule"Data`)
			payload := compatibilityQualifiedTable(activeSchema, "computer_use_tasks")
			write := `DELETE FROM memory_embedding_scopes WHERE instance_id = 'synthetic-instance'`
			if test.view {
				compatibilityExec(t, db, `CREATE VIEW compatibility_view AS SELECT generation FROM computer_use_tasks`)
				var rules int
				if err := db.SQL.QueryRow(`SELECT count(*) FROM pg_catalog.pg_rewrite
					WHERE ev_class = 'compatibility_view'::regclass AND rulename = '_RETURN'`).Scan(&rules); err != nil || rules != 1 {
					t.Fatalf("ordinary view must have its built-in return rule: count=%d err=%v", rules, err)
				}
			} else {
				target := compatibilityQualifiedTable(activeSchema, test.table)
				if test.fallback || test.shadowed {
					target = compatibilityQualifiedTable(moduleSchema, test.table)
					if test.shadowed {
						compatibilityExec(t, db, `CREATE TABLE `+target+` AS TABLE `+test.table)
					} else {
						compatibilityExec(t, db, `ALTER TABLE `+test.table+` SET SCHEMA "`+strings.ReplaceAll(moduleSchema, `"`, `""`)+`"`)
					}
				}
				event := "DELETE"
				if test.table == "counters" {
					event = "UPDATE"
					write = `UPDATE counters SET value = value + 1 WHERE key = 'synthetic-counter'`
				} else if test.table == "schema_meta" {
					event = "UPDATE"
					write = `UPDATE schema_meta SET version = version`
				}
				action := `ALSO DELETE FROM ` + payload
				if test.effect == "update" {
					action = `ALSO UPDATE ` + payload + ` SET metadata_json = 'changed-by-rule'`
				} else if test.effect == "suppress" {
					action = `INSTEAD NOTHING`
				}
				compatibilityExec(t, db, `CREATE RULE future_write AS ON `+event+` TO `+target+` DO `+action)
				var rules, triggers int
				if err := db.SQL.QueryRow(`SELECT
					(SELECT count(*) FROM pg_catalog.pg_rewrite WHERE ev_class = $1::regclass AND rulename = 'future_write'),
					(SELECT count(*) FROM pg_catalog.pg_trigger WHERE tgrelid = $1::regclass AND NOT tgisinternal)`, target).Scan(&rules, &triggers); err != nil || rules != 1 || triggers != 0 {
					t.Fatalf("fixture must have a rewrite rule without a user trigger: rules=%d triggers=%d err=%v", rules, triggers, err)
				}
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
			if !test.shadowed && !test.view {
				// Reproduce the rule effect through unqualified SQL in a rolled
				// back transaction. The scope DELETE is the memory import query;
				// this probe does not claim to execute the complete import API.
				proveCompatibilityRuleEffect(t, writer, write, test.effect)
				if after := compatibilityPostgresSchemaSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("rolled-back rule probe changed active schema")
				}
				if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
					t.Fatal("rolled-back rule probe changed external schema")
				}
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, reopenConfig)
			if test.shadowed || test.view {
				if err != nil {
					t.Fatalf("unrelated rule or ordinary view was rejected: %v", err)
				}
				defer opened.Close()
				expectedVersion, expectedFloor := version, 14
				if test.legacy {
					expectedVersion, expectedFloor = SchemaVersion, SchemaVersion
				}
				assertCompatibilityVersions(t, opened, expectedVersion, expectedFloor)
				tx, err := opened.SQL.Begin()
				if err != nil {
					opened.Close()
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.Exec(write); err != nil {
					t.Fatal(err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				compatibilityClose(t, opened)
			} else {
				if err == nil {
					opened.Close()
					t.Fatal("rewrite rule on a writer target was accepted")
				}
				if !strings.Contains(err.Error(), "rewrite rule") {
					t.Fatalf("startup did not identify the rewrite rule: %v", err)
				}
			}
			// Snapshot through the original paths so rendered definitions are
			// stable. This also proves legacy v15 did not gain a marker column.
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("startup changed active data, rules or compatibility metadata")
			}
			if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
				t.Fatal("startup changed external data or rules")
			}
		})
	}
}

func proveCompatibilityRuleEffect(t *testing.T, db *sql.DB, write, effect string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(write); err != nil {
		t.Fatal(err)
	}
	if effect == "suppress" || effect == "delete" {
		table, expected := "computer_use_tasks", 0
		if effect == "suppress" {
			table, expected = "memory_embedding_scopes", 1
		}
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != expected {
			t.Fatalf("rule did not produce expected %s effect: count=%d err=%v", effect, count, err)
		}
	} else {
		var payload string
		if err := tx.QueryRow(`SELECT metadata_json FROM computer_use_tasks`).Scan(&payload); err != nil || payload != "changed-by-rule" {
			t.Fatalf("rule did not mutate newer module data: payload=%q err=%v", payload, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
