package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSQLiteSchemaCompatibilityEmbeddingCaches(t *testing.T) {
	exerciseEmbeddingCacheCompatibility(t, SQLite)
}

func TestPostgresMigrationSchemaCompatibilityEmbeddingCaches(t *testing.T) {
	exerciseEmbeddingCacheCompatibility(t, Postgres)
}

func exerciseEmbeddingCacheCompatibility(t *testing.T, backend Backend) {
	t.Helper()
	for _, test := range []struct {
		name, parent  string
		version       int
		declared      bool
		alterCacheKey bool
	}{
		{name: "legacy-v15", version: 15},
		{name: "declared-v16", version: 16, declared: true},
		{name: "extension-references-scopes", parent: "memory_embedding_scopes", version: 16, declared: true},
		{name: "extension-references-vectors", parent: "memory_embeddings", version: 16, declared: true},
		{name: "uppercase-scope-reference", parent: "MEMORY_EMBEDDING_SCOPES", version: 16, declared: true},
		{name: "uppercase-vector-reference", parent: "MEMORY_EMBEDDINGS", version: 16, declared: true},
		{name: "altered-native-cache-key", version: 16, declared: true, alterCacheKey: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			compatibilitySeedComputerUse(t, db)
			seedCompatibilityEmbeddingCaches(t, db, test.alterCacheKey)
			compatibilityMetadata(t, db, test.version, test.declared, 14)
			if test.parent != "" {
				columns := "instance_id,scope_type,platform,group_id,owner"
				memoryColumn := ""
				if strings.EqualFold(test.parent, "memory_embeddings") {
					columns += ",memory_id"
					memoryColumn = "memory_id TEXT NOT NULL,"
				}
				compatibilityExec(t, db, `CREATE TABLE future_embedding_extension (
					instance_id TEXT NOT NULL, scope_type TEXT NOT NULL, platform TEXT NOT NULL,
					group_id TEXT NOT NULL, owner TEXT NOT NULL, `+memoryColumn+` payload TEXT NOT NULL,
					FOREIGN KEY (`+columns+`) REFERENCES `+test.parent+` (`+columns+`) ON DELETE CASCADE
				)`)
				compatibilityExec(t, db, `INSERT INTO future_embedding_extension
					SELECT `+columns+`, 'synthetic-unknown-module-data' FROM `+test.parent)

				// Prove this fixture has the reported cascade hazard, then roll it
				// back before testing that startup rejects it without any mutation.
				tx, err := db.SQL.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.Exec(`DELETE FROM ` + test.parent); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM future_embedding_extension`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("fixture did not reproduce extension cascade: count=%d err=%v", count, err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			before := compatibilitySnapshot(t, db.SQL, backend)
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, config)
			if test.parent == "" && !test.alterCacheKey {
				if err != nil {
					t.Fatalf("existing embedding-cache relationship was rejected: %v", err)
				}
				defer opened.Close()
				assertCompatibilityVersions(t, opened, test.version, 14)
				compatibilityUpdateInstance(t, opened)
				assertCompatibilityJSON(t, opened)
				after := compatibilitySnapshot(t, opened.SQL, backend)
				for _, table := range []string{"memory_embedding_scopes", "memory_embeddings"} {
					for _, suffix := range []string{":rows", ":columns"} {
						if !reflect.DeepEqual(before[table+suffix], after[table+suffix]) {
							t.Fatalf("compatible startup changed %s%s", table, suffix)
						}
					}
				}
				return
			}
			if err == nil {
				opened.Close()
				t.Fatal("future extension referencing mutable embedding cache was accepted")
			}
			unsafeTable := "future_embedding_extension"
			if test.alterCacheKey {
				unsafeTable = "memory_embeddings"
			}
			if !strings.Contains(err.Error(), unsafeTable) {
				t.Fatalf("startup did not identify the unsafe extension: %v", err)
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
				t.Fatal("rejected startup changed metadata, cache rows or extension data")
			}
		})
	}
}

func seedCompatibilityEmbeddingCaches(t *testing.T, db *DB, alterCacheKey bool) {
	t.Helper()
	// Match the native composite keys and foreign keys independently of the
	// guard. TEXT payloads let this relational regression also run on SQLite
	// and in the PostgreSQL migrations job before pgvector is enabled.
	for _, statement := range []string{
		`CREATE TABLE memory_embedding_scopes (
			instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
			scope_type TEXT NOT NULL, platform TEXT NOT NULL, group_id TEXT NOT NULL, owner TEXT NOT NULL,
			fingerprint TEXT NOT NULL, source_hash TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(instance_id,scope_type,platform,group_id,owner)
		)`,
		`CREATE TABLE memory_embeddings (
			instance_id TEXT NOT NULL, scope_type TEXT NOT NULL, platform TEXT NOT NULL,
			group_id TEXT NOT NULL, owner TEXT NOT NULL, memory_id TEXT NOT NULL,
			content_hash TEXT NOT NULL, dimensions INTEGER NOT NULL, embedding TEXT NOT NULL,
			PRIMARY KEY(instance_id,scope_type,platform,group_id,owner,memory_id),
			FOREIGN KEY(instance_id,scope_type,platform,group_id,owner)
				REFERENCES memory_embedding_scopes(instance_id,scope_type,platform,group_id,owner) ON DELETE CASCADE
		)`,
		`INSERT INTO memory_embedding_scopes VALUES ('synthetic-instance', 'private', '', '', 'synthetic-owner', 'synthetic-fingerprint', 'synthetic-source')`,
		`INSERT INTO memory_embeddings VALUES ('synthetic-instance', 'private', '', '', 'synthetic-owner', 'synthetic-memory', 'synthetic-hash', 2, '[0.1, 0.2]')`,
	} {
		if alterCacheKey && strings.HasPrefix(statement, "CREATE TABLE memory_embeddings (") {
			statement = strings.Replace(statement, "ON DELETE CASCADE", "ON DELETE CASCADE ON UPDATE CASCADE", 1)
		}
		compatibilityExec(t, db, statement)
	}
}
