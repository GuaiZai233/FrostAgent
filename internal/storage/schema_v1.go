package storage

import (
	"context"
	"fmt"
	"strings"
)

// v18 is the v1 relational baseline. Removal of these inactive legacy values
// was explicitly approved without archival. Active provider data was migrated
// by v11; never copy the stale provider archives back over the active catalog.
var v1CleanupSchema = []string{
	`DROP TABLE IF EXISTS models`,
	`DROP TABLE IF EXISTS model_endpoints`,
	`DROP TABLE IF EXISTS model_secrets`,
	`DROP TABLE IF EXISTS endpoint_ids`,
	`ALTER TABLE dialogues DROP COLUMN scene`,
	`ALTER TABLE dialogues DROP COLUMN relation`,
	`ALTER TABLE memory_entries DROP COLUMN visibility`,
}

func (d *DB) validateV1Cleanup(ctx context.Context, q schemaReader) error {
	retired := map[string]bool{"models": true, "model_endpoints": true, "model_secrets": true, "endpoint_ids": true}
	tables, err := d.schemaTables(ctx, q)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if retired[table] {
			continue
		}
		keys, err := d.tableForeignKeys(ctx, q, table)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if retired[key.parent] {
				return fmt.Errorf("v1 cleanup cannot remove %s: unknown dependent table %s", key.parent, table)
			}
		}
	}
	return nil
}

// Derive fresh v1 definitions without mutating the released v7 migration source
// or ever creating obsolete columns. Subsequent additive migrations retain the
// same column order as upgraded databases.
func cleanV1Statement(statement string) string {
	if strings.HasPrefix(statement, "CREATE TABLE dialogues (") {
		statement = strings.Replace(statement, "scene TEXT NOT NULL DEFAULT '',", "", 1)
		statement = strings.Replace(statement, "relation TEXT NOT NULL DEFAULT '',", "", 1)
	}
	if strings.HasPrefix(statement, "CREATE TABLE memory_entries (") {
		statement = strings.Replace(statement, "visibility TEXT NOT NULL DEFAULT '',", "", 1)
	}
	return statement
}

func legacyCoreColumns() map[string]string {
	columns := make(map[string]string, len(compatibleCoreColumns))
	for table, layout := range compatibleCoreColumns {
		columns[table] = layout
	}
	columns["dialogues"] = "instance_id=text position=integer id=text scene=text relation=text user_text=text preferred=text"
	columns["memory_entries"] = strings.Replace(columns["memory_entries"], "source=text ", "source=text visibility=text ", 1)
	delete(columns, "module_payloads")
	return columns
}
