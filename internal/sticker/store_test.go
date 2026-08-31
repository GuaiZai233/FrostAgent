package sticker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func blockIndexSave(t *testing.T, store *Store) func() {
	t.Helper()
	blocker := store.indexPath() + ".tmp"
	if err := os.Mkdir(blocker, 0755); err != nil {
		t.Fatalf("block index save: %v", err)
	}
	return func() {
		if err := os.Remove(blocker); err != nil {
			t.Fatalf("unblock index save: %v", err)
		}
	}
}

func TestStore_CRUDAndPersistence(t *testing.T) {
	tempDir := t.TempDir()
	storeDir := filepath.Join(tempDir, "stickers")

	store, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	testData := []byte("image_data_test_123")
	hash := HashBytes(testData)
	fileName := hash + ".jpg"

	// 1. Add sticker
	if err := store.Add(hash, fileName, testData); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if !store.Exists(hash) {
		t.Errorf("expected sticker %s to exist", hash)
	}

	// 2. Duplicate add should fail
	if err := store.Add(hash, fileName, testData); err == nil {
		t.Error("expected duplicate add to fail")
	}

	// 3. Weight increment
	if err := store.IncrementWeight(hash); err != nil {
		t.Fatalf("IncrementWeight failed: %v", err)
	}

	entry, ok := store.Get(hash)
	if !ok {
		t.Fatal("Get returned false")
	}
	if entry.Weight != 2 {
		t.Errorf("expected weight 2, got %d", entry.Weight)
	}
	if entry.Status != StatusUnsummarized {
		t.Errorf("expected status %s, got %s", StatusUnsummarized, entry.Status)
	}

	// 4. Update description and keywords
	if err := store.Update(hash, "开心的大笑", []string{"开心", "大笑"}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	entry, _ = store.Get(hash)
	if entry.Status != StatusReady {
		t.Errorf("expected status %s, got %s", StatusReady, entry.Status)
	}
	if entry.Description != "开心的大笑" {
		t.Errorf("expected description %q, got %q", "开心的大笑", entry.Description)
	}

	// 5. Reload store from disk (test persistence)
	reloaded, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("failed to reload store: %v", err)
	}
	reloadedEntry, ok := reloaded.Get(hash)
	if !ok {
		t.Fatal("reloaded store missing entry")
	}
	if reloadedEntry.Weight != 2 || reloadedEntry.Description != "开心的大笑" {
		t.Errorf("reloaded entry mismatch: %+v", reloadedEntry)
	}

	// 6. Stats
	stats := reloaded.Stats()
	if stats.Total != 1 || stats.Ready != 1 || stats.Unsummarized != 0 {
		t.Errorf("unexpected stats: %+v", stats)
	}

	// 7. Delete
	if err := reloaded.Delete(hash); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if reloaded.Exists(hash) {
		t.Error("expected sticker to be deleted")
	}
}

func TestStoreAddPersistenceFailureDoesNotCommitMemory(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "stickers")
	store, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	data := []byte("GIF89a failed add")
	id := HashBytes(data)
	fileName := id + ".gif"
	unblock := blockIndexSave(t, store)

	if err := store.Add(id, fileName, data); err == nil {
		t.Fatal("expected Add persistence failure")
	}
	if store.Exists(id) {
		t.Fatal("failed Add committed the entry in memory")
	}
	if _, err := os.Stat(filepath.Join(storeDir, fileName)); !os.IsNotExist(err) {
		t.Fatalf("uncommitted image file still exists: %v", err)
	}

	unblock()
	reloaded, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	if reloaded.Exists(id) {
		t.Fatal("failed Add was persisted to disk")
	}
}

func TestStoreMutationPersistenceFailuresDoNotCommitMemory(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "stickers")
	store, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	data := []byte("GIF89a existing sticker")
	id := HashBytes(data)
	fileName := id + ".gif"
	if err := store.Add(id, fileName, data); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	want, _ := store.Get(id)
	unblock := blockIndexSave(t, store)

	operations := []struct {
		name string
		run  func() error
	}{
		{name: "IncrementWeight", run: func() error { return store.IncrementWeight(id) }},
		{name: "Update", run: func() error { return store.Update(id, "changed", []string{"changed"}) }},
		{name: "SetStatus", run: func() error { return store.SetStatus(id, StatusReady) }},
		{name: "Delete", run: func() error { return store.Delete(id) }},
	}
	for _, operation := range operations {
		if err := operation.run(); err == nil {
			t.Errorf("%s succeeded while persistence was blocked", operation.name)
		}
		got, ok := store.Get(id)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s changed in-memory entry: got %+v, found=%v; want %+v", operation.name, got, ok, want)
		}
	}
	if _, err := os.Stat(filepath.Join(storeDir, fileName)); err != nil {
		t.Fatalf("failed Delete removed image file: %v", err)
	}

	unblock()
	reloaded, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := reloaded.Get(id)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted entry changed after failed mutations: got %+v, found=%v; want %+v", got, ok, want)
	}
}

func TestStoreDeleteReportsFileRemovalFailureAndRestoresIndex(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "stickers")
	store, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	data := []byte("GIF89a missing file")
	id := HashBytes(data)
	fileName := id + ".gif"
	if err := store.Add(id, fileName, data); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if err := os.Remove(filepath.Join(storeDir, fileName)); err != nil {
		t.Fatalf("remove image before Delete: %v", err)
	}

	err = store.Delete(id)
	if err == nil || !strings.Contains(err.Error(), "remove file") {
		t.Fatalf("Delete error = %v, want file removal error", err)
	}
	if !store.Exists(id) {
		t.Fatal("Delete committed memory after file removal failed")
	}
	reloaded, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	if !reloaded.Exists(id) {
		t.Fatal("Delete did not restore the persisted index after file removal failed")
	}
}
