package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// repairEmbeddingColumns reconciles the two development v12 layouts without
// changing an existing column or touching data owned by another feature.
// The caller advances the schema version in the same transaction.
func (d *DB) repairEmbeddingColumns(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct{ table, name string }{
		{"provider_models", "max_input_tokens"},
		{"memory_entries", "summary_dirty"},
	} {
		// Reading result metadata works on both SQLite and PostgreSQL and uses
		// the same search path as the ALTER. No application rows are fetched.
		rows, err := tx.QueryContext(ctx, "SELECT * FROM "+column.table+" WHERE 1=0")
		if err != nil {
			return fmt.Errorf("inspect embedding schema %s: %w", column.table, err)
		}
		names, err := rows.Columns()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		exists := false
		for _, name := range names {
			if name == column.name {
				exists = true
				break
			}
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE "+column.table+" ADD COLUMN "+column.name+" INTEGER NOT NULL DEFAULT 0"); err != nil {
				return fmt.Errorf("repair embedding schema %s.%s: %w", column.table, column.name, err)
			}
		}
	}
	return nil
}
