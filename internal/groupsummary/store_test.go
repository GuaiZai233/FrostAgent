package groupsummary

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestStoreLegacyMigrationAndDeduplication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "group_summary.json")

	// 1. Manually write disk file containing legacy alias AND canonical record
	tOld := time.Now().Add(-1 * time.Hour)
	tNew := time.Now()
	content := fmt.Sprintf(`{
		"version": 1,
		"summaries": [
			{
				"session_id": "aiocqhttp:group:test_grp_dedup",
				"summary": "older summary from astrbot",
				"created_at": "%s",
				"updated_at": "%s"
			},
			{
				"session_id": "group:test_grp_dedup",
				"summary": "newer summary from onebot",
				"created_at": "%s",
				"updated_at": "%s"
			}
		]
	}`, tOld.Format(time.RFC3339Nano), tOld.Format(time.RFC3339Nano), tNew.Format(time.RFC3339Nano), tNew.Format(time.RFC3339Nano))

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// 2. Load Store and verify deduplication
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	records, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 deduplicated record in List, got %d: %+v", len(records), records)
	}
	if records[0].SessionID != "group:test_grp_dedup" {
		t.Errorf("expected canonical session ID 'group:test_grp_dedup', got %q", records[0].SessionID)
	}
	if records[0].Summary != "newer summary from onebot" {
		t.Errorf("expected newer summary to be retained, got %q", records[0].Summary)
	}

	// 3. Upsert via legacy alias should update the canonical record and clean up any alias
	ok, err := store.Upsert("aiocqhttp:group:test_grp_dedup", "updated summary via alias", 0)
	if err != nil || !ok {
		t.Fatalf("Upsert via alias failed: ok=%v err=%v", ok, err)
	}

	recordsAfter, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(recordsAfter) != 1 {
		t.Fatalf("expected still exactly 1 record after alias upsert, got %d", len(recordsAfter))
	}
	if recordsAfter[0].SessionID != "group:test_grp_dedup" {
		t.Errorf("expected session ID to remain canonical 'group:test_grp_dedup', got %q", recordsAfter[0].SessionID)
	}
	if recordsAfter[0].Summary != "updated summary via alias" {
		t.Errorf("expected updated summary, got %q", recordsAfter[0].Summary)
	}
}

func TestStoreCrossAliasDeleteAndResurrection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "group_summary.json")

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Persist via legacy alias
	ok, err := store.Upsert("aiocqhttp:group:test_grp_resurrect", "initial summary", 0)
	if err != nil || !ok {
		t.Fatalf("Upsert failed: ok=%v err=%v", ok, err)
	}

	// Verify accessible via canonical key
	rec, found, err := store.Get("group:test_grp_resurrect")
	if err != nil || !found || rec.Summary != "initial summary" {
		t.Fatalf("expected to read summary via canonical key: found=%v rec=%+v", found, rec)
	}

	// Delete via canonical key
	if err := store.Delete("group:test_grp_resurrect"); err != nil {
		t.Fatalf("Delete via canonical key failed: %v", err)
	}

	// Must NOT resurrect when querying via legacy alias
	_, foundLegacy, err := store.Get("aiocqhttp:group:test_grp_resurrect")
	if err != nil {
		t.Fatalf("Get via legacy alias failed: %v", err)
	}
	if foundLegacy {
		t.Fatalf("summary resurrected when queried via legacy alias after canonical delete")
	}

	// Must NOT appear in List
	recs, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected 0 records in List after delete, got %d", len(recs))
	}
}

func TestStoreCrossAliasStaleWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "group_summary.json")

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Generation on legacy alias should report 0 initially
	gen := store.Generation("aiocqhttp:group:test_grp_stale")
	if gen != 0 {
		t.Fatalf("expected initial generation 0, got %d", gen)
	}

	// Delete via canonical key
	if err := store.Delete("group:test_grp_stale"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Generation on legacy alias must reflect the deletion epoch
	genAfter := store.Generation("aiocqhttp:group:test_grp_stale")
	if genAfter == 0 {
		t.Fatalf("expected generation to advance after canonical delete, got %d", genAfter)
	}

	// Attempt stale write with generation 0 via legacy alias -> must be rejected
	ok, err := store.Upsert("aiocqhttp:group:test_grp_stale", "stale summary write", 0)
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	if ok {
		t.Fatalf("expected stale write with mismatched generation to be rejected, but it succeeded")
	}

	// Verify no record exists
	_, found, _ := store.Get("group:test_grp_stale")
	if found {
		t.Fatalf("stale summary was unexpectedly written to store")
	}
}
