package storage

import (
	"context"
	"database/sql"
	"errors"
)

// SQLConnection allows the same readers to run against a single consistent
// transaction during a complete backup. Normal operation uses *sql.DB.
type SQLConnection interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	Begin() (*sql.Tx, error)
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	Close() error
}

var errSnapshotWrite = errors.New("writes are not allowed in a backup snapshot")

type snapshotConnection struct{ *sql.Tx }

func (snapshotConnection) Exec(string, ...any) (sql.Result, error) { return nil, errSnapshotWrite }
func (snapshotConnection) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errSnapshotWrite
}
func (snapshotConnection) Begin() (*sql.Tx, error) { return nil, errSnapshotWrite }
func (snapshotConnection) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errSnapshotWrite
}
func (snapshotConnection) Close() error { return errSnapshotWrite }

// ReadSnapshot owns its connection until fn returns. The view must not escape
// the callback. Its mutexes are separate: database snapshot isolation supplies
// consistency, and it must never wait for a writer holding a live store lock.
func (d *DB) ReadSnapshot(ctx context.Context, fn func(*DB) error) error {
	if _, ok := d.SQL.(snapshotConnection); ok {
		return fn(d)
	}
	tx, err := d.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	view := &DB{SQL: snapshotConnection{tx}, Backend: d.Backend}
	view.EmbeddingStorageReady.Store(d.EmbeddingStorageReady.Load())
	if err := fn(view); err != nil {
		return err
	}
	return tx.Commit()
}
