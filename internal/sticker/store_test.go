package sticker

import (
	"path/filepath"
	"testing"
)

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
