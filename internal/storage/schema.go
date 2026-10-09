package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// schemaStatements describe the SQL-owned state. JSON is retained only for
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
		instance_id TEXT NOT NULL, endpoint_id TEXT NOT NULL, secret TEXT NOT NULL,
		PRIMARY KEY (instance_id, endpoint_id),
		FOREIGN KEY (instance_id, endpoint_id) REFERENCES model_endpoints(instance_id, id) ON DELETE CASCADE
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
		position INTEGER NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL,
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
		principal_key TEXT PRIMARY KEY, state TEXT NOT NULL, record_json TEXT NOT NULL
	)`,
	`CREATE TABLE security_audit (
		id TEXT PRIMARY KEY, occurred_at TEXT NOT NULL, instance_id TEXT NOT NULL DEFAULT '',
		event_json TEXT NOT NULL
	)`,
	`CREATE TABLE pending_deletions (
		instance_id TEXT PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
		prepared_at TEXT NOT NULL, backup_version INTEGER NOT NULL
	)`,
}

var tables = []string{
	"pending_deletions", "security_audit", "access_records", "sticker_keywords", "sticker_entries",
	"group_summaries", "memory_catalogs", "group_members", "group_profiles",
	"memory_merge_archives", "memory_tags", "memory_entries", "dialogues",
	"mcp_tool_policies", "mcp_servers", "model_bindings", "models", "model_secrets",
	"model_endpoints", "model_revisions", "settings", "endpoint_ids", "counters", "instances",
}

func (d *DB) initSchema(ctx context.Context) error {
	if _, err := d.SQL.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_meta (
		id INTEGER PRIMARY KEY, version INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema metadata: %w", err)
	}
	var version int
	err := d.SQL.QueryRowContext(ctx, "SELECT version FROM schema_meta WHERE id = 1").Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read schema version: %w", err)
	}
	if err == nil && version == SchemaVersion {
		return nil
	}
	// Before v1.0, an incompatible SQL schema is deliberately rebuilt. Keep the
	// version write and every table mutation in one transaction.
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range tables {
		if _, err = tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			return fmt.Errorf("drop incompatible table %s: %w", table, err)
		}
	}
	for _, statement := range schemaStatements {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, d.Bind("INSERT INTO schema_meta(id, version) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET version = excluded.version"), SchemaVersion); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return tx.Commit()
}
