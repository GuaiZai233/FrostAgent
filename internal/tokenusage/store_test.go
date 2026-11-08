package tokenusage

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"synthetic-a", "synthetic-b"} {
		if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES(?,?,?)`, id, id, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func row(t *testing.T, s *Store, module string) Module {
	t.Helper()
	snap, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Modules {
		if r.Module == module {
			return r
		}
	}
	t.Fatal("missing module")
	return Module{}
}
func TestUsagePersistenceIsolationAndMissingValues(t *testing.T) {
	db := testDB(t)
	s, _ := New(db, "synthetic-a", "")
	cache := 4
	for _, u := range []*core.Usage{
		{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CachedPromptTokens: &cache},
		nil,
		{PromptTokens: 8, Reported: &core.UsageReported{Prompt: true}},
	} {
		if err := s.Record("dialogue", u); err != nil {
			t.Fatal(err)
		}
	}
	reopened, _ := New(db, "synthetic-a", "")
	got := row(t, reopened, "dialogue")
	if got.Requests != 3 || got.Input != (Counter{Value: 18, UnestimatedRequests: 1}) || got.Total != (Counter{Value: 15, UnestimatedRequests: 2}) || got.Cached != (Counter{Value: 4, UnestimatedRequests: 2}) || got.CacheHits != (Counter{Value: 1, UnestimatedRequests: 2}) {
		t.Fatalf("incorrect partial totals: %+v", got)
	}
	other, _ := New(db, "synthetic-b", "")
	if got := row(t, other, "dialogue"); got.Requests != 0 {
		t.Fatalf("cross-instance usage: %+v", got)
	}
	zero := 0
	if err := other.Record("vision", &core.Usage{CachedPromptTokens: &zero}); err != nil {
		t.Fatal(err)
	}
	if got := row(t, other, "vision"); got.Cached.UnestimatedRequests != 0 || got.CacheHits.Value != 0 {
		t.Fatalf("explicit zero cache was marked missing: %+v", got)
	}
}
func TestUsageConcurrentStoresAndDeletion(t *testing.T) {
	db := testDB(t)
	a, _ := New(db, "synthetic-a", "")
	b, _ := New(db, "synthetic-a", "")
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			if err := s.Record("group_compact", &core.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if got := row(t, a, "group_compact"); got.Requests != 40 || got.Total.Value != 120 {
		t.Fatalf("lost concurrent update: %+v", got)
	}
	if err := db.PrepareDeletion(context.Background(), "synthetic-a", 1); err != nil {
		t.Fatal(err)
	}
	if err := a.Record("vision", nil); err == nil {
		t.Fatal("recorded usage for deleting instance")
	}
	if err := db.ConfirmDeletion(context.Background(), "synthetic-a"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM settings WHERE instance_id='synthetic-a'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("usage survived deletion: %d %v", count, err)
	}
	if err := a.Record("vision", nil); err == nil {
		t.Fatal("resurrected usage for deleted instance")
	}
}
func TestUsageFileReopenAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token_usage.json")
	a, err := New(nil, "", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Record("sticker_summary", nil); err != nil {
		t.Fatal(err)
	}
	b, err := New(nil, "", path)
	if err != nil {
		t.Fatal(err)
	}
	if got := row(t, b, "sticker_summary"); got.Requests != 1 || got.Total.UnestimatedRequests != 1 {
		t.Fatalf("file restart lost usage: %+v", got)
	}
	snap, _ := b.Snapshot(context.Background())
	db := testDB(t)
	dest, _ := New(db, "synthetic-b", "")
	if err := dest.Restore(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	if got := row(t, dest, "sticker_summary"); got.Requests != 1 {
		t.Fatalf("restore lost usage: %+v", got)
	}
	snap.Modules = append(snap.Modules, snap.Modules[0])
	if err := dest.Restore(context.Background(), snap); err == nil {
		t.Fatal("duplicate backup module accepted")
	}
}
func TestUsageStorageFailureIsVisible(t *testing.T) {
	db := testDB(t)
	s, _ := New(db, "synthetic-a", "")
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_usage BEFORE INSERT ON settings WHEN NEW.key != '__incomplete' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("dialogue", nil); err == nil {
		t.Fatal("write failure hidden")
	}
	reopened, _ := New(db, "synthetic-a", "")
	snap, err := reopened.Snapshot(context.Background())
	if err != nil || !snap.Incomplete {
		t.Fatalf("missing persistent incomplete indicator: %+v %v", snap, err)
	}
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_usage`); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Record("dialogue", nil); err != nil {
		t.Fatal(err)
	}
	snap, err = reopened.Snapshot(context.Background())
	if err != nil || !snap.Incomplete {
		t.Fatalf("recovery erased incomplete indicator: %+v %v", snap, err)
	}
}

func TestCorruptUsageIsNotOverwritten(t *testing.T) {
	db := testDB(t)
	s, _ := New(db, "synthetic-a", "")
	raw := `{"module":"dialogue","requests":-1}`
	if _, err := db.SQL.Exec(`INSERT INTO settings(scope,instance_id,key,value) VALUES(?,?,?,?)`, Scope, "synthetic-a", "dialogue", raw); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("dialogue", nil); err == nil {
		t.Fatal("corrupt row overwritten")
	}
	var got string
	if err := db.SQL.QueryRow(`SELECT value FROM settings WHERE scope=? AND instance_id=? AND key=?`, Scope, "synthetic-a", "dialogue").Scan(&got); err != nil || got != raw {
		t.Fatalf("corrupt data changed: %s %v", got, err)
	}
}
