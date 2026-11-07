package memory

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresEmbeddingCompatibilityCacheWrites(t *testing.T) {
	for _, table := range []string{"memory_embedding_scopes", "memory_embeddings", "native-layout"} {
		t.Run(table, func(t *testing.T) {
			db, config := newEmbeddingPostgresDBWithConfig(t)
			ctx := context.Background()
			for _, statement := range []string{
				`INSERT INTO instances (id,name,created_at) VALUES ('synthetic-instance','Synthetic cache','2026-01-01T00:00:00Z')`,
				`CREATE TABLE future_module (id TEXT PRIMARY KEY, payload TEXT NOT NULL)`,
				`INSERT INTO future_module VALUES ('synthetic-module','preserve exactly')`,
			} {
				if _, err := db.SQL.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			store := NewSQLStore(db, "synthetic-instance")
			seedEmbeddingMemory(t, store, "synthetic-memory", "synthetic-owner", "original source memory")
			vectorType, err := db.EnsureEmbeddingStorage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			manager := &Embeddings{db: db, instanceID: "synthetic-instance", vectorType: vectorType}
			scope := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
			vectors := map[string]embeddedMemory{"synthetic-memory": {Hash: "synthetic-hash", Vector: []float32{1, 0}}}
			if err := manager.publish(ctx, scope, "synthetic-model", vectors, "synthetic-source-hash"); err != nil {
				t.Fatal(err)
			}
			if table != "native-layout" {
				for _, statement := range []string{
					`ALTER TABLE ` + table + ` ADD COLUMN future_required TEXT NOT NULL DEFAULT 'synthetic-preserved'`,
					`ALTER TABLE ` + table + ` ALTER COLUMN future_required DROP DEFAULT`,
				} {
					if _, err := db.SQL.Exec(statement); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := db.SQL.Exec(`UPDATE schema_meta SET version = 16, min_compatible_version = 14 WHERE id = 1`); err != nil {
				t.Fatal(err)
			}
			before := embeddingCompatibilitySnapshot(t, db.SQL)
			if table != "native-layout" {
				// Reproduce the real publication failure before testing startup.
				// The manifest DELETE and all preceding INSERTs must roll back.
				err := manager.publish(ctx, scope, "synthetic-replacement", vectors, "new-source-hash")
				var pgError *pgconn.PgError
				if !errors.As(err, &pgError) || pgError.Code != "23502" || pgError.TableName != table || pgError.ColumnName != "future_required" {
					t.Fatalf("real publication did not fail at required cache column: %v", err)
				}
				if after := embeddingCompatibilitySnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("failed publication changed source/module data, caches or metadata")
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			opened, err := storage.OpenWithConfig(ctx, t.TempDir(), config)
			if table == "native-layout" {
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
				if !opened.EmbeddingStorageReady.Load() {
					t.Fatal("native cache was not ready after compatible restart")
				}
				if after := embeddingCompatibilitySnapshot(t, opened.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("compatible restart changed data")
				}
				vectorType, err = opened.EnsureEmbeddingStorage(ctx)
				if err != nil {
					t.Fatal(err)
				}
				manager = &Embeddings{db: opened, instanceID: "synthetic-instance", vectorType: vectorType}
				if err := manager.publish(ctx, scope, "synthetic-model", vectors, "synthetic-source-hash"); err != nil {
					t.Fatalf("compatible cache publication failed: %v", err)
				}
				scores, err := manager.pgScores(ctx, scope, "synthetic-model", []float32{1, 0})
				if err != nil || len(scores) != 1 || scores[0].ID != "synthetic-memory" {
					t.Fatalf("compatible cache query failed: %v %v", scores, err)
				}
				if after := embeddingCompatibilitySnapshot(t, opened.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("normal compatible publication changed source/module data or schema")
				}
				return
			}
			if err == nil {
				opened.Close()
				t.Fatal("startup accepted a layout that breaks real embedding publication")
			}
			if !strings.Contains(err.Error(), table) {
				t.Fatalf("startup did not identify the incompatible cache table: %v", err)
			}
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := embeddingCompatibilitySnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected startup changed source/module data, caches or metadata")
			}
		})
	}
}

func embeddingCompatibilitySnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var snapshot []string
	for _, table := range []string{"instances", "memory_entries", "memory_tags", "memory_catalogs", "memory_merge_archives",
		"future_module", "schema_meta", "memory_embedding_scopes", "memory_embeddings"} {
		var rows string
		if err := db.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text, '[]') FROM ` + table + ` r`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, table+":"+rows)
	}
	for _, query := range []string{
		`SELECT jsonb_agg(jsonb_build_array(c.relname, a.attname, pg_catalog.format_type(a.atttypid,a.atttypmod),
			a.attnotnull, a.attgenerated, a.attidentity, pg_catalog.pg_get_expr(d.adbin,d.adrelid)) ORDER BY c.relname,a.attnum)::text
			FROM pg_catalog.pg_class c JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid
			LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
			WHERE c.oid IN ('memory_embedding_scopes'::regclass,'memory_embeddings'::regclass) AND a.attnum>0 AND NOT a.attisdropped`,
		`SELECT jsonb_agg(jsonb_build_array(conrelid::regclass::text,conname,pg_catalog.pg_get_constraintdef(oid)) ORDER BY conname)::text
			FROM pg_catalog.pg_constraint WHERE conrelid IN ('memory_embedding_scopes'::regclass,'memory_embeddings'::regclass)`,
	} {
		var value string
		if err := db.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, value)
	}
	return snapshot
}
