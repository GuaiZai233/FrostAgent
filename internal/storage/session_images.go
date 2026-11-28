package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"
)

// SessionImage is a conversation image kept for console previews.
type SessionImage struct {
	ContentType string
	Data        []byte
}

// PutSessionImage stores an image and evicts the least recently used images of
// the instance until their total decoded size fits maxTotalBytes. An existing
// key only has its last-used time refreshed.
func (d *DB) PutSessionImage(ctx context.Context, instanceID, key, contentType string, data []byte, maxTotalBytes int64) error {
	now := time.Now().UnixMilli()
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, d.Bind(`UPDATE session_images SET last_used_at = ? WHERE instance_id = ? AND key = ?`), now, instanceID, key)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err == nil && count > 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO session_images(instance_id, key, content_type, size, data, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (instance_id, key) DO UPDATE SET last_used_at = excluded.last_used_at`),
		instanceID, key, contentType, int64(len(data)), base64.StdEncoding.EncodeToString(data), now, now); err != nil {
		return err
	}
	if maxTotalBytes > 0 {
		var total int64
		if err := tx.QueryRowContext(ctx, d.Bind(`SELECT COALESCE(SUM(size), 0) FROM session_images WHERE instance_id = ?`), instanceID).Scan(&total); err != nil {
			return err
		}
		for total > maxTotalBytes {
			var oldest string
			var size int64
			err := tx.QueryRowContext(ctx, d.Bind(`SELECT key, size FROM session_images WHERE instance_id = ? AND key <> ?
				ORDER BY last_used_at ASC, created_at ASC LIMIT 1`), instanceID, key).Scan(&oldest, &size)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, d.Bind(`DELETE FROM session_images WHERE instance_id = ? AND key = ?`), instanceID, oldest); err != nil {
				return err
			}
			total -= size
		}
	}
	return tx.Commit()
}

// GetSessionImage returns a stored image and refreshes its last-used time.
func (d *DB) GetSessionImage(ctx context.Context, instanceID, key string) (SessionImage, bool, error) {
	var image SessionImage
	var encoded string
	err := d.SQL.QueryRowContext(ctx, d.Bind(`SELECT content_type, data FROM session_images WHERE instance_id = ? AND key = ?`), instanceID, key).
		Scan(&image.ContentType, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionImage{}, false, nil
	}
	if err != nil {
		return SessionImage{}, false, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return SessionImage{}, false, err
	}
	image.Data = data
	_, _ = d.SQL.ExecContext(ctx, d.Bind(`UPDATE session_images SET last_used_at = ? WHERE instance_id = ? AND key = ?`), time.Now().UnixMilli(), instanceID, key)
	return image, true, nil
}

// TouchSessionImage refreshes an image's last-used time.
func (d *DB) TouchSessionImage(ctx context.Context, instanceID, key string) error {
	_, err := d.SQL.ExecContext(ctx, d.Bind(`UPDATE session_images SET last_used_at = ? WHERE instance_id = ? AND key = ?`), time.Now().UnixMilli(), instanceID, key)
	return err
}

// HasSessionImage reports whether an image is stored without loading its bytes.
func (d *DB) HasSessionImage(ctx context.Context, instanceID, key string) (bool, error) {
	var one int
	err := d.SQL.QueryRowContext(ctx, d.Bind(`SELECT 1 FROM session_images WHERE instance_id = ? AND key = ?`), instanceID, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
