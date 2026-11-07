package storage

import (
	"context"
	"strings"
	"testing"
)

func seedDuplicateProviders(t *testing.T, db *DB) {
	t.Helper()
	for _, owner := range []string{"synthetic-a", "synthetic-b"} {
		for _, item := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO instances(id,name,created_at) VALUES (?,?, 'now')`, []any{owner, owner}},
			{`INSERT INTO model_endpoints(instance_id,id,display_name,base_url,api_key_source,api_key_ref,enabled,position) VALUES (?, 'endpoint', 'Provider', ?, 'manual', 'endpoint/endpoint', 1, 0)`, []any{owner, "https://" + owner + ".invalid/v1"}},
			{`INSERT INTO model_secrets(instance_id,ref,secret) VALUES (?, 'endpoint/endpoint', ?)`, []any{owner, owner + "-secret"}},
			{`INSERT INTO models(instance_id,id,display_name,endpoint_id,upstream_model,enabled,capabilities,position) VALUES (?, 'model', 'Model', 'endpoint', ?, 1, '[]', 0)`, []any{owner, owner + "-model"}},
			{`INSERT INTO model_bindings(instance_id,platform,group_id,workload,mode,model_id) VALUES (?, '', '', 'dialogue', 'model', 'model')`, []any{owner}},
			{`INSERT INTO model_bindings(instance_id,platform,group_id,workload,mode,model_id) VALUES (?, 'qq', 'synthetic-group', 'vision', 'model', 'model')`, []any{owner}},
		} {
			if _, err := db.SQL.Exec(db.Bind(item.query), item.args...); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func assertMigratedProviders(t *testing.T, db *DB) {
	t.Helper()
	for _, owner := range []string{"synthetic-a", "synthetic-b"} {
		for _, workload := range []string{"dialogue", "vision"} {
			var url, upstream, secret string
			err := db.SQL.QueryRow(db.Bind(`SELECT e.base_url, m.upstream_model, s.secret
 FROM model_bindings b JOIN provider_models m ON m.id = b.model_id
 JOIN provider_endpoints e ON e.id = m.endpoint_id
 JOIN provider_secrets s ON s.ref = e.api_key_ref
 WHERE b.instance_id = ? AND b.workload = ?`), owner, workload).Scan(&url, &upstream, &secret)
			if err != nil || url != "https://"+owner+".invalid/v1" || upstream != owner+"-model" || secret != owner+"-secret" {
				t.Fatalf("migration crossed provider ownership for %s/%s: %v", owner, workload, err)
			}
		}
	}
	for _, table := range []string{"model_endpoints", "models", "model_secrets", "provider_endpoints", "provider_models", "provider_secrets"} {
		var count int
		if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 2 {
			t.Fatalf("migration did not preserve %s: %d, %v", table, count, err)
		}
	}
}

func TestProviderSchemaMigration(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			config := Config{Backend: backend}
			if backend == Postgres {
				config = postgresMigrationConfig(t)
			}
			root := t.TempDir()
			db, err := OpenWithConfig(context.Background(), root, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			prepareOldSchema(t, db, 10)
			seedDuplicateProviders(t, db)
			db.Close()
			for range 2 {
				db, err = OpenWithConfig(context.Background(), root, config)
				if err != nil {
					t.Fatal(err)
				}
				assertMigratedProviders(t, db)
				db.Close()
			}
		})
	}
}

func TestProviderMigrationFailureIsAtomic(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		for _, failure := range []string{"missing-model", "windows-collision"} {
			t.Run(string(backend)+"/"+failure, func(t *testing.T) {
				config := Config{Backend: backend}
				if backend == Postgres {
					config = postgresMigrationConfig(t)
				}
				db, err := OpenWithConfig(context.Background(), t.TempDir(), config)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				prepareOldSchema(t, db, 10)
				seedDuplicateProviders(t, db)
				statement := `UPDATE model_bindings SET model_id = 'missing'`
				if failure == "windows-collision" {
					statement = `UPDATE model_endpoints SET api_key_source = 'windows_credential_manager'`
				}
				if _, err := db.SQL.Exec(statement); err != nil {
					t.Fatal(err)
				}
				err = db.applyMigration(context.Background(), migration{version: 11, statements: providerSchema})
				if err == nil {
					t.Fatal("invalid source unexpectedly migrated")
				}
				if failure == "windows-collision" && !strings.Contains(err.Error(), "Windows credentials") {
					t.Fatal(err)
				}
				var version int
				if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil || version != 10 {
					t.Fatalf("version changed: %d, %v", version, err)
				}
				var count int
				if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM model_secrets`).Scan(&count); err != nil || count != 2 {
					t.Fatalf("source credentials changed: %d, %v", count, err)
				}
				if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM provider_endpoints`).Scan(&count); err == nil {
					t.Fatal("failed migration left destination tables")
				}
			})
		}
	}
}

func TestFreshSchemaHasOnlySharedProviderTables(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"model_endpoints", "model_secrets", "models", "endpoint_ids"} {
		var count int
		if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy table %s exists: %v", table, err)
		}
	}
}
