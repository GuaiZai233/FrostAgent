package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type schemaReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func hasCompatibilityColumn(ctx context.Context, q schemaReader) (bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT * FROM schema_meta WHERE 1=0`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return false, err
	}
	for _, name := range columns {
		if name == "min_compatible_version" {
			return true, nil
		}
	}
	return false, rows.Err()
}

func addCompatibilityColumn(ctx context.Context, tx *sql.Tx) error {
	has, err := hasCompatibilityColumn(ctx, tx)
	if err != nil || has {
		return err
	}
	// Zero is deliberately invalid. The real value is written in this same
	// transaction; older writers must not silently fabricate compatibility.
	_, err = tx.ExecContext(ctx, `ALTER TABLE schema_meta ADD COLUMN min_compatible_version INTEGER NOT NULL DEFAULT 0`)
	return err
}

func validateCompatibility(version, minimum int) error {
	if minimum < 7 || minimum > version {
		return fmt.Errorf("invalid database compatibility range [%d, %d]", minimum, version)
	}
	return nil
}

func (d *DB) prepareCompatibility(ctx context.Context, expectedVersion int) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil {
		return err
	}
	if version != expectedVersion {
		return fmt.Errorf("database schema changed during compatibility check; retry startup")
	}
	has, err := hasCompatibilityColumn(ctx, tx)
	if err != nil {
		return err
	}
	minimum := version
	if has {
		if err := tx.QueryRowContext(ctx, `SELECT min_compatible_version FROM schema_meta WHERE id = 1`).Scan(&minimum); err != nil {
			return fmt.Errorf("read database compatibility: %w", err)
		}
	} else if version > SchemaVersion {
		// The only pre-marker future layout we know is Computer Use v15.
		// Do not guess compatibility from a version number or missing marker.
		if version != 15 || SchemaVersion != 14 {
			return fmt.Errorf("database schema version %d is newer than supported version %d and has no compatibility declaration", version, SchemaVersion)
		}
		if err := d.validateLegacyComputerUse(ctx, tx); err != nil {
			return fmt.Errorf("unrecognized legacy v15 database: %w", err)
		}
		minimum = 14
	}
	if err := validateCompatibility(version, minimum); err != nil {
		return err
	}
	if minimum > SchemaVersion {
		return fmt.Errorf("database schema version %d requires compatible schema %d or newer; this program supports %d", version, minimum, SchemaVersion)
	}
	if version > SchemaVersion {
		if err := d.validateFutureLayout(ctx, tx); err != nil {
			return fmt.Errorf("database compatibility declaration cannot be used by this program: %w", err)
		}
	}
	if !has {
		if err := addCompatibilityColumn(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, d.Bind(`UPDATE schema_meta SET min_compatible_version = ? WHERE id = 1`), minimum); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RequireNativeSchema protects operations that claim to encompass an entire
// instance. Older code cannot export, restore or delete unknown module data.
// Normal operations on the validated core remain available.
func (d *DB) RequireNativeSchema(ctx context.Context, operation string) error {
	return requireNativeSchema(ctx, d.SQL, operation)
}

func requireNativeSchema(ctx context.Context, q schemaReader, operation string) error {
	var version int
	if err := q.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil {
		return err
	}
	if version > SchemaVersion {
		return fmt.Errorf("%s requires a program supporting database schema %d; this program supports %d and cannot preserve all newer module data", operation, version, SchemaVersion)
	}
	return nil
}

// These are the columns owned by the v14 writer, not an exhaustive description
// of future databases. Extra columns are unsafe while some stores replace rows.
// Updating this contract requires auditing the reader AND writer paths.
var compatibleCoreColumns = map[string]string{
	"access_records":        "principal_key=text platform=text user_id=text state=text reason=text locked_at=text updated_at=text strike_times_json=text last_blocked_hash=text last_blocked_at=text",
	"counters":              "key=text value=bigint",
	"dialogues":             "instance_id=text position=integer id=text scene=text relation=text user_text=text preferred=text",
	"group_members":         "instance_id=text platform=text group_id=text user_id=text nickname=text card=text role=text preferred_name=text aliases_json=text source=text created_at=text updated_at=text last_spoke_at=text",
	"group_profiles":        "instance_id=text platform=text group_id=text group_name=text updated_at=text",
	"group_summaries":       "instance_id=text platform=text group_id=text summary=text generation=bigint created_at=text updated_at=text",
	"instances":             "id=text name=text created_at=text enabled=integer deleting=integer error=text credential_targets_json=text",
	"mcp_servers":           "instance_id=text id=text name=text enabled=integer transport_type=text command=text working_dir=text url=text args_json=text env_json=text headers_json=text position=integer",
	"mcp_tool_policies":     "instance_id=text server_id=text tool_name=text enabled=integer",
	"memory_catalogs":       "instance_id=text scope_type=text platform=text group_id=text owner=text catalog_json=text",
	"memory_entries":        "instance_id=text scope_type=text platform=text group_id=text id=text owner=text owner_type=text content=text summary=text evidence=text source_message_id=text source_sender_id=text source=text visibility=text created_at=text updated_at=text access_count=integer merged_from_json=text source_media=text summary_dirty=integer",
	"memory_merge_archives": "instance_id=text scope_type=text platform=text group_id=text merged_id=text owner=text merged_at=text sources_json=text",
	"memory_tags":           "instance_id=text scope_type=text platform=text group_id=text memory_id=text tag=text",
	"model_bindings":        "instance_id=text platform=text group_id=text workload=text mode=text model_id=text",
	"model_revisions":       "instance_id=text revision=bigint",
	"pending_deletions":     "instance_id=text prepared_at=text backup_version=integer",
	"provider_endpoints":    "id=text display_name=text base_url=text api_key_source=text api_key_ref=text enabled=integer position=integer",
	"provider_models":       "id=text display_name=text endpoint_id=text upstream_model=text enabled=integer capabilities=text position=integer max_input_tokens=integer",
	"provider_revision":     "id=integer revision=bigint",
	"provider_secrets":      "ref=text secret=text",
	"security_audit":        "id=text occurred_at=text platform=text user_id=text instance_id=text session_id=text tool=text stage=text source=text action=text reason=text content_hash=text preview=text encoded=integer category=text risk_level=text",
	"settings":              "scope=text instance_id=text key=text value=text",
	"skills":                "instance_id=text name=text enabled=integer info_json=text body=text package_json=text size_bytes=integer",
	"sticker_entries":       "instance_id=text id=text file_name=text description=text weight=integer status=text model_suspected=integer manual_blocked=integer created_at=bigint updated_at=bigint position=integer",
	"sticker_keywords":      "instance_id=text sticker_id=text position=integer keyword=text",
}

type schemaColumn struct {
	name, kind string
	notNull    bool
	primary    int
}

func (d *DB) tableColumns(ctx context.Context, q schemaReader, table string) ([]schemaColumn, error) {
	query := `SELECT name, lower(type), "notnull", pk FROM pragma_table_info(?) ORDER BY cid`
	if d.Backend == Postgres {
		query = `SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod), a.attnotnull,
			COALESCE(array_position(c.conkey, a.attnum), 0)
			FROM pg_catalog.pg_attribute a LEFT JOIN pg_catalog.pg_constraint c
			ON c.conrelid = a.attrelid AND c.contype = 'p'
			WHERE a.attrelid = pg_catalog.to_regclass($1) AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`
	}
	rows, err := q.QueryContext(ctx, query, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []schemaColumn
	for rows.Next() {
		var column schemaColumn
		if err := rows.Scan(&column.name, &column.kind, &column.notNull, &column.primary); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func (d *DB) matchColumns(ctx context.Context, q schemaReader, table, expected string) ([]schemaColumn, error) {
	columns, err := d.tableColumns(ctx, q, table)
	if err != nil {
		return nil, err
	}
	want := strings.Fields(expected)
	if len(columns) != len(want) {
		return nil, fmt.Errorf("table %s has missing or unrecognized columns", table)
	}
	for index, column := range columns {
		if column.name+"="+column.kind != want[index] {
			return nil, fmt.Errorf("table %s column %s does not match the supported layout", table, column.name)
		}
	}
	return columns, nil
}

func (d *DB) validateLegacyComputerUse(ctx context.Context, q schemaReader) error {
	tables, err := d.schemaTables(ctx, q)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if _, core := compatibleCoreColumns[table]; core {
			continue
		}
		switch table {
		case "schema_meta", "storage_selection", "endpoint_ids", "model_endpoints", "model_secrets", "models",
			"memory_embeddings", "memory_embedding_scopes", "computer_use_config", "computer_use_projects", "computer_use_tasks":
		default:
			return fmt.Errorf("unrecognized legacy table %s", table)
		}
	}
	for table, expected := range map[string]string{
		"computer_use_config":   "instance_id=text config_json=text",
		"computer_use_projects": "instance_id=text id=text metadata_json=text",
		"computer_use_tasks":    "instance_id=text id=text generation=bigint metadata_json=text",
	} {
		columns, err := d.matchColumns(ctx, q, table, expected)
		if err != nil {
			return err
		}
		for i, column := range columns {
			pk := 0
			if i == 0 || (i == 1 && table != "computer_use_config") {
				pk = i + 1
			}
			if column.primary != pk || (!column.notNull && !(table == "computer_use_config" && i == 0 && d.Backend == SQLite)) {
				return fmt.Errorf("table %s has unrecognized constraints", table)
			}
		}
		keys, err := d.tableForeignKeys(ctx, q, table)
		if err != nil {
			return err
		}
		if len(keys) != 1 || keys[0] != (schemaForeignKey{"instance_id", "instances", "id", "CASCADE"}) {
			return fmt.Errorf("table %s has unrecognized foreign keys", table)
		}
	}
	return nil
}

type schemaForeignKey struct{ column, parent, parentColumn, onDelete string }

func (d *DB) tableForeignKeys(ctx context.Context, q schemaReader, table string) ([]schemaForeignKey, error) {
	query := `SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list(?)`
	if d.Backend == Postgres {
		query = `SELECT a.attname, parent.relname, pa.attname,
			CASE c.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END
			FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_class parent ON parent.oid = c.confrelid
			CROSS JOIN LATERAL unnest(c.conkey, c.confkey) AS k(localkey, parentkey)
			JOIN pg_catalog.pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.localkey
			JOIN pg_catalog.pg_attribute pa ON pa.attrelid = c.confrelid AND pa.attnum = k.parentkey
			WHERE c.contype = 'f' AND c.conrelid = pg_catalog.to_regclass($1)`
	}
	rows, err := q.QueryContext(ctx, query, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []schemaForeignKey
	for rows.Next() {
		var key schemaForeignKey
		if err := rows.Scan(&key.column, &key.parent, &key.parentColumn, &key.onDelete); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (d *DB) validateFutureLayout(ctx context.Context, q schemaReader) error {
	for table, expected := range compatibleCoreColumns {
		if _, err := d.matchColumns(ctx, q, table, expected); err != nil {
			return err
		}
	}
	triggerQuery := `SELECT count(*) FROM sqlite_master WHERE type = 'trigger'`
	if d.Backend == Postgres {
		triggerQuery = `SELECT count(*) FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid = t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE NOT t.tgisinternal AND n.nspname = current_schema()`
	}
	var triggers int
	if err := q.QueryRowContext(ctx, triggerQuery).Scan(&triggers); err != nil {
		return err
	}
	if triggers != 0 {
		return fmt.Errorf("unrecognized database triggers require a newer program")
	}
	tables, err := d.schemaTables(ctx, q)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if _, core := compatibleCoreColumns[table]; core {
			continue
		}
		keys, err := d.tableForeignKeys(ctx, q, table)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if _, core := compatibleCoreColumns[key.parent]; core && key.parent != "instances" {
				return fmt.Errorf("extension table %s references mutable core table %s; a newer program is required", table, key.parent)
			}
		}
	}
	return nil
}

func (d *DB) schemaTables(ctx context.Context, q schemaReader) ([]string, error) {
	query := `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`
	if d.Backend == Postgres {
		query = `SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = current_schema()`
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}
