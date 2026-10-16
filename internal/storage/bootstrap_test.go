package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteOpenIgnoresCustomDSN(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "unexpected.db")
	db, err := OpenWithConfig(context.Background(), root, Config{Backend: SQLite, DSN: outside})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "frostagent.db")); err != nil {
		t.Fatalf("local SQLite database was not created: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("custom SQLite path was used: %v", err)
	}
}

func TestBootstrapRetainsPostgresAddressWhileSQLiteIsSelected(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bootstrap, err := OpenBootstrap(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	const address = "postgres://synthetic:synthetic@localhost/synthetic"
	if err := bootstrap.Save(ctx, Config{Backend: Postgres, DSN: address}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Save(ctx, Config{Backend: SQLite}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
	bootstrap, err = OpenBootstrap(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	config, err := bootstrap.Load(ctx)
	if err != nil || config.Backend != SQLite || config.DSN != "" {
		t.Fatalf("SQLite runtime configuration changed: %#v, %v", config, err)
	}
	saved, err := bootstrap.PostgresDSN(ctx)
	if err != nil || saved != address {
		t.Fatal("PostgreSQL address was lost after selecting SQLite and reopening bootstrap")
	}
	const edited = "postgres://synthetic:edited@localhost/synthetic"
	if err := bootstrap.Save(ctx, Config{Backend: SQLite, DSN: " " + edited + " "}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Save(ctx, Config{Backend: Postgres}); err == nil {
		t.Fatal("empty PostgreSQL address was accepted")
	}
	saved, err = bootstrap.PostgresDSN(ctx)
	if err != nil || saved != edited {
		t.Fatal("edited address was lost while SQLite was selected")
	}
}

func TestBootstrapKeepsDatabaseSelectionAcrossSchemaMigration(t *testing.T) {
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
	prepareOldSchema(t, db, 7)
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
