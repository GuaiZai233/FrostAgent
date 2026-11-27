// Package sqlfault injects transaction failures into real SQL-backed tests.
package sqlfault

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

type Fault struct {
	Commits      atomic.Int32
	FailAt       int32
	FailReadback bool
	AfterCommit  func()
	blocked      atomic.Bool
}

// Install wraps the existing driver and opens the same synthetic database.
// Configure the returned fault before sending requests; do not use this helper
// while another goroutine is using the DB handle.
func Install(t testing.TB, db *storage.DB, dsn string) *Fault {
	t.Helper()
	previous := db.SQL
	fault := &Fault{FailAt: 1}
	db.SQL = sql.OpenDB(&connector{driver: previous.Driver(), dsn: dsn, fault: fault})
	db.SQL.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.SQL.Close()
		db.SQL = previous
	})
	return fault
}

type connector struct {
	driver driver.Driver
	dsn    string
	fault  *Fault
}

func (c *connector) Driver() driver.Driver { return c.driver }
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &connection{Conn: conn, fault: c.fault}, nil
}

type connection struct {
	driver.Conn
	fault *Fault
}

func (c *connection) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *connection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	var tx driver.Tx
	var err error
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = conn.BeginTx(ctx, opts)
	} else {
		tx, err = c.Conn.Begin()
	}
	if err != nil {
		return nil, err
	}
	return &transaction{Tx: tx, fault: c.fault}, nil
}
func (c *connection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.fault.blocked.Load() {
		return nil, errors.New("synthetic database unavailable during readback")
	}
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}
func (c *connection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

type transaction struct {
	driver.Tx
	fault *Fault
}

func (t *transaction) Commit() error {
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	if t.fault.Commits.Add(1) == t.fault.FailAt {
		t.fault.blocked.Store(t.fault.FailReadback)
		if t.fault.AfterCommit != nil {
			t.fault.AfterCommit()
		}
		return errors.New("synthetic COMMIT acknowledgement lost")
	}
	return nil
}
