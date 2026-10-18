package storage

import (
	"context"
	"fmt"
	"strings"
)

// Existing derived rows need transactional invalidation even when Embedding is
// disabled after a restart. Catalog lookup neither requires nor installs pgvector.
func (d *DB) detectEmbeddingStorage(ctx context.Context) error {
	if d.Backend != Postgres {
		return nil
	}
	var exists bool
	if err := d.SQL.QueryRowContext(ctx, `SELECT pg_catalog.to_regclass('memory_embeddings') IS NOT NULL`).Scan(&exists); err != nil {
		return fmt.Errorf("detect existing embedding storage: %w", err)
	}
	d.EmbeddingStorageReady.Store(exists)
	return nil
}

// EnsureEmbeddingStorage never installs extensions. Ordinary SQL startup does
// not depend on pgvector; this schema is created only when Embedding is enabled.
func (d *DB) EnsureEmbeddingStorage(ctx context.Context) (string, error) {
	if d == nil || d.Backend != Postgres {
		return "", nil
	}
	d.ModelRouterMu.Lock()
	defer d.ModelRouterMu.Unlock()
	var namespace string
	if err := d.SQL.QueryRowContext(ctx, `SELECT n.nspname FROM pg_extension e
		JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'vector'`).Scan(&namespace); err != nil {
		return "", fmt.Errorf("启用 Embedding 需要部署者预先安装并启用 pgvector: %w", err)
	}
	vectorType := `"` + strings.ReplaceAll(namespace, `"`, `""`) + `".vector`
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS memory_embedding_scopes (
			instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
			scope_type TEXT NOT NULL, platform TEXT NOT NULL, group_id TEXT NOT NULL, owner TEXT NOT NULL,
			fingerprint TEXT NOT NULL,source_hash TEXT NOT NULL DEFAULT '', PRIMARY KEY(instance_id,scope_type,platform,group_id,owner)
		)`,
		`CREATE TABLE IF NOT EXISTS memory_embeddings (
			instance_id TEXT NOT NULL, scope_type TEXT NOT NULL, platform TEXT NOT NULL,
			group_id TEXT NOT NULL, owner TEXT NOT NULL, memory_id TEXT NOT NULL,
			content_hash TEXT NOT NULL, dimensions INTEGER NOT NULL, embedding ` + vectorType + ` NOT NULL,
			PRIMARY KEY(instance_id,scope_type,platform,group_id,owner,memory_id),
			FOREIGN KEY(instance_id,scope_type,platform,group_id,owner)
				REFERENCES memory_embedding_scopes(instance_id,scope_type,platform,group_id,owner) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	d.EmbeddingStorageReady.Store(true)
	return vectorType, nil
}
