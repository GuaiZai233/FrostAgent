package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestLegacySQLMemoryScopesSurviveRestartWithoutMigration(t *testing.T) {
	root := t.TempDir()
	config := storage.Config{Backend: storage.SQLite, DSN: filepath.Join(root, "memory.db")}
	db, err := storage.OpenWithConfig(context.Background(), root, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	const instance = "synthetic-scope-instance"
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('synthetic-scope-instance','Test','now')`); err != nil {
		t.Fatal(err)
	}
	store := NewSQLStore(db, instance)
	if err := store.Save(MemoryEntry{ID: "legacy-user", Owner: "synthetic-user", Content: "user fact", Source: SourceManual}); err != nil {
		t.Fatal(err)
	}
	manager := NewSQLGroupManager(db, instance, nil)
	group, err := manager.GetGroupStoreForPlatform("qq", "synthetic-group")
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Save(MemoryEntry{ID: "legacy-group", Content: "group fact", Source: SourceDistill, SourceSenderID: "synthetic-sender", SourceMessageID: "synthetic-message"}); err != nil {
		t.Fatal(err)
	}
	// Simulate rows written by the previous implementation, including a member owner.
	if _, err := db.SQL.Exec(`UPDATE memory_entries SET visibility='public', owner='synthetic-sender', owner_type='user' WHERE scope_type='group'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`UPDATE memory_entries SET visibility='public' WHERE scope_type='private'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.OpenWithConfig(context.Background(), root, config)
	if err != nil {
		t.Fatal(err)
	}
	store = NewSQLStore(db, instance)
	manager = NewSQLGroupManager(db, instance, nil)
	group, err = manager.GetGroupStoreForPlatform("onebot", "synthetic-group")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := group.ListAll()
	if err != nil || len(entries) != 1 {
		t.Fatalf("group records lost: %#v %v", entries, err)
	}
	entry := entries[0]
	if entry.Owner != GroupOwnerExplicit || entry.OwnerType != OwnerGroup || entry.ScopeType != ScopeGroup || entry.GroupID != "synthetic-group" || entry.SourceSenderID != "synthetic-sender" || entry.SourceMessageID != "synthetic-message" {
		t.Fatalf("legacy group scope/provenance changed: %#v", entry)
	}
	if err := group.RecordRecall(entries); err != nil {
		t.Fatal(err)
	}
	users, err := store.ListAll()
	if err != nil || len(users) != 1 || users[0].Owner != "synthetic-user" || users[0].AccessCount != 0 {
		t.Fatalf("group memory moved into user scope: %#v %v", users, err)
	}
	if got := NewGateway().FilterPrivate(users, "synthetic-sender"); len(got) != 0 {
		t.Fatalf("public granted extra access: %#v", got)
	}
	raw, err := json.Marshal(append(users, entries...))
	if err != nil || strings.Contains(string(raw), "visibility") {
		t.Fatalf("retired field exported: %s %v", raw, err)
	}
	var version int
	if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&version); err != nil || version != 16 {
		t.Fatalf("schema version changed: %d %v", version, err)
	}
	var legacyColumn string
	if err := db.SQL.QueryRow(`SELECT visibility FROM memory_entries WHERE id='legacy-user'`).Scan(&legacyColumn); err != nil || legacyColumn != "public" {
		t.Fatalf("legacy physical column changed: %q %v", legacyColumn, err)
	}
}
