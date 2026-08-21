package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreIncrementAccessCount(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)

	oldTime := time.Now().Add(-1 * time.Hour)
	entry1 := MemoryEntry{
		ID:          "mem_1",
		Owner:       "alice",
		Content:     "Alice loves coding in Go",
		Tags:        []string{"golang", "programming"},
		Visibility:  VisibilityPrivate,
		AccessCount: 0,
		CreatedAt:   oldTime,
		UpdatedAt:   oldTime,
	}
	entry2 := MemoryEntry{
		ID:          "mem_2",
		Owner:       "alice",
		Content:     "Alice uses Vim",
		Tags:        []string{"editor", "vim"},
		Visibility:  VisibilityPrivate,
		AccessCount: 2,
		CreatedAt:   oldTime,
		UpdatedAt:   oldTime,
	}

	if err := store.Save(entry1); err != nil {
		t.Fatalf("failed to save entry1: %v", err)
	}
	if err := store.Save(entry2); err != nil {
		t.Fatalf("failed to save entry2: %v", err)
	}

	// Increment access count for mem_1
	if err := store.IncrementAccessCount("mem_1"); err != nil {
		t.Fatalf("IncrementAccessCount failed: %v", err)
	}

	entries, err := store.ListByOwner("alice")
	if err != nil {
		t.Fatalf("ListByOwner failed: %v", err)
	}

	var found1, found2 *MemoryEntry
	for i := range entries {
		switch entries[i].ID {
		case "mem_1":
			found1 = &entries[i]
		case "mem_2":
			found2 = &entries[i]
		}
	}

	if found1 == nil || found1.AccessCount != 1 {
		t.Errorf("expected mem_1 AccessCount = 1, got %v", found1)
	}
	if found1 != nil && !found1.UpdatedAt.After(oldTime) {
		t.Errorf("expected mem_1 UpdatedAt to be updated, got %v", found1.UpdatedAt)
	}

	if found2 == nil || found2.AccessCount != 2 {
		t.Errorf("expected mem_2 AccessCount = 2, got %v", found2)
	}

	// Increment access count for both
	if err := store.IncrementAccessCount("mem_1", "mem_2"); err != nil {
		t.Fatalf("IncrementAccessCount failed: %v", err)
	}

	entries, _ = store.ListByOwner("alice")
	for _, e := range entries {
		if e.ID == "mem_1" && e.AccessCount != 2 {
			t.Errorf("expected mem_1 AccessCount = 2, got %d", e.AccessCount)
		}
		if e.ID == "mem_2" && e.AccessCount != 3 {
			t.Errorf("expected mem_2 AccessCount = 3, got %d", e.AccessCount)
		}
	}

	// Non-existent ID should not error
	if err := store.IncrementAccessCount("non_existent"); err != nil {
		t.Errorf("IncrementAccessCount with non-existent ID should not error: %v", err)
	}
}

func TestReaderRecordRecall(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)
	reader := NewReader(store, 5)

	entry := MemoryEntry{
		ID:          "mem_test",
		Owner:       "bob",
		Content:     "Bob likes tea",
		Tags:        []string{"tea", "drink"},
		Visibility:  VisibilityPrivate,
		AccessCount: 0,
	}
	if err := store.Save(entry); err != nil {
		t.Fatalf("failed to save entry: %v", err)
	}

	recalled, err := reader.SearchByTags(context.Background(), []string{"tea"})
	if err != nil {
		t.Fatalf("SearchByTags failed: %v", err)
	}
	if len(recalled) != 1 {
		t.Fatalf("expected 1 recalled entry, got %d", len(recalled))
	}

	if err := reader.RecordRecall(recalled); err != nil {
		t.Fatalf("RecordRecall failed: %v", err)
	}

	all, _ := store.ListByOwner("bob")
	if len(all) != 1 || all[0].AccessCount != 1 {
		t.Errorf("expected AccessCount = 1 after RecordRecall, got %d", all[0].AccessCount)
	}
}
