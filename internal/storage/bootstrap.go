package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Bootstrap keeps only the database selection in the default local SQLite
// database. Application settings and instance data live in the selected DB.
type Bootstrap struct {
	db *sql.DB
}

func OpenBootstrap(ctx context.Context, dataDir string) (*Bootstrap, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", filepath.Join(dataDir, "frostagent.db"))
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = 10000"); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS storage_selection (
		id INTEGER PRIMARY KEY CHECK (id = 1), backend TEXT NOT NULL, dsn TEXT NOT NULL
	)`); err != nil {
		conn.Close()
		return nil, err
	}
	return &Bootstrap{db: conn}, nil
}

func (b *Bootstrap) Close() error {
	if b == nil || b.db == nil {
		return nil
	}
	return b.db.Close()
}

func NormalizeConfig(config Config) (Config, error) {
	config.Backend = Backend(strings.ToLower(strings.TrimSpace(string(config.Backend))))
	config.DSN = strings.TrimSpace(config.DSN)
	if config.Backend == "" {
		config.Backend = SQLite
	}
	switch config.Backend {
	case SQLite:
		config.DSN = ""
	case Postgres:
		if config.DSN == "" {
			return Config{}, errors.New("PostgreSQL connection address is required")
		}
	default:
		return Config{}, fmt.Errorf("unsupported database backend %q", config.Backend)
	}
	return config, nil
}

func (b *Bootstrap) Load(ctx context.Context) (Config, error) {
	var config Config
	err := b.db.QueryRowContext(ctx, `SELECT backend, dsn FROM storage_selection WHERE id = 1`).Scan(&config.Backend, &config.DSN)
	if errors.Is(err, sql.ErrNoRows) {
		return Config{Backend: SQLite}, nil
	}
	if err != nil {
		return Config{}, err
	}
	return NormalizeConfig(config)
}

func (b *Bootstrap) Save(ctx context.Context, config Config) error {
	var err error
	config, err = NormalizeConfig(config)
	if err != nil {
		return err
	}
	_, err = b.db.ExecContext(ctx, `INSERT INTO storage_selection(id, backend, dsn)
		VALUES (1, ?, ?) ON CONFLICT(id) DO UPDATE SET backend = excluded.backend, dsn = excluded.dsn`,
		config.Backend, config.DSN)
	return err
}
