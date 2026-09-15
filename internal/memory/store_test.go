package memory

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStore_SaveEntriesConditionally_Success(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)

	entries := []MemoryEntry{
		{
			ID:        "mem_01",
			Owner:     "user_01",
			OwnerType: OwnerUser,
			Content:   "memory 1",
		},
		{
			ID:        "mem_02",
			Owner:     "user_01",
			OwnerType: OwnerUser,
			Content:   "memory 2",
		},
	}

	validatorCalled := false
	validator := func() bool {
		validatorCalled = true
		return true
	}

	if err := store.SaveEntriesConditionally(entries, validator); err != nil {
		t.Fatalf("SaveEntriesConditionally failed: %v", err)
	}

	if !validatorCalled {
		t.Fatalf("expected validator to be called")
	}

	loaded, err := store.ListByOwner("user_01")
	if err != nil {
		t.Fatalf("ListByOwner failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}
}

func TestStore_SaveEntriesConditionally_ConditionFailed(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)

	entries := []MemoryEntry{
		{
			ID:        "mem_01",
			Owner:     "user_01",
			OwnerType: OwnerUser,
			Content:   "memory 1",
		},
	}

	validator := func() bool {
		return false
	}

	err := store.SaveEntriesConditionally(entries, validator)
	if !errors.Is(err, ErrConditionFailed) {
		t.Fatalf("expected ErrConditionFailed, got: %v", err)
	}

	loaded, err := store.ListByOwner("user_01")
	if err != nil {
		t.Fatalf("ListByOwner failed: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 entries in store after condition failure, got %d", len(loaded))
	}
}

func TestStore_SaveEntriesConditionally_DeterministicLockRace(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)

	// Pre-populate with one valid entry
	initEntry := MemoryEntry{
		ID:        "mem_init",
		Owner:     "user_race",
		OwnerType: OwnerUser,
		Content:   "initial memory",
	}
	if err := store.Save(initEntry); err != nil {
		t.Fatalf("initial save failed: %v", err)
	}

	// Lock the store first
	unlock := store.LockWriteForTest()

	valid := true
	validator := func() bool {
		return valid
	}

	staleEntries := []MemoryEntry{
		{
			ID:        "mem_stale",
			Owner:     "user_race",
			OwnerType: OwnerUser,
			Content:   "stale memory that must not be saved",
		},
	}

	var wg sync.WaitGroup
	var saveErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		saveErr = store.SaveEntriesConditionally(staleEntries, validator)
	}()

	// Wait briefly so the background goroutine is definitely waiting on s.mu.Lock()
	time.Sleep(50 * time.Millisecond)

	// Invalidate the condition while the store lock is held
	valid = false

	// Release store lock
	unlock()

	// Wait for background goroutine to complete
	wg.Wait()

	if !errors.Is(saveErr, ErrConditionFailed) {
		t.Fatalf("expected ErrConditionFailed, got %v", saveErr)
	}

	// Verify only the initial memory exists
	entries, err := store.ListByOwner("user_race")
	if err != nil {
		t.Fatalf("ListByOwner failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry in store, got %d", len(entries))
	}
	if entries[0].Content != "initial memory" {
		t.Fatalf("expected initial memory, got %q", entries[0].Content)
	}
}
