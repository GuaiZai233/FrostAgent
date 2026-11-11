package storage

import (
	"context"
	"testing"
)

func TestPostgresMigrationSchema16CompatibilityBuild(t *testing.T) {
	ctx := context.Background()
	config := postgresMigrationConfig(t)
	root := t.TempDir()
	db, err := OpenWithConfig(ctx, root, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO provider_endpoints(id,display_name,base_url,api_key_source,enabled,position,api_format) VALUES ('synthetic-endpoint','Synthetic','https://relay.invalid','manual',1,0,'anthropic_messages')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`UPDATE schema_meta SET version=16,min_compatible_version=16 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenWithConfig(ctx, root, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	var format string
	if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&version); err != nil || version != 16 {
		t.Fatal("v15 build downgraded v16")
	}
	if err := db.SQL.QueryRow(`SELECT api_format FROM provider_endpoints WHERE id='synthetic-endpoint'`).Scan(&format); err != nil || format != "anthropic_messages" {
		t.Fatal("new endpoint metadata lost")
	}
	if err := db.RequireNativeSchema(ctx, "export complete backup"); err != nil {
		t.Fatal(err)
	}
}
