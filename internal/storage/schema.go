package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// schemaStatements are the immutable v7 baseline. JSON is retained only for
// fields whose shape is intentionally user-defined, such as MCP transport
// parameters and setting values.
var schemaStatements = []string{
	`CREATE TABLE instances (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 0, deleting INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '', credential_targets_json TEXT NOT NULL DEFAULT '[]'
	)`,
	`CREATE TABLE counters (
		key TEXT PRIMARY KEY, value BIGINT NOT NULL
	)`,
	`CREATE TABLE endpoint_ids (
		id TEXT PRIMARY KEY, instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE settings (
		scope TEXT NOT NULL, instance_id TEXT NOT NULL, key TEXT NOT NULL,
		value TEXT NOT NULL, PRIMARY KEY (scope, instance_id, key)
	)`,
	`CREATE TABLE model_endpoints (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		id TEXT NOT NULL, display_name TEXT NOT NULL, base_url TEXT NOT NULL,
		api_key_source TEXT NOT NULL, api_key_ref TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL, position INTEGER NOT NULL,
		PRIMARY KEY (instance_id, id)
	)`,
	`CREATE TABLE model_secrets (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		ref TEXT NOT NULL, secret TEXT NOT NULL,
		PRIMARY KEY (instance_id, ref)
	)`,
	`CREATE TABLE models (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		id TEXT NOT NULL, display_name TEXT NOT NULL, endpoint_id TEXT NOT NULL,
		upstream_model TEXT NOT NULL, enabled INTEGER NOT NULL, capabilities TEXT NOT NULL,
		position INTEGER NOT NULL, PRIMARY KEY (instance_id, id),
		FOREIGN KEY (instance_id, endpoint_id) REFERENCES model_endpoints(instance_id, id)
	)`,
	`CREATE TABLE model_bindings (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		platform TEXT NOT NULL, group_id TEXT NOT NULL, workload TEXT NOT NULL,
		mode TEXT NOT NULL, model_id TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (instance_id, platform, group_id, workload)
	)`,
	`CREATE TABLE model_revisions (
		instance_id TEXT PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
		revision BIGINT NOT NULL
	)`,
	`CREATE TABLE mcp_servers (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		id TEXT NOT NULL, name TEXT NOT NULL, enabled INTEGER NOT NULL,
		transport_type TEXT NOT NULL, command TEXT NOT NULL DEFAULT '',
		working_dir TEXT NOT NULL DEFAULT '', url TEXT NOT NULL DEFAULT '',
		args_json TEXT NOT NULL DEFAULT '[]', env_json TEXT NOT NULL DEFAULT '{}',
		headers_json TEXT NOT NULL DEFAULT '{}', position INTEGER NOT NULL,
		PRIMARY KEY (instance_id, id)
	)`,
	`CREATE TABLE mcp_tool_policies (
		instance_id TEXT NOT NULL, server_id TEXT NOT NULL, tool_name TEXT NOT NULL,
		enabled INTEGER NOT NULL, PRIMARY KEY (instance_id, server_id, tool_name),
		FOREIGN KEY (instance_id, server_id) REFERENCES mcp_servers(instance_id, id) ON DELETE CASCADE
	)`,
	`CREATE TABLE dialogues (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		position INTEGER NOT NULL, id TEXT NOT NULL DEFAULT '', scene TEXT NOT NULL DEFAULT '',
		relation TEXT NOT NULL DEFAULT '', user_text TEXT NOT NULL DEFAULT '', preferred TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (instance_id, position)
	)`,
	`CREATE TABLE memory_entries (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		scope_type TEXT NOT NULL, platform TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL DEFAULT '',
		id TEXT NOT NULL, owner TEXT NOT NULL, owner_type TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL, summary TEXT NOT NULL DEFAULT '', evidence TEXT NOT NULL DEFAULT '',
		source_message_id TEXT NOT NULL DEFAULT '', source_sender_id TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL, visibility TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL, access_count INTEGER NOT NULL DEFAULT 0,
		merged_from_json TEXT NOT NULL DEFAULT '[]',
		PRIMARY KEY (instance_id, scope_type, platform, group_id, id)
	)`,
	`CREATE TABLE memory_tags (
		instance_id TEXT NOT NULL, scope_type TEXT NOT NULL, platform TEXT NOT NULL,
		group_id TEXT NOT NULL, memory_id TEXT NOT NULL, tag TEXT NOT NULL,
		PRIMARY KEY (instance_id, scope_type, platform, group_id, memory_id, tag),
		FOREIGN KEY (instance_id, scope_type, platform, group_id, memory_id)
			REFERENCES memory_entries(instance_id, scope_type, platform, group_id, id) ON DELETE CASCADE
	)`,
	`CREATE INDEX memory_owner_idx ON memory_entries(instance_id, scope_type, platform, group_id, owner)`,
	`CREATE INDEX memory_updated_idx ON memory_entries(instance_id, updated_at)`,
	`CREATE TABLE memory_merge_archives (
		instance_id TEXT NOT NULL, scope_type TEXT NOT NULL, platform TEXT NOT NULL,
		group_id TEXT NOT NULL, merged_id TEXT NOT NULL, owner TEXT NOT NULL,
		merged_at TEXT NOT NULL, sources_json TEXT NOT NULL,
		PRIMARY KEY (instance_id, scope_type, platform, group_id, merged_id),
		FOREIGN KEY (instance_id) REFERENCES instances(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE group_profiles (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		platform TEXT NOT NULL, group_id TEXT NOT NULL, group_name TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL, PRIMARY KEY (instance_id, platform, group_id)
	)`,
	`CREATE TABLE group_members (
		instance_id TEXT NOT NULL, platform TEXT NOT NULL, group_id TEXT NOT NULL,
		user_id TEXT NOT NULL, nickname TEXT NOT NULL DEFAULT '', card TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT '', preferred_name TEXT NOT NULL DEFAULT '',
		aliases_json TEXT NOT NULL DEFAULT '[]', source TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL, last_spoke_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (instance_id, platform, group_id, user_id),
		FOREIGN KEY (instance_id, platform, group_id)
			REFERENCES group_profiles(instance_id, platform, group_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE memory_catalogs (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		scope_type TEXT NOT NULL, platform TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL DEFAULT '',
		owner TEXT NOT NULL, catalog_json TEXT NOT NULL,
		PRIMARY KEY (instance_id, scope_type, platform, group_id, owner)
	)`,
	`CREATE TABLE group_summaries (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		platform TEXT NOT NULL, group_id TEXT NOT NULL, summary TEXT NOT NULL,
		generation BIGINT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		PRIMARY KEY (instance_id, platform, group_id)
	)`,
	`CREATE TABLE sticker_entries (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		id TEXT NOT NULL, file_name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
		weight INTEGER NOT NULL, status TEXT NOT NULL, model_suspected INTEGER NOT NULL,
		manual_blocked INTEGER NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
		position INTEGER NOT NULL,
		PRIMARY KEY (instance_id, id)
	)`,
	`CREATE TABLE sticker_keywords (
		instance_id TEXT NOT NULL, sticker_id TEXT NOT NULL, position INTEGER NOT NULL,
		keyword TEXT NOT NULL, PRIMARY KEY (instance_id, sticker_id, position),
		FOREIGN KEY (instance_id, sticker_id) REFERENCES sticker_entries(instance_id, id) ON DELETE CASCADE
	)`,
	`CREATE TABLE access_records (
		principal_key TEXT PRIMARY KEY, platform TEXT NOT NULL, user_id TEXT NOT NULL,
		state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', locked_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL, strike_times_json TEXT NOT NULL DEFAULT '[]',
		last_blocked_hash TEXT NOT NULL DEFAULT '', last_blocked_at TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE security_audit (
		id TEXT PRIMARY KEY, occurred_at TEXT NOT NULL, platform TEXT NOT NULL,
		user_id TEXT NOT NULL, instance_id TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
		tool TEXT NOT NULL DEFAULT '', stage TEXT NOT NULL, source TEXT NOT NULL, action TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT '', preview TEXT NOT NULL DEFAULT '',
		encoded INTEGER NOT NULL DEFAULT 0, category TEXT NOT NULL DEFAULT '', risk_level TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE pending_deletions (
		instance_id TEXT PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
		prepared_at TEXT NOT NULL, backup_version INTEGER NOT NULL
	)`,
}

// A migration is applied once in its own transaction. Released entries must
// never be edited; append a new version for every later schema change.
type migration struct {
	version    int
	statements []string
}

var migrations = []migration{
	{8, []string{
		`CREATE INDEX memory_source_message_idx ON memory_entries(instance_id, scope_type, platform, group_id, source_message_id)`,
	}},
	{9, []string{
		`CREATE INDEX security_audit_occurred_idx ON security_audit(occurred_at DESC, id DESC)`,
	}},
}

func (d *DB) initSchema(ctx context.Context) error {
	var version int
	err := d.SQL.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !isMissingSchemaTable(err) {
		return fmt.Errorf("read schema version: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) || isMissingSchemaTable(err) {
		return d.createSchema(ctx)
	}
	if version > SchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, SchemaVersion)
	}
	if version < 7 {
		return fmt.Errorf("database schema version %d predates the v7 SQL baseline", version)
	}
	for _, step := range migrations {
		if step.version <= version {
			continue
		}
		if step.version != version+1 {
			return fmt.Errorf("missing migration from schema version %d", version)
		}
		if err := d.applyMigration(ctx, step); err != nil {
			return err
		}
		version = step.version
	}
	if version != SchemaVersion {
		return fmt.Errorf("missing migration from schema version %d", version)
	}
	return nil
}

func (d *DB) createSchema(ctx context.Context) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_meta (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema metadata: %w", err)
	}
	for _, statement := range schemaStatements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create v7 schema: %w", err)
		}
	}
	for _, step := range migrations {
		for _, statement := range step.statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("create schema v%d: %w", step.version, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO schema_meta(id, version) VALUES (1, ?)`), SchemaVersion); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return tx.Commit()
}

func (d *DB) applyMigration(ctx context.Context, step migration) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version for migration: %w", err)
	}
	if version != step.version-1 {
		return fmt.Errorf("expected schema version %d before migration v%d, got %d", step.version-1, step.version, version)
	}
	for _, statement := range step.statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema migration v%d: %w", step.version, err)
		}
	}
	if _, err := tx.ExecContext(ctx, d.Bind(`UPDATE schema_meta SET version = ? WHERE id = 1`), step.version); err != nil {
		return fmt.Errorf("advance schema version to %d: %w", step.version, err)
	}
	return tx.Commit()
}

func isMissingSchemaTable(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "no such table: schema_meta") ||
		strings.Contains(message, `relation "schema_meta" does not exist`)
}
