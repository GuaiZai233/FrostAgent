package storage

import (
	"context"
	"database/sql"
	"fmt"
)

const endpointMetadataMigration = "provider-endpoint-api-format/v1"
const computerUseMetadataMigration = "computer-use-metadata/v1"

var compatibleComputerUseColumns = map[string]string{
	"computer_use_config":   "instance_id=text config_json=text",
	"computer_use_projects": "instance_id=text id=text metadata_json=text",
	"computer_use_tasks":    "instance_id=text id=text generation=bigint metadata_json=text",
}

var computerUseSchema = []string{
	`CREATE TABLE IF NOT EXISTS computer_use_config (instance_id TEXT PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE, config_json TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS computer_use_projects (instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE, id TEXT NOT NULL, metadata_json TEXT NOT NULL, PRIMARY KEY(instance_id,id))`,
	`CREATE TABLE IF NOT EXISTS computer_use_tasks (instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE, id TEXT NOT NULL, generation BIGINT NOT NULL, metadata_json TEXT NOT NULL, PRIMARY KEY(instance_id,id))`,
}

func (d *DB) ensureEndpointMetadataTx(ctx context.Context, tx *sql.Tx) error {
	columns, err := d.tableColumns(ctx, tx, "provider_endpoints")
	if err != nil {
		return err
	}
	found := false
	for _, column := range columns {
		if column.name == "api_format" {
			if column.kind != "text" || !column.notNull {
				return fmt.Errorf("invalid endpoint API format column")
			}
			found = true
		}
	}
	if !found {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE provider_endpoints ADD COLUMN api_format TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

// Module migrations have stable IDs independent of the core schema cursor.
// The v15 compatibility build can install these without renumbering its core.
func (d *DB) ensureCompatibilityModules(ctx context.Context) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS module_migrations (id TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	if err := d.ensureEndpointMetadataTx(ctx, tx); err != nil {
		return err
	}
	for _, statement := range computerUseSchema {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for table, expected := range compatibleComputerUseColumns {
		if _, err := d.matchColumns(ctx, tx, table, expected); err != nil {
			return err
		}
	}
	for _, id := range []string{endpointMetadataMigration, computerUseMetadataMigration} {
		if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO module_migrations(id) VALUES (?) ON CONFLICT(id) DO NOTHING`), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
