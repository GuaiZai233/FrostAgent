package storage

import (
	"context"
	"database/sql"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestPostgresMigrationSchemaCompatibilityCacheLayout(t *testing.T) {
	for _, test := range []struct {
		name, table, change string
		legacy, accept      bool
	}{
		{name: "scopes-required-column", table: "memory_embedding_scopes", change: "required"},
		{name: "vectors-required-column", table: "memory_embeddings", change: "required"},
		{name: "scopes-wrong-type", table: "memory_embedding_scopes", change: "scope-type"},
		{name: "vectors-wrong-type", table: "memory_embeddings", change: "vector-type"},
		{name: "vectors-fixed-dimensions", table: "memory_embeddings", change: "typmod"},
		{name: "vectors-dimensions-text", table: "memory_embeddings", change: "dimensions-type"},
		{name: "scopes-missing-column", table: "memory_embedding_scopes", change: "missing"},
		{name: "vectors-missing-column", table: "memory_embeddings", change: "missing"},
		{name: "scopes-generated", table: "memory_embedding_scopes", change: "generated"},
		{name: "vectors-identity", table: "memory_embeddings", change: "identity"},
		{name: "scopes-check", table: "memory_embedding_scopes", change: "check"},
		{name: "vectors-unique", table: "memory_embeddings", change: "unique"},
		{name: "vectors-deferrable-key", table: "memory_embeddings", change: "deferrable"},
		{name: "vectors-exclusion", table: "memory_embeddings", change: "exclusion"},
		{name: "vectors-nullable", table: "memory_embeddings", change: "nullable"},
		{name: "vectors-without-scopes", table: "memory_embeddings", change: "no-scopes"},
		{name: "legacy-scopes-no-backfill", table: "memory_embedding_scopes", change: "required", legacy: true},
		{name: "legacy-vectors-no-backfill", table: "memory_embeddings", change: "required", legacy: true},
		{name: "fallback-scopes", table: "memory_embedding_scopes", change: "fallback"},
		{name: "shadowed-same-name", table: "memory_embedding_scopes", change: "shadowed", accept: true},
		{name: "native-layout", accept: true},
		{name: "scopes-only", change: "scopes-only", accept: true},
		{name: "no-caches", change: "no-caches", accept: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, Postgres)
			compatibilitySeedComputerUse(t, db)
			seedCompatibilityEmbeddingCaches(t, db, false)
			version := 17
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			openConfig := config
			var moduleDB *sql.DB
			switch test.change {
			case "required":
				compatibilityExec(t, db, `ALTER TABLE `+test.table+` ADD COLUMN future_required TEXT NOT NULL DEFAULT 'synthetic-preserved'`)
				compatibilityExec(t, db, `ALTER TABLE `+test.table+` ALTER COLUMN future_required DROP DEFAULT`)
			case "scope-type":
				compatibilityExec(t, db, `ALTER TABLE memory_embedding_scopes ALTER COLUMN fingerprint TYPE BYTEA USING convert_to(fingerprint, 'UTF8')`)
			case "vector-type":
				compatibilityExec(t, db, `ALTER TABLE memory_embeddings ALTER COLUMN embedding TYPE TEXT USING embedding::text`)
			case "typmod":
				var kind string
				if err := db.SQL.QueryRow(`SELECT pg_catalog.format_type(atttypid, 2) FROM pg_catalog.pg_attribute
					WHERE attrelid = 'memory_embeddings'::regclass AND attname = 'embedding'`).Scan(&kind); err != nil {
					t.Fatal(err)
				}
				compatibilityExec(t, db, `ALTER TABLE memory_embeddings ALTER COLUMN embedding TYPE `+kind)
			case "dimensions-type":
				compatibilityExec(t, db, `ALTER TABLE memory_embeddings ALTER COLUMN dimensions TYPE TEXT`)
			case "missing":
				column := "source_hash"
				if test.table == "memory_embeddings" {
					column = "embedding"
				}
				compatibilityExec(t, db, `ALTER TABLE `+test.table+` DROP COLUMN `+column)
			case "generated":
				compatibilityExec(t, db, `ALTER TABLE memory_embedding_scopes DROP COLUMN source_hash`)
				compatibilityExec(t, db, `ALTER TABLE memory_embedding_scopes ADD COLUMN source_hash TEXT GENERATED ALWAYS AS ('synthetic-source') STORED NOT NULL`)
			case "identity":
				compatibilityExec(t, db, `ALTER TABLE memory_embeddings ALTER COLUMN dimensions ADD GENERATED ALWAYS AS IDENTITY`)
			case "check":
				compatibilityExec(t, db, `ALTER TABLE memory_embedding_scopes ADD CONSTRAINT synthetic_check CHECK (fingerprint <> '')`)
			case "unique":
				compatibilityExec(t, db, `CREATE UNIQUE INDEX synthetic_unique ON memory_embeddings (dimensions)`)
			case "deferrable":
				compatibilityExec(t, db, `ALTER TABLE memory_embeddings DROP CONSTRAINT memory_embeddings_pkey`)
				compatibilityExec(t, db, `ALTER TABLE memory_embeddings ADD PRIMARY KEY (instance_id,scope_type,platform,group_id,owner,memory_id) DEFERRABLE`)
			case "exclusion":
				compatibilityExec(t, db, `ALTER TABLE memory_embeddings ADD CONSTRAINT synthetic_exclusion EXCLUDE USING gist (int4range(dimensions, dimensions, '[]') WITH &&)`)
			case "nullable":
				compatibilityExec(t, db, `ALTER TABLE memory_embeddings ALTER COLUMN content_hash DROP NOT NULL`)
			case "no-scopes":
				compatibilityExec(t, db, `DROP TABLE memory_embedding_scopes CASCADE`)
			case "scopes-only", "no-caches":
				compatibilityExec(t, db, `DROP TABLE memory_embeddings`)
				if test.change == "no-caches" {
					compatibilityExec(t, db, `DROP TABLE memory_embedding_scopes`)
				}
			case "fallback", "shadowed":
				var activeSchema string
				if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&activeSchema); err != nil {
					t.Fatal(err)
				}
				var moduleSchema string
				moduleSchema, moduleDB = compatibilityModuleSchema(t, config, activeSchema+`.cache"Data`)
				target := compatibilityQualifiedTable(moduleSchema, test.table)
				if test.change == "fallback" {
					compatibilityExec(t, db, `DROP TABLE memory_embeddings`)
					compatibilityExec(t, db, `ALTER TABLE memory_embedding_scopes SET SCHEMA "`+strings.ReplaceAll(moduleSchema, `"`, `""`)+`"`)
				} else {
					compatibilityExec(t, db, `CREATE TABLE `+target+` AS TABLE memory_embedding_scopes`)
				}
				compatibilityExec(t, db, `ALTER TABLE `+target+` ADD COLUMN future_required TEXT NOT NULL DEFAULT 'synthetic-preserved'`)
				compatibilityExec(t, db, `ALTER TABLE `+target+` ALTER COLUMN future_required DROP DEFAULT`)
				parsed, err := url.Parse(config.DSN)
				if err != nil {
					t.Fatal(err)
				}
				query := parsed.Query()
				query.Set("search_path", `"`+activeSchema+`","`+strings.ReplaceAll(moduleSchema, `"`, `""`)+`"`)
				parsed.RawQuery = query.Encode()
				openConfig.DSN = parsed.String()
			}
			before := compatibilityPostgresSchemaSnapshot(t, db.SQL)
			var moduleBefore map[string][]string
			if moduleDB != nil {
				moduleBefore = compatibilityPostgresSchemaSnapshot(t, moduleDB)
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, openConfig)
			if test.accept {
				if err != nil {
					t.Fatalf("safe optional cache layout was rejected: %v", err)
				}
				expectedVersion, expectedFloor := version, 14
				if test.legacy {
					expectedVersion, expectedFloor = SchemaVersion, SchemaVersion
				}
				assertCompatibilityVersions(t, opened, expectedVersion, expectedFloor)
				if ready := opened.EmbeddingStorageReady.Load(); ready != (test.change != "no-caches" && test.change != "scopes-only") {
					t.Fatalf("incorrect cache readiness: %v", ready)
				}
				compatibilityClose(t, opened)
			} else {
				if err == nil {
					opened.Close()
					t.Fatal("incompatible cache writer layout was accepted")
				}
				if !strings.Contains(err.Error(), test.table) {
					t.Fatalf("startup did not identify the incompatible cache: %v", err)
				}
			}
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("startup changed source memories, module data, cache layout or metadata")
			}
			if moduleDB != nil && !reflect.DeepEqual(moduleBefore, compatibilityPostgresSchemaSnapshot(t, moduleDB)) {
				t.Fatal("startup changed external module/cache data or schema")
			}
		})
	}
}
