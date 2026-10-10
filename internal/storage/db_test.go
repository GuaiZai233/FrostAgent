package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
)

func populateMigrationFixture(t *testing.T, db *DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO instances(id, name, created_at, enabled) VALUES ('synthetic-instance', 'Synthetic', 'now', 0)`,
		`INSERT INTO settings(scope, instance_id, key, value) VALUES ('instance', 'synthetic-instance', 'UPSTREAM_API_KEY', 'synthetic-secret')`,
		`INSERT INTO model_secrets(instance_id, ref, secret) VALUES ('synthetic-instance', 'endpoint', 'synthetic-model-secret')`,
		`INSERT INTO memory_entries(instance_id, scope_type, platform, group_id, id, owner, content, source, created_at, updated_at) VALUES ('synthetic-instance', 'group', 'qq', 'synthetic-group', 'memory-1', 'synthetic-owner', 'remembered', 'manual', 'now', 'now')`,
		`INSERT INTO group_profiles(instance_id, platform, group_id, updated_at) VALUES ('synthetic-instance', 'qq', 'synthetic-group', 'now')`,
		`INSERT INTO group_members(instance_id, platform, group_id, user_id, created_at, updated_at) VALUES ('synthetic-instance', 'qq', 'synthetic-group', 'synthetic-user', 'now', 'now')`,
		`INSERT INTO access_records(principal_key, platform, user_id, state, updated_at) VALUES ('synthetic-principal', 'qq', 'synthetic-user', 'locked', 'now')`,
		`INSERT INTO security_audit(id, occurred_at, platform, user_id, stage, source, action) VALUES ('synthetic-audit', 'now', 'qq', 'synthetic-user', 'ingress', 'test', 'block')`,
	} {
		if _, err := db.SQL.Exec(statement); err != nil {
			t.Fatalf("populate migration fixture: %v", err)
		}
	}
}

func assertMigrationFixture(t *testing.T, db *DB) {
	t.Helper()
	for _, item := range []struct{ query, want string }{
		{`SELECT name FROM instances WHERE id = 'synthetic-instance'`, "Synthetic"},
		{`SELECT value FROM settings WHERE key = 'UPSTREAM_API_KEY'`, "synthetic-secret"},
		{`SELECT secret FROM model_secrets WHERE ref = 'endpoint'`, "synthetic-model-secret"},
		{`SELECT content FROM memory_entries WHERE id = 'memory-1'`, "remembered"},
		{`SELECT user_id FROM group_members WHERE user_id = 'synthetic-user'`, "synthetic-user"},
		{`SELECT state FROM access_records WHERE principal_key = 'synthetic-principal'`, "locked"},
		{`SELECT action FROM security_audit WHERE id = 'synthetic-audit'`, "block"},
	} {
		var got string
		if err := db.SQL.QueryRow(item.query).Scan(&got); err != nil || got != item.want {
			t.Fatalf("migration changed fixture: %s: %q, %v", item.query, got, err)
		}
	}
}

func prepareOldSchema(t *testing.T, db *DB, version int) {
	t.Helper()
	if version <= 8 {
		if _, err := db.SQL.Exec(`DROP INDEX security_audit_occurred_idx`); err != nil {
			t.Fatal(err)
		}
	}
	if version <= 7 {
		if _, err := db.SQL.Exec(`DROP INDEX memory_source_message_idx`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SQL.Exec(db.Bind(`UPDATE schema_meta SET version = ? WHERE id = 1`), version); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteMigratesPopulatedV7AndV8(t *testing.T) {
	ctx := context.Background()
	for _, previous := range []int{7, 8} {
		t.Run(strconv.Itoa(previous), func(t *testing.T) {
			root := t.TempDir()
			db, err := Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			populateMigrationFixture(t, db)
			prepareOldSchema(t, db, previous)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			for start := range 2 {
				db, err = Open(ctx, root)
				if err != nil {
					t.Fatalf("startup %d: %v", start, err)
				}
				assertMigrationFixture(t, db)
				var got int
				if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id = 1`).Scan(&got); err != nil || got != SchemaVersion {
					t.Fatalf("schema version = %d, %v", got, err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestSQLiteRejectsFutureAndPreBaselineSchemaWithoutMutation(t *testing.T) {
	for _, version := range []int{0, SchemaVersion + 1} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			root := t.TempDir()
			db, err := Open(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			populateMigrationFixture(t, db)
			if _, err := db.SQL.Exec(db.Bind(`UPDATE schema_meta SET version = ? WHERE id = 1`), version); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if db, err = Open(context.Background(), root); err == nil {
				db.Close()
				t.Fatal("unsupported schema was loaded")
			}
			raw, err := sql.Open("sqlite", filepath.Join(root, "frostagent.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			var gotVersion, count int
			if err := raw.QueryRow(`SELECT version FROM schema_meta WHERE id = 1`).Scan(&gotVersion); err != nil {
				t.Fatal(err)
			}
			if err := raw.QueryRow(`SELECT COUNT(*) FROM instances WHERE id = 'synthetic-instance'`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if gotVersion != version || count != 1 {
				t.Fatalf("unsupported schema was modified: version=%d instances=%d", gotVersion, count)
			}
		})
	}
}

func TestSQLiteFailedMigrationRollsBackAndRetries(t *testing.T) {
	root := t.TempDir()
	db, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	populateMigrationFixture(t, db)
	prepareOldSchema(t, db, 7)
	err = db.applyMigration(context.Background(), migration{8, []string{
		`CREATE INDEX memory_source_message_idx ON memory_entries(instance_id, source_message_id)`,
		`THIS IS NOT SQL`,
	}})
	if err == nil {
		t.Fatal("broken migration unexpectedly succeeded")
	}
	var version int
	if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil || version != 7 {
		t.Fatalf("failed migration advanced version: %d, %v", version, err)
	}
	var count int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'memory_source_message_idx'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration left index: %d, %v", count, err)
	}
	assertMigrationFixture(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(context.Background(), root)
	if err != nil || db == nil {
		t.Fatalf("retry migration: %v", err)
	}
	defer db.Close()
	assertMigrationFixture(t, db)
}

func TestSQLiteRejectsMissingParentInstance(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.SQL.Exec(`INSERT INTO memory_entries (
		instance_id, scope_type, platform, group_id, id, owner, content, source, created_at, updated_at
	) VALUES ('missing', 'private', '', '', 'm1', 'user', 'content', 'manual', 'now', 'now')`)
	if err == nil {
		t.Fatalf("memory without an instance was accepted: %v", err)
	}
}
