package storage

import (
	"context"
	"fmt"
	"testing"
)

// The Skills and Embedding development branches both used schema v12.
// Reproduce either layout, including partially repaired databases, using only
// disposable databases. Existing feature data must survive the repair.
func prepareEmbeddingV12(t *testing.T, db *DB, modelColumn, memoryColumn bool) {
	t.Helper()
	populateMigrationFixture(t, db)
	for _, statement := range []string{
		`INSERT INTO provider_endpoints(id,display_name,base_url,api_key_source,enabled,position) VALUES ('synthetic-provider','Synthetic','https://model.invalid/v1','manual',1,0)`,
		`INSERT INTO provider_models(id,display_name,endpoint_id,upstream_model,enabled,capabilities,position) VALUES ('synthetic-model','Synthetic','synthetic-provider','synthetic-model',1,'[]',0)`,
		`CREATE TABLE skills (instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		name TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 0, info_json TEXT NOT NULL,
		body TEXT NOT NULL, package_json TEXT NOT NULL, size_bytes INTEGER NOT NULL,
		PRIMARY KEY (instance_id,name))`,
		`INSERT INTO skills VALUES ('synthetic-instance','synthetic-skill',1,'{}','preserved body','{}',14)`,
		`UPDATE provider_models SET max_input_tokens=4096`,
		`UPDATE memory_entries SET summary_dirty=1`,
		`UPDATE schema_meta SET version=12 WHERE id=1`,
	} {
		if _, err := db.SQL.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if !modelColumn {
		if _, err := db.SQL.Exec(`ALTER TABLE provider_models DROP COLUMN max_input_tokens`); err != nil {
			t.Fatal(err)
		}
	}
	if !memoryColumn {
		if _, err := db.SQL.Exec(`ALTER TABLE memory_entries DROP COLUMN summary_dirty`); err != nil {
			t.Fatal(err)
		}
	}
}

func assertEmbeddingV12Repair(t *testing.T, db *DB, modelColumn, memoryColumn bool) {
	t.Helper()
	assertMigrationFixture(t, db)
	modelWant, memoryWant := 0, 0
	if modelColumn {
		modelWant = 4096
	}
	if memoryColumn {
		memoryWant = 1
	}
	for _, item := range []struct {
		query string
		want  int
	}{
		{`SELECT version FROM schema_meta WHERE id=1`, SchemaVersion},
		{`SELECT max_input_tokens FROM provider_models WHERE id='synthetic-model'`, modelWant},
		{`SELECT summary_dirty FROM memory_entries WHERE id='memory-1'`, memoryWant},
	} {
		var got int
		if err := db.SQL.QueryRow(item.query).Scan(&got); err != nil || got != item.want {
			t.Fatalf("repair %s: got %d, want %d, error %v", item.query, got, item.want, err)
		}
	}
	var body string
	if err := db.SQL.QueryRow(`SELECT body FROM skills WHERE name='synthetic-skill'`).Scan(&body); err != nil || body != "preserved body" {
		t.Fatalf("repair changed skill data: %q %v", body, err)
	}
}

func TestSQLiteRepairsEmbeddingV12(t *testing.T) {
	exerciseEmbeddingV12Repair(t, SQLite)
}

func TestPostgresMigrationRepairsEmbeddingV12(t *testing.T) {
	exerciseEmbeddingV12Repair(t, Postgres)
}

func exerciseEmbeddingV12Repair(t *testing.T, backend Backend) {
	t.Helper()
	for _, columns := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("model-%v-memory-%v", columns[0], columns[1]), func(t *testing.T) {
			root := t.TempDir()
			config := Config{Backend: backend}
			if backend == Postgres {
				config = postgresMigrationConfig(t)
			}
			db, err := OpenWithConfig(context.Background(), root, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if db != nil {
					db.Close()
				}
			})
			prepareEmbeddingV12(t, db, columns[0], columns[1])
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				db, err = OpenWithConfig(context.Background(), root, config)
				if err != nil {
					t.Fatal(err)
				}
				assertEmbeddingV12Repair(t, db, columns[0], columns[1])
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestSQLiteEmbeddingRepairRollsBackAndRetries(t *testing.T) {
	root := t.TempDir()
	db, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	prepareEmbeddingV12(t, db, false, false)
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_repair BEFORE UPDATE ON schema_meta BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.initSchema(context.Background()); err == nil {
		t.Fatal("failed repair unexpectedly succeeded")
	}
	var version int
	if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&version); err != nil || version != 12 {
		t.Fatalf("failed repair advanced version: %d %v", version, err)
	}
	for _, query := range []string{`SELECT max_input_tokens FROM provider_models`, `SELECT summary_dirty FROM memory_entries`} {
		rows, err := db.SQL.Query(query)
		if err == nil {
			rows.Close()
			t.Fatalf("failed repair retained column: %s", query)
		}
	}
	assertMigrationFixture(t, db)
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_repair`); err != nil {
		t.Fatal(err)
	}
	if err := db.initSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertEmbeddingV12Repair(t, db, false, false)
	db.Close()
}
