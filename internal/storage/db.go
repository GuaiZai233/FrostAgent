package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const SchemaVersion = 3

type Backend string

const (
	SQLite   Backend = "sqlite"
	Postgres Backend = "postgres"
)

type DB struct {
	SQL     *sql.DB
	Backend Backend
	lock    *sql.Conn
}

func Open(ctx context.Context, dataDir string) (*DB, error) {
	backend := Backend(strings.ToLower(strings.TrimSpace(os.Getenv("FROSTAGENT_DB_DRIVER"))))
	if backend == "" {
		backend = SQLite
	}
	dsn := strings.TrimSpace(os.Getenv("FROSTAGENT_DB_DSN"))
	var driver string
	switch backend {
	case SQLite:
		driver = "sqlite"
		if dsn == "" {
			dsn = filepath.Join(dataDir, "frostagent.db")
		}
		if err := os.MkdirAll(filepath.Dir(dsn), 0700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	case Postgres:
		driver = "pgx"
		if dsn == "" {
			return nil, errors.New("FROSTAGENT_DB_DSN is required for PostgreSQL")
		}
	default:
		return nil, fmt.Errorf("unsupported database backend %q", backend)
	}
	conn, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	conn.SetConnMaxLifetime(0)
	if err = conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	db := &DB{SQL: conn, Backend: backend}
	if backend == SQLite {
		for _, pragma := range []string{"PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 10000"} {
			if _, err = conn.ExecContext(ctx, pragma); err != nil {
				conn.Close()
				return nil, fmt.Errorf("configure SQLite: %w", err)
			}
		}
	} else {
		conn.SetMaxOpenConns(2)
		db.lock, err = conn.Conn(ctx)
		if err != nil {
			conn.Close()
			return nil, err
		}
		var acquired bool
		if err = db.lock.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(736417141)").Scan(&acquired); err != nil || !acquired {
			db.Close()
			if err != nil {
				return nil, fmt.Errorf("acquire database lease: %w", err)
			}
			return nil, errors.New("another FrostAgent process is using this PostgreSQL database")
		}
	}
	if err = db.initSchema(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	if d.lock != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = d.lock.ExecContext(ctx, "SELECT pg_advisory_unlock(736417141)")
		cancel()
		_ = d.lock.Close()
	}
	return d.SQL.Close()
}

func (d *DB) Bind(query string) string {
	if d.Backend != Postgres {
		return query
	}
	var out strings.Builder
	n := 0
	for _, char := range query {
		if char == '?' {
			n++
			fmt.Fprintf(&out, "$%d", n)
		} else {
			out.WriteRune(char)
		}
	}
	return out.String()
}
