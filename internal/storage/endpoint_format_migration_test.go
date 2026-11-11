package storage

import (
	"context"
	"testing"
)

func TestEndpointFormatMigration(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			ctx := context.Background()
			root, config, db := compatibilityDatabase(t, backend)
			prepareOldSchema(t, db, 14)
			populateMigrationFixture(t, db)
			compatibilityExec(t, db, `INSERT INTO provider_endpoints(id, display_name, base_url, api_key_source, api_key_ref, enabled, position) VALUES ('synthetic-endpoint','Synthetic','https://model.invalid/v1','manual','endpoint',1,0)`)
			compatibilityClose(t, db)
			db = compatibilityReopen(t, root, config)
			defer db.Close()
			assertCompatibilityVersions(t, db, 16, 16)
			assertMigrationFixture(t, db)
			var base, format string
			if err := db.SQL.QueryRow(`SELECT base_url,api_format FROM provider_endpoints WHERE id='synthetic-endpoint'`).Scan(&base, &format); err != nil || base != "https://model.invalid/v1" || format != "" {
				t.Fatalf("legacy endpoint changed: %s %s %v", base, format, err)
			}
			if err := db.RequireNativeSchema(ctx, "export complete backup"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresMigrationEndpointFormat(t *testing.T) {
	// Reuse the same assertions in the migration-only PostgreSQL CI job.
	config := postgresMigrationConfig(t)
	ctx := context.Background()
	root := t.TempDir()
	db, err := OpenWithConfig(ctx, root, config)
	if err != nil {
		t.Fatal(err)
	}
	prepareOldSchema(t, db, 14)
	compatibilityClose(t, db)
	db = compatibilityReopen(t, root, config)
	defer db.Close()
	assertCompatibilityVersions(t, db, 16, 16)
}
