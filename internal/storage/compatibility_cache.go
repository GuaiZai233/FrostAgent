package storage

import (
	"context"
	"fmt"
)

// Optional PostgreSQL caches are still writer-owned: publication replaces their
// rows, and queries rely on the vector type/operators. A future schema must not
// turn their existence alone into a promise that the old writer can use them.
func (d *DB) validateEmbeddingCacheLayout(ctx context.Context, q schemaReader) error {
	for _, contract := range []struct{ table, columns string }{
		{"memory_embedding_scopes", "instance_id=text scope_type=text platform=text group_id=text owner=text fingerprint=text source_hash=text"},
		{"memory_embeddings", "instance_id=text scope_type=text platform=text group_id=text owner=text memory_id=text content_hash=text dimensions=integer embedding=vector"},
	} {
		var exists bool
		if err := q.QueryRowContext(ctx, `SELECT pg_catalog.to_regclass($1) IS NOT NULL`, contract.table).Scan(&exists); err != nil {
			return err
		}
		// Neither table is required until Embedding is enabled. A surviving
		// scopes table is also valid; EnsureEmbeddingStorage can add vectors.
		if !exists {
			continue
		}
		columns, err := d.tableColumns(ctx, q, contract.table)
		if err != nil {
			return err
		}
		if contract.table == "memory_embeddings" {
			var nativeVector bool
			// Resolve the extension-owned type by OID, not its display name or
			// search_path. Fixed vector dimensions would reject a model change.
			if err := q.QueryRowContext(ctx, `SELECT EXISTS (
				SELECT 1 FROM pg_catalog.pg_attribute a
				JOIN pg_catalog.pg_type t ON t.oid = a.atttypid
				JOIN pg_catalog.pg_depend dep ON dep.classid = 'pg_catalog.pg_type'::regclass
					AND dep.objid = t.oid AND dep.refclassid = 'pg_catalog.pg_extension'::regclass AND dep.deptype = 'e'
				JOIN pg_catalog.pg_extension e ON e.oid = dep.refobjid
				WHERE a.attrelid = pg_catalog.to_regclass('memory_embeddings') AND a.attname = 'embedding'
				AND NOT a.attisdropped AND a.atttypmod = -1 AND t.typname = 'vector' AND e.extname = 'vector'
			)`).Scan(&nativeVector); err != nil {
				return err
			}
			if !nativeVector {
				return fmt.Errorf("table memory_embeddings column embedding requires the native pgvector type without fixed dimensions")
			}
			for i := range columns {
				if columns[i].name == "embedding" {
					// Normalize only after checking type identity; qualified type
					// names can contain spaces and depend on the current path.
					columns[i].kind = "vector"
				}
			}
		}
		if err := matchColumnLayout(contract.table, columns, contract.columns); err != nil {
			return err
		}
		if err := d.validateWriterConstraints(ctx, q, contract.table, columns); err != nil {
			return err
		}
	}
	return nil
}
