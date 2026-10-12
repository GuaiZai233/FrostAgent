package groupsummary

import (
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
)

func TestSQLSummaryPersistsAndDeletes(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "summaries.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLStore(db, "test-instance")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Upsert("qq:group:test-group", "summary", 0); err != nil || !ok {
		t.Fatalf("upsert: %v, %v", ok, err)
	}
	reopened, err := NewSQLStore(db, "test-instance")
	if err != nil {
		t.Fatal(err)
	}
	if record, ok, err := reopened.Get("group:test-group"); err != nil || !ok || record.Summary != "summary" {
		t.Fatalf("SQL summary not recovered: %#v, %v, %v", record, ok, err)
	}
	if err := reopened.Delete("group:test-group"); err != nil {
		t.Fatal(err)
	}
	final, err := NewSQLStore(db, "test-instance")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := final.Get("group:test-group"); err != nil || ok {
		t.Fatalf("deleted summary remained: %v, %v", ok, err)
	}
}
