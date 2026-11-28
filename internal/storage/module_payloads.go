package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

const (
	ModulePayloadProtocol      = 1
	MaxModulePayloadBytes      = 32 << 20
	MaxModulePayloadTotalBytes = 128 << 20
	MaxModulePayloadRecords    = 10000
	modulePayloadMigration     = "opaque-instance-payloads/v1"
)

var ErrModulePayloadConflict = errors.New("module payload changed or has an unsupported format")
var modulePayloadID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

const modulePayloadSchema = `CREATE TABLE module_payloads (
	instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
	module_id TEXT NOT NULL, record_id TEXT NOT NULL, format_version INTEGER NOT NULL,
	payload_base64 TEXT NOT NULL, sha256 TEXT NOT NULL, requires_rebind INTEGER NOT NULL,
	PRIMARY KEY (instance_id, module_id, record_id)
)`

func createModulePayloads(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, modulePayloadSchema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS module_migrations (id TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO module_migrations(id) VALUES ('opaque-instance-payloads/v1')`)
	return err
}

// ModulePayload is an independent, instance-owned module record. Bytes must be
// eligible for backup: credentials belong in the protected credential stores.
// This envelope is frozen; future fields belong inside Data, not the envelope.
// Unknown formats are transported only and must never be automatically executed.
type ModulePayload struct {
	ModuleID       string `json:"module_id"`
	RecordID       string `json:"record_id"`
	FormatVersion  int    `json:"format_version"`
	Data           []byte `json:"data"`
	SHA256         string `json:"sha256"`
	RequiresRebind bool   `json:"requires_rebind"`
}

type ModulePayloadRevision struct {
	FormatVersion int
	SHA256        string
}

func (p ModulePayload) Revision() ModulePayloadRevision {
	return ModulePayloadRevision{p.FormatVersion, p.SHA256}
}

func ModulePayloadDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ValidateModulePayloads(records []ModulePayload) error {
	if len(records) > MaxModulePayloadRecords {
		return fmt.Errorf("too many module payload records")
	}
	seen := make(map[string]bool, len(records))
	total := 0
	for _, p := range records {
		if len(p.ModuleID) > 128 || len(p.RecordID) > 256 || !modulePayloadID.MatchString(p.ModuleID) || !modulePayloadID.MatchString(p.RecordID) {
			return fmt.Errorf("invalid module payload identity")
		}
		key := p.ModuleID + "\x00" + p.RecordID
		if seen[key] {
			return fmt.Errorf("duplicate module payload %s/%s", p.ModuleID, p.RecordID)
		}
		seen[key] = true
		if p.FormatVersion < 1 || p.FormatVersion > 2147483647 {
			return fmt.Errorf("invalid module payload format")
		}
		total += len(p.Data)
		if len(p.Data) > MaxModulePayloadBytes || total > MaxModulePayloadTotalBytes {
			return fmt.Errorf("module payload exceeds size limit")
		}
		if p.SHA256 != ModulePayloadDigest(p.Data) {
			return fmt.Errorf("module payload checksum mismatch: %s/%s", p.ModuleID, p.RecordID)
		}
	}
	return nil
}

func (d *DB) ModulePayloads(ctx context.Context, instanceID string) ([]ModulePayload, error) {
	rows, err := d.SQL.QueryContext(ctx, d.Bind(`SELECT module_id,record_id,format_version,payload_base64,sha256,requires_rebind FROM module_payloads WHERE instance_id=? ORDER BY module_id,record_id`), instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []ModulePayload{}
	total := 0
	for rows.Next() {
		var p ModulePayload
		var encoded string
		var rebind int
		if err := rows.Scan(&p.ModuleID, &p.RecordID, &p.FormatVersion, &encoded, &p.SHA256, &rebind); err != nil {
			return nil, err
		}
		if len(encoded) > base64.StdEncoding.EncodedLen(MaxModulePayloadBytes) {
			return nil, fmt.Errorf("module payload exceeds size limit")
		}
		p.Data, err = base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("invalid module payload encoding: %w", err)
		}
		if rebind != 0 && rebind != 1 {
			return nil, fmt.Errorf("invalid module payload binding state")
		}
		p.RequiresRebind = rebind == 1
		records = append(records, p)
		total += len(p.Data)
		if len(records) > MaxModulePayloadRecords || total > MaxModulePayloadTotalBytes {
			return nil, fmt.Errorf("module payload exceeds size limit")
		}
		if err := ValidateModulePayloads([]ModulePayload{p}); err != nil {
			return nil, err
		}
	}
	return records, rows.Err()
}

// SaveModulePayload is insert-only when expected is nil, otherwise a conditional
// update. Callers supply the format they understand, not a token blindly copied
// from an unknown record. Downgrades are forbidden. No bulk replace API exists.
// A restored record stays unbound even when a writer supplies false.
func (d *DB) SaveModulePayload(ctx context.Context, instanceID string, p ModulePayload, expected *ModulePayloadRevision) error {
	if err := ValidateModulePayloads([]ModulePayload{p}); err != nil {
		return err
	}
	tx, err := d.beginModulePayloadWrite(ctx, instanceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count, size int64
	if err := tx.QueryRowContext(ctx, d.Bind(`SELECT COUNT(*),COALESCE(SUM((LENGTH(REPLACE(payload_base64,'=',''))*3)/4),0) FROM module_payloads WHERE instance_id=? AND NOT (module_id=? AND record_id=?)`), instanceID, p.ModuleID, p.RecordID).Scan(&count, &size); err != nil {
		return err
	}
	if count >= MaxModulePayloadRecords || size+int64(len(p.Data)) > MaxModulePayloadTotalBytes {
		return fmt.Errorf("module payload exceeds size limit")
	}
	encoded := base64.StdEncoding.EncodeToString(p.Data)
	rebind := 0
	if p.RequiresRebind {
		rebind = 1
	}
	var result sql.Result
	if expected == nil {
		result, err = tx.ExecContext(ctx, d.Bind(`INSERT INTO module_payloads(instance_id,module_id,record_id,format_version,payload_base64,sha256,requires_rebind) VALUES (?,?,?,?,?,?,?) ON CONFLICT(instance_id,module_id,record_id) DO NOTHING`), instanceID, p.ModuleID, p.RecordID, p.FormatVersion, encoded, p.SHA256, rebind)
	} else {
		if expected.FormatVersion < 1 || p.FormatVersion < expected.FormatVersion {
			return ErrModulePayloadConflict
		}
		result, err = tx.ExecContext(ctx, d.Bind(`UPDATE module_payloads SET format_version=?,payload_base64=?,sha256=?,requires_rebind=CASE WHEN requires_rebind=1 THEN 1 ELSE ? END WHERE instance_id=? AND module_id=? AND record_id=? AND format_version=? AND sha256=?`), p.FormatVersion, encoded, p.SHA256, rebind, instanceID, p.ModuleID, p.RecordID, expected.FormatVersion, expected.SHA256)
	}
	if err := modulePayloadResult(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

// RebindModulePayload must only be called after a module understands this exact
// format, validates its data, and obtains its normal execution authorization.
func (d *DB) RebindModulePayload(ctx context.Context, instanceID, moduleID, recordID string, expected ModulePayloadRevision) error {
	result, err := d.SQL.ExecContext(ctx, d.Bind(`UPDATE module_payloads SET requires_rebind=0 WHERE instance_id=? AND module_id=? AND record_id=? AND format_version=? AND sha256=?`), instanceID, moduleID, recordID, expected.FormatVersion, expected.SHA256)
	return modulePayloadResult(result, err)
}

// DeleteModulePayload retires one understood record after the caller's normal
// deletion authorization. Instance deletion remains the only all-module delete.
func (d *DB) DeleteModulePayload(ctx context.Context, instanceID, moduleID, recordID string, expected ModulePayloadRevision) error {
	result, err := d.SQL.ExecContext(ctx, d.Bind(`DELETE FROM module_payloads WHERE instance_id=? AND module_id=? AND record_id=? AND format_version=? AND sha256=?`), instanceID, moduleID, recordID, expected.FormatVersion, expected.SHA256)
	return modulePayloadResult(result, err)
}

func modulePayloadResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrModulePayloadConflict
	}
	return nil
}

// RestoreModulePayloads inserts into an empty target instance. It cannot replace
// existing records, and the backup's binding flag is deliberately not trusted.
func (d *DB) RestoreModulePayloads(ctx context.Context, instanceID string, records []ModulePayload) error {
	if err := ValidateModulePayloads(records); err != nil {
		return err
	}
	tx, err := d.beginModulePayloadWrite(ctx, instanceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	if err := tx.QueryRowContext(ctx, d.Bind(`SELECT COUNT(*) FROM module_payloads WHERE instance_id=?`), instanceID).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return fmt.Errorf("module restore requires an empty target")
	}
	for _, p := range records {
		if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO module_payloads(instance_id,module_id,record_id,format_version,payload_base64,sha256,requires_rebind) VALUES (?,?,?,?,?,?,1)`), instanceID, p.ModuleID, p.RecordID, p.FormatVersion, base64.StdEncoding.EncodeToString(p.Data), p.SHA256); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) beginModulePayloadWrite(ctx context.Context, instanceID string) (*sql.Tx, error) {
	if d.Backend != Postgres {
		return d.SQL.BeginTx(ctx, nil)
	}
	// Serialize saves and restores for this instance. Read committed gives the
	// subsequent quota check a fresh snapshot after the previous writer commits.
	tx, err := d.SQL.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	var id string
	if err := tx.QueryRowContext(ctx, d.Bind(`SELECT id FROM instances WHERE id=? FOR NO KEY UPDATE`), instanceID).Scan(&id); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
