package groupsummary

import (
	"path/filepath"
	"testing"
)

func TestStoreAliasLookup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "group_summary.json")

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Persist under legacy AstrBot format
	ok, err := store.Upsert("aiocqhttp:group:test_grp_101", "running summary for test group", 0)
	if err != nil || !ok {
		t.Fatalf("Upsert failed: ok=%v err=%v", ok, err)
	}

	// Query under canonical OneBot format
	rec, found, err := store.Get("group:test_grp_101")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !found {
		t.Fatalf("expected to find summary via canonical key")
	}
	if rec.Summary != "running summary for test group" {
		t.Errorf("summary mismatch: got %q", rec.Summary)
	}

	// Persist another under canonical format
	ok, err = store.Upsert("group:test_grp_202", "canonical summary", 0)
	if err != nil || !ok {
		t.Fatalf("Upsert failed: ok=%v err=%v", ok, err)
	}

	// Query under legacy AstrBot format
	rec2, found2, err := store.Get("aiocqhttp:group:test_grp_202")
	if err != nil || !found2 {
		t.Fatalf("expected to find summary via aiocqhttp alias: found=%v err=%v", found2, err)
	}
	if rec2.Summary != "canonical summary" {
		t.Errorf("summary mismatch: got %q", rec2.Summary)
	}
}
