package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type InstanceRecord struct {
	ID                string
	Name              string
	CreatedAt         time.Time
	Enabled           bool
	Deleting          bool
	Error             string
	CredentialTargets []string
}

func (d *DB) LoadInstances(ctx context.Context) ([]InstanceRecord, int, error) {
	rows, err := d.SQL.QueryContext(ctx, `SELECT id, name, created_at, enabled, deleting,
		error, credential_targets_json FROM instances ORDER BY created_at, id`)
	if err != nil {
		return nil, 0, err
	}
	var records []InstanceRecord
	for rows.Next() {
		var record InstanceRecord
		var created, targets string
		var enabled, deleting int
		if err := rows.Scan(&record.ID, &record.Name, &created, &enabled, &deleting,
			&record.Error, &targets); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if record.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(targets), &record.CredentialTargets); err != nil {
			rows.Close()
			return nil, 0, err
		}
		record.Enabled, record.Deleting = enabled != 0, deleting != 0
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	var next int
	err = d.SQL.QueryRowContext(ctx, `SELECT value FROM counters WHERE key = 'next_instance_number'`).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return records, 1, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if next < 1 {
		return nil, 0, fmt.Errorf("invalid next instance number %d", next)
	}
	return records, next, nil
}

func (d *DB) CreateInstance(ctx context.Context, record InstanceRecord, nextNumber int) error {
	if record.ID == "" || nextNumber < 1 {
		return fmt.Errorf("invalid instance ID or next number")
	}
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO instances
		(id, name, created_at, enabled, deleting, error, credential_targets_json)
		VALUES (?, ?, ?, 0, 0, '', '[]')`), record.ID, record.Name,
		record.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, d.Bind(`INSERT INTO counters(key, value)
		VALUES ('next_instance_number', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`), nextNumber); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) UpdateInstance(ctx context.Context, record InstanceRecord) error {
	targets, err := json.Marshal(record.CredentialTargets)
	if err != nil {
		return err
	}
	enabled, deleting := 0, 0
	if record.Enabled {
		enabled = 1
	}
	if record.Deleting {
		deleting = 1
	}
	result, err := d.SQL.ExecContext(ctx, d.Bind(`UPDATE instances SET name = ?, enabled = ?, deleting = ?,
		error = ?, credential_targets_json = ? WHERE id = ?`), record.Name, enabled, deleting,
		record.Error, string(targets), record.ID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (d *DB) LoadEndpointOwners(ctx context.Context) (map[string]string, error) {
	rows, err := d.SQL.QueryContext(ctx, `SELECT id, instance_id FROM endpoint_ids`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := make(map[string]string)
	for rows.Next() {
		var id, owner string
		if err := rows.Scan(&id, &owner); err != nil {
			return nil, err
		}
		owners[id] = owner
	}
	return owners, rows.Err()
}

func (d *DB) ReserveEndpoint(ctx context.Context, id, instanceID string) error {
	_, err := d.SQL.ExecContext(ctx, d.Bind(`INSERT INTO endpoint_ids(id, instance_id)
		VALUES (?, ?) ON CONFLICT (id) DO NOTHING`), id, instanceID)
	if err != nil {
		return err
	}
	var owner string
	if err := d.SQL.QueryRowContext(ctx, d.Bind(`SELECT instance_id FROM endpoint_ids WHERE id = ?`), id).Scan(&owner); err != nil {
		return err
	}
	if owner != instanceID {
		return fmt.Errorf("endpoint ID %s belongs to another instance", id)
	}
	return nil
}
