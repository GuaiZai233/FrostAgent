package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPostgresMigrationExternalInstanceModuleBlocksFullOperations(t *testing.T) {
	_, config, db := compatibilityDatabase(t, Postgres)
	defer db.Close()
	compatibilityExec(t, db, `INSERT INTO instances(id,name,created_at) VALUES ('synthetic-a','A','now')`)
	var coreSchema string
	if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&coreSchema); err != nil {
		t.Fatal(err)
	}
	_, external := compatibilityModuleSchema(t, config, fmt.Sprintf("fa_external_payload_%d", time.Now().UnixNano()))
	if _, err := external.Exec(`CREATE TABLE independent_module(instance_id TEXT PRIMARY KEY REFERENCES ` + compatibilityQualifiedTable(coreSchema, "instances") + `(id) ON DELETE CASCADE,payload TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := external.Exec(`INSERT INTO independent_module VALUES ('synthetic-a','opaque external data')`); err != nil {
		t.Fatal(err)
	}
	if err := db.validateFutureLayout(context.Background(), db.SQL); err != nil {
		t.Fatalf("safe in-place use blocked: %v", err)
	}
	if err := db.RequireNativeSchema(context.Background(), "export"); err == nil || !strings.Contains(err.Error(), "external module") {
		t.Fatalf("external module missing from full export guard: %v", err)
	}
	if err := db.PrepareDeletion(context.Background(), "synthetic-a", 1); err == nil {
		t.Fatal("unknown external module deletion accepted")
	}
	var payload string
	if err := external.QueryRow(`SELECT payload FROM independent_module`).Scan(&payload); err != nil || payload != "opaque external data" {
		t.Fatalf("external data changed: %v", err)
	}
}
