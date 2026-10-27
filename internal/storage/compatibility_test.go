package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Keep this fixture independent of the compatibility implementation: these are
// the three tables shipped by the original computer-use v15 migration.
var compatibilityComputerUseTables = []string{
	`CREATE TABLE computer_use_config (
		instance_id TEXT PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
		config_json TEXT NOT NULL
	)`,
	`CREATE TABLE computer_use_projects (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		id TEXT NOT NULL, metadata_json TEXT NOT NULL,
		PRIMARY KEY (instance_id, id)
	)`,
	`CREATE TABLE computer_use_tasks (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		id TEXT NOT NULL, generation BIGINT NOT NULL, metadata_json TEXT NOT NULL,
		PRIMARY KEY (instance_id, id)
	)`,
}

var compatibilityJSON = []string{
	"{\n  \"enabled\": true, \"future\": { \"unknown\": [1, 2, {\"nested\": true}] }\n}",
	`{ "project": "synthetic-project", "unknown": {"version": 21, "values": [null, false]} }`,
	`{"task":"synthetic-task", "future_result": ["preserve whitespace", {"raw":"\\u0061"}]}`,
}

func TestSQLiteSchemaCompatibility(t *testing.T) {
	exerciseSchemaCompatibility(t, SQLite)
}

// The prefix includes this suite in the PostgreSQL migrations CI job.
func TestPostgresMigrationSchemaCompatibility(t *testing.T) {
	exerciseSchemaCompatibility(t, Postgres)
}

func exerciseSchemaCompatibility(t *testing.T, backend Backend) {
	t.Helper()
	ctx := context.Background()
	t.Run("fresh-metadata", func(t *testing.T) {
		_, _, db := compatibilityDatabase(t, backend)
		assertCompatibilityVersions(t, db, 14, 14)
	})
	for previous := 7; previous <= 14; previous++ {
		t.Run(fmt.Sprintf("legacy-v%d", previous), func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			prepareOldSchema(t, db, previous)
			populateMigrationFixture(t, db)
			compatibilityMetadata(t, db, previous, false, nil)
			compatibilityClose(t, db)
			for range 2 {
				db = compatibilityReopen(t, root, config)
				assertCompatibilityVersions(t, db, 14, 14)
				assertMigrationFixture(t, db)
				compatibilityClose(t, db)
			}
		})
	}
	t.Run("original-computer-use-v15", func(t *testing.T) {
		root, config, db := compatibilityDatabase(t, backend)
		compatibilitySeedComputerUse(t, db)
		compatibilityMetadata(t, db, 15, false, nil)
		compatibilityClose(t, db)
		for range 2 {
			db = compatibilityReopen(t, root, config)
			assertCompatibilityVersions(t, db, 15, 14)
			compatibilityUpdateInstance(t, db)
			assertCompatibilityJSON(t, db)
			assertMigrationFixture(t, db)
			compatibilityClose(t, db)
		}
	})
	for _, floor := range []int{7, 14} {
		t.Run(fmt.Sprintf("declared-future-floor-%d", floor), func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			compatibilitySeedComputerUse(t, db)
			compatibilityMetadata(t, db, 16, true, floor)
			compatibilityExec(t, db, `CREATE TABLE future_extension (id TEXT PRIMARY KEY, payload TEXT NOT NULL)`)
			compatibilityExec(t, db, `INSERT INTO future_extension VALUES ('synthetic-extension', 'unrecognized-data')`)
			compatibilityClose(t, db)
			for range 2 {
				db = compatibilityReopen(t, root, config)
				assertCompatibilityVersions(t, db, 16, floor)
				compatibilityUpdateInstance(t, db)
				assertCompatibilityJSON(t, db)
				var payload string
				if err := db.SQL.QueryRow(`SELECT payload FROM future_extension`).Scan(&payload); err != nil || payload != "unrecognized-data" {
					t.Fatalf("independent extension was changed: %q, %v", payload, err)
				}
				compatibilityClose(t, db)
			}
		})
	}
	t.Run("future-deletion-protection", func(t *testing.T) {
		root, config, db := compatibilityDatabase(t, backend)
		compatibilitySeedComputerUse(t, db)
		compatibilityMetadata(t, db, 16, true, 14)
		compatibilityClose(t, db)
		db = compatibilityReopen(t, root, config)
		before := compatibilitySnapshot(t, db.SQL, backend)
		if err := db.PrepareDeletion(ctx, "synthetic-instance", 1); err == nil {
			t.Fatal("older application prepared deletion of future-version data")
		}
		if after := compatibilitySnapshot(t, db.SQL, backend); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected deletion preparation changed data")
		}
		compatibilityExec(t, db, `UPDATE instances SET deleting = 1 WHERE id = 'synthetic-instance'`)
		compatibilityExec(t, db, `INSERT INTO pending_deletions VALUES ('synthetic-instance', 'synthetic-prepared-time', 1)`)
		before = compatibilitySnapshot(t, db.SQL, backend)
		if err := db.ConfirmDeletion(ctx, "synthetic-instance"); err == nil {
			t.Fatal("older application deleted future-version data")
		}
		if after := compatibilitySnapshot(t, db.SQL, backend); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected deletion confirmation changed data")
		}
		if err := db.CancelDeletion(ctx, "synthetic-instance"); err != nil {
			t.Fatalf("safe cancellation should remain available: %v", err)
		}
		var deleting, pending int
		if err := db.SQL.QueryRow(`SELECT deleting FROM instances WHERE id = 'synthetic-instance'`).Scan(&deleting); err != nil || deleting != 0 {
			t.Fatalf("cancellation did not reset deleting: %d, %v", deleting, err)
		}
		if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM pending_deletions`).Scan(&pending); err != nil || pending != 0 {
			t.Fatalf("cancellation left deletion marker: %d, %v", pending, err)
		}
		assertCompatibilityJSON(t, db)
		assertCompatibilityVersions(t, db, 16, 14)
	})

	rejections := []struct {
		name     string
		version  int
		declared bool
		floor    any
		mutate   func(*testing.T, *DB)
	}{
		{"unknown-unmarked-future", 16, false, nil, nil},
		{"unmarked-v15-missing-table", 15, false, nil, func(t *testing.T, db *DB) {
			compatibilityExec(t, db, `DROP TABLE computer_use_tasks`)
		}},
		{"unmarked-v15-extra-table", 15, false, nil, func(t *testing.T, db *DB) {
			compatibilityExec(t, db, `CREATE TABLE unknown_addition (payload TEXT)`)
		}},
		{"unmarked-v15-altered-table", 15, false, nil, func(t *testing.T, db *DB) {
			compatibilityExec(t, db, `ALTER TABLE computer_use_tasks ADD COLUMN unknown_value TEXT`)
		}},
		{"null-floor", 16, true, nil, nil},
		{"negative-floor", 16, true, -1, nil},
		{"pre-baseline-floor", 16, true, 6, nil},
		{"floor-above-version", 16, true, 17, nil},
		{"requires-newer-program", 16, true, 15, nil},
		{"missing-core-column", 16, true, 14, func(t *testing.T, db *DB) {
			compatibilityExec(t, db, `ALTER TABLE instances DROP COLUMN error`)
		}},
		{"unknown-core-column", 16, true, 14, func(t *testing.T, db *DB) {
			compatibilityExec(t, db, `ALTER TABLE instances ADD COLUMN future_payload TEXT NOT NULL DEFAULT '{}'`)
		}},
		{"changed-core-column-type", 16, true, 14, func(t *testing.T, db *DB) {
			compatibilityExec(t, db, `ALTER TABLE instances DROP COLUMN error`)
			compatibilityExec(t, db, `ALTER TABLE instances ADD COLUMN error INTEGER NOT NULL DEFAULT 0`)
		}},
		{"extension-fk-to-replaced-core-row", 16, true, 14, func(t *testing.T, db *DB) {
			compatibilityExec(t, db, `CREATE TABLE future_settings (
				scope TEXT NOT NULL, instance_id TEXT NOT NULL, key TEXT NOT NULL, payload TEXT NOT NULL,
				FOREIGN KEY (scope, instance_id, key) REFERENCES settings(scope, instance_id, key) ON DELETE CASCADE
			)`)
			compatibilityExec(t, db, `INSERT INTO future_settings VALUES ('instance', 'synthetic-instance', 'UPSTREAM_API_KEY', 'must-not-cascade')`)
		}},
		{"core-trigger", 16, true, 14, func(t *testing.T, db *DB) {
			if db.Backend == SQLite {
				compatibilityExec(t, db, `CREATE TRIGGER compatibility_update AFTER UPDATE ON instances
					BEGIN UPDATE settings SET value = 'trigger-value' WHERE key = 'UPSTREAM_API_KEY'; END`)
			} else {
				compatibilityExec(t, db, `CREATE FUNCTION compatibility_update() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN UPDATE settings SET value = 'trigger-value' WHERE key = 'UPSTREAM_API_KEY'; RETURN NEW; END; $$`)
				compatibilityExec(t, db, `CREATE TRIGGER compatibility_update AFTER UPDATE ON instances
					FOR EACH ROW EXECUTE FUNCTION compatibility_update()`)
			}
		}},
	}
	for _, test := range rejections {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			compatibilitySeedComputerUse(t, db)
			compatibilityMetadata(t, db, test.version, test.declared, test.floor)
			if test.mutate != nil {
				test.mutate(t, db)
			}
			before := compatibilitySnapshot(t, db.SQL, backend)
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(ctx, root, config)
			if err == nil {
				opened.Close()
				t.Fatal("incompatible database was accepted")
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
				t.Fatal("rejected startup changed database rows or column layout")
			}
		})
	}
}

func compatibilityDatabase(t *testing.T, backend Backend) (string, Config, *DB) {
	t.Helper()
	root := t.TempDir()
	config := Config{Backend: backend}
	if backend == Postgres {
		config = postgresMigrationConfig(t)
	}
	return root, config, compatibilityReopen(t, root, config)
}

func compatibilityReopen(t *testing.T, root string, config Config) *DB {
	t.Helper()
	db, err := OpenWithConfig(context.Background(), root, config)
	if err != nil {
		t.Fatalf("open compatibility fixture: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func compatibilityClose(t *testing.T, db *DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func compatibilityExec(t *testing.T, db *DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.SQL.Exec(db.Bind(statement), args...); err != nil {
		t.Fatalf("prepare disposable compatibility fixture: %v", err)
	}
}

func compatibilityMetadata(t *testing.T, db *DB, version int, declared bool, floor any) {
	t.Helper()
	compatibilityExec(t, db, `DROP TABLE schema_meta`)
	if declared {
		compatibilityExec(t, db, `CREATE TABLE schema_meta (id INTEGER PRIMARY KEY, version INTEGER NOT NULL, min_compatible_version INTEGER)`)
		compatibilityExec(t, db, `INSERT INTO schema_meta VALUES (1, ?, ?)`, version, floor)
	} else {
		compatibilityExec(t, db, `CREATE TABLE schema_meta (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)`)
		compatibilityExec(t, db, `INSERT INTO schema_meta VALUES (1, ?)`, version)
	}
}

func compatibilitySeedComputerUse(t *testing.T, db *DB) {
	t.Helper()
	populateMigrationFixture(t, db)
	compatibilityExec(t, db, `UPDATE instances SET created_at = '2026-01-01T00:00:00Z'`)
	for _, statement := range compatibilityComputerUseTables {
		compatibilityExec(t, db, statement)
	}
	compatibilityExec(t, db, `INSERT INTO computer_use_config VALUES ('synthetic-instance', ?)`, compatibilityJSON[0])
	compatibilityExec(t, db, `INSERT INTO computer_use_projects VALUES ('synthetic-instance', 'synthetic-project', ?)`, compatibilityJSON[1])
	compatibilityExec(t, db, `INSERT INTO computer_use_tasks VALUES ('synthetic-instance', 'synthetic-task', 73, ?)`, compatibilityJSON[2])
}

func compatibilityUpdateInstance(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	records, _, err := db.LoadInstances(ctx)
	if err != nil || len(records) != 1 {
		t.Fatalf("read compatible instance: %d records, %v", len(records), err)
	}
	records[0].Enabled = !records[0].Enabled
	if err := db.UpdateInstance(ctx, records[0]); err != nil {
		t.Fatalf("ordinary compatible instance update failed: %v", err)
	}
}

func assertCompatibilityVersions(t *testing.T, db *DB, version, floor int) {
	t.Helper()
	var actualVersion, actualFloor int
	err := db.SQL.QueryRow(`SELECT version, min_compatible_version FROM schema_meta WHERE id = 1`).Scan(&actualVersion, &actualFloor)
	if err != nil || actualVersion != version || actualFloor != floor {
		t.Fatalf("compatibility metadata = (%d, %d), want (%d, %d): %v", actualVersion, actualFloor, version, floor, err)
	}
}

func assertCompatibilityJSON(t *testing.T, db *DB) {
	t.Helper()
	for i, query := range []string{
		`SELECT config_json FROM computer_use_config WHERE instance_id = 'synthetic-instance'`,
		`SELECT metadata_json FROM computer_use_projects WHERE id = 'synthetic-project'`,
		`SELECT metadata_json FROM computer_use_tasks WHERE id = 'synthetic-task'`,
	} {
		var actual string
		if err := db.SQL.QueryRow(query).Scan(&actual); err != nil || actual != compatibilityJSON[i] {
			t.Fatalf("future JSON %d was lost or rewritten: %q, %v", i, actual, err)
		}
	}
	var generation int
	if err := db.SQL.QueryRow(`SELECT generation FROM computer_use_tasks`).Scan(&generation); err != nil || generation != 73 {
		t.Fatalf("task generation changed: %d, %v", generation, err)
	}
}

// Snapshot every user table, column type and raw value, not merely the version
// number, so a rejected open cannot quietly rewrite unfamiliar data.
func compatibilitySnapshot(t *testing.T, db *sql.DB, backend Backend) map[string][]string {
	t.Helper()
	query := `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	if backend == Postgres {
		query = `SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = current_schema() ORDER BY tablename`
	}
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string][]string{}
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		var layout []string
		for _, column := range columns {
			layout = append(layout, column.Name()+":"+column.DatabaseTypeName())
		}
		snapshot[table+":columns"] = layout
		var data []string
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			data = append(data, string(encoded))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(data)
		snapshot[table+":rows"] = data
	}
	return snapshot
}
