package storage

import (
	"context"
	"testing"
)

func TestBootstrapKeepsDatabaseSelectionAcrossSchemaRebuild(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bootstrap, err := OpenBootstrap(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	if err := bootstrap.Save(ctx, Config{Backend: Postgres, DSN: "postgres://synthetic:test@localhost/synthetic"}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Save(ctx, Config{Backend: Postgres}); err == nil {
		t.Fatal("empty PostgreSQL address was accepted")
	}
	if _, err := OpenWithConfig(ctx, root, Config{Backend: Postgres}); err == nil {
		t.Fatal("empty PostgreSQL address connected")
	}
	db, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `UPDATE schema_meta SET version = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	bootstrap.Close()
	bootstrap, err = OpenBootstrap(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	loaded, err := bootstrap.Load(ctx)
	if err != nil || loaded.Backend != Postgres || loaded.DSN != "postgres://synthetic:test@localhost/synthetic" {
		t.Fatalf("database selection was lost: %#v, %v", loaded, err)
	}
}
