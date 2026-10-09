package memory

import (
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLStorePersistsMemoryAndArchives(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "memory.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	store := NewSQLStore(db, "test-instance")
	now := time.Now().UTC().Round(0)
	brain := &BrainData{
		Entries: []MemoryEntry{{ID: "memory-a", Owner: "test-user", Content: "example", Source: SourceManual,
			Tags: []string{"first", "second"}, CreatedAt: now, UpdatedAt: now, MergedFrom: []string{"old-a"}}},
		MergeArchives: []MemoryMergeArchive{{MergedID: "memory-a", Owner: "test-user", MergedAt: now,
			Sources: []MemoryEntry{{ID: "old-a", Owner: "test-user", Content: "old", CreatedAt: now}}}},
	}
	if err := store.save(brain); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Entries) != 1 || loaded.Entries[0].ID != "memory-a" || len(loaded.Entries[0].Tags) != 2 ||
		len(loaded.Entries[0].MergedFrom) != 1 || len(loaded.MergeArchives) != 1 ||
		loaded.MergeArchives[0].Sources[0].Content != "old" {
		t.Fatalf("incomplete SQL round trip: %#v", loaded)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	loaded, err = NewSQLStore(db, "test-instance").load()
	if err != nil || len(loaded.Entries) != 1 || len(loaded.MergeArchives) != 1 {
		t.Fatalf("memory was not durable: %#v, %v", loaded, err)
	}
}

func TestSQLStoreSaveRollbackOnInvalidEntry(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "memory.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	store := NewSQLStore(db, "test-instance")
	now := time.Now().UTC()
	if err := store.save(&BrainData{Entries: []MemoryEntry{{ID: "valid", Owner: "test-user", Content: "saved", CreatedAt: now, UpdatedAt: now}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.save(&BrainData{Entries: []MemoryEntry{{ID: "", Owner: "test-user", Content: "invalid"}}}); err == nil {
		t.Fatal("invalid save succeeded")
	}
	loaded, err := store.load()
	if err != nil || len(loaded.Entries) != 1 || loaded.Entries[0].ID != "valid" {
		t.Fatalf("failed save damaged prior memory: %#v, %v", loaded, err)
	}
}
