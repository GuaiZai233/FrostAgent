package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (d *DB) PrepareDeletion(ctx context.Context, instanceID string, backupVersion int) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, d.Bind(`UPDATE instances SET enabled = 0, deleting = 1 WHERE id = ? AND deleting = 0`), instanceID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return err
		}
		return fmt.Errorf("instance is missing or already pending deletion")
	}
	if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO pending_deletions(instance_id, prepared_at, backup_version)
		VALUES (?, ?, ?)`), instanceID, time.Now().UTC().Format(time.RFC3339Nano), backupVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) CancelDeletion(ctx context.Context, instanceID string) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, d.Bind(`DELETE FROM pending_deletions WHERE instance_id = ?`), instanceID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, d.Bind(`UPDATE instances SET deleting = 0, enabled = 0 WHERE id = ?`), instanceID); err != nil {
		return err
	}
	return tx.Commit()
}

// ConfirmDeletion removes SQL-owned instance data after the caller has staged
// its filesystem directory for rollback. A missing pending marker is fatal.
func (d *DB) ConfirmDeletion(ctx context.Context, instanceID string) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, d.Bind(`SELECT backup_version FROM pending_deletions WHERE instance_id = ?`), instanceID).Scan(&version); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, d.Bind(`DELETE FROM settings WHERE scope = 'instance' AND instance_id = ?`), instanceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, d.Bind(`DELETE FROM security_audit WHERE instance_id = ?`), instanceID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, d.Bind(`DELETE FROM instances WHERE id = ? AND deleting = 1`), instanceID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return tx.Commit()
}
