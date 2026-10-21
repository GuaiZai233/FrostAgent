package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/storage"
)

func newEmbeddingPostgresDB(t *testing.T) *storage.DB {
	t.Helper()
	db, _ := newEmbeddingPostgresDBWithConfig(t)
	return db
}

func newEmbeddingPostgresDBWithConfig(t *testing.T) (*storage.DB, storage.Config) {
	t.Helper()
	return newEmbeddingPostgresDBAt(t, os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN"))
}

func newEmbeddingPostgresDBAt(t *testing.T, base string) (*storage.DB, storage.Config) {
	t.Helper()
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	var extension string
	if err := admin.QueryRow(`SELECT extname FROM pg_extension WHERE extname='vector'`).Scan(&extension); err != nil {
		admin.Close()
		t.Fatal("CI vector test requires pre-enabled pgvector")
	}
	schema := fmt.Sprintf("fa_embedding_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	config := storage.Config{Backend: storage.Postgres, DSN: parsed.String()}
	db, err := storage.OpenWithConfig(context.Background(), t.TempDir(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, config
}

func TestPostgresEmbeddingPersistenceAndIsolation(t *testing.T) {
	db := newEmbeddingPostgresDB(t)
	for _, id := range []string{"instance-a", "instance-b"} {
		if _, err := db.SQL.Exec(db.Bind(`INSERT INTO instances(id,name,created_at)VALUES(?,?,'now')`), id, id); err != nil {
			t.Fatal(err)
		}
	}
	vectorType, err := db.EnsureEmbeddingStorage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	managers := []*Embeddings{}
	for _, id := range []string{"instance-a", "instance-b"} {
		m := &Embeddings{db: db, instanceID: id, vectorType: vectorType}
		managers = append(managers, m)
		if err := m.publish(context.Background(), s, "synthetic-model", map[string]embeddedMemory{"same-id": {Hash: id, Vector: []float32{1, 0}}}); err != nil {
			t.Fatal(err)
		}
	}
	m := managers[0]
	scores, err := m.pgScores(context.Background(), s, "synthetic-model", []float32{1, 0})
	if err != nil || len(scores) != 1 || scores[0].Hash != "instance-a" {
		t.Fatalf("cross-instance score: %v %v", scores, err)
	}
	if scores, err := m.pgScores(context.Background(), s, "changed-model", []float32{1, 0}); err != nil || len(scores) != 0 {
		t.Fatalf("mixed model index: %v %v", scores, err)
	}
	if fp, err := (&Embeddings{db: db, instanceID: "instance-a"}).persistedFingerprint(context.Background(), s); err != nil || fp != "synthetic-model" {
		t.Fatalf("index did not survive new manager: %q %v", fp, err)
	}
	// A failed complete publication must roll back both manifest and vectors.
	if err := m.publish(context.Background(), s, "replacement", map[string]embeddedMemory{"bad": {Hash: "new", Vector: []float32{}}}); err == nil {
		t.Fatal("invalid vector unexpectedly persisted")
	}
	if fp, _ := m.persistedFingerprint(context.Background(), s); fp != "synthetic-model" {
		t.Fatal("failed publication replaced manifest")
	}
	for _, scope := range []SearchScope{
		{Type: ScopeGroup, Platform: "qq", GroupID: "group-a"},
		{Type: ScopeGroup, Platform: "qq", GroupID: "group-b"},
		{Type: ScopeGroup, Platform: "other", GroupID: "group-a"},
	} {
		key := scope.Platform + "/" + scope.GroupID
		if err := m.publish(context.Background(), scope, "group-model", map[string]embeddedMemory{"same-id": {Hash: key, Vector: []float32{0, 1}}}); err != nil {
			t.Fatal(err)
		}
		scores, err := m.pgScores(context.Background(), scope, "group-model", []float32{0, 1})
		if err != nil || len(scores) != 1 || scores[0].Hash != key {
			t.Fatalf("cross-group score: %v %v", scores, err)
		}
	}
	// Normal row edits/deletions remove only vectors with stale content hashes.
	store := NewSQLStore(db, "instance-a")
	seedEmbeddingMemory(t, store, "row", "synthetic-owner", "original")
	entries, _ := store.ListByOwner("synthetic-owner")
	if err := m.publish(context.Background(), s, "synthetic-model", map[string]embeddedMemory{"row": {Hash: memoryHash(entries[0]), Vector: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementAccessCount("row"); err != nil {
		t.Fatal(err)
	}
	scores, err = m.pgScores(context.Background(), s, "synthetic-model", []float32{1, 0})
	if err != nil || len(scores) != 1 {
		t.Fatalf("access update removed vector: %v %v", scores, err)
	}
	if err := store.UpdateEntry(MemoryEntry{ID: "row", Content: "edited"}); err != nil {
		t.Fatal(err)
	}
	scores, err = m.pgScores(context.Background(), s, "synthetic-model", []float32{1, 0})
	if err != nil || len(scores) != 0 {
		t.Fatalf("edited vector remained: %v %v", scores, err)
	}

	// Exercise the real index/query lifecycle, not only SQL helpers.
	f := newEmbeddingFixture(t, embeddingHandler)
	f.store = NewSQLStore(db, "instance-a")
	f.manager, err = NewEmbeddings(f.manager.Scope, f.store, NewSQLGroupManager(db, "instance-a", f.manager.Scope), f.router, nil)
	if err != nil {
		t.Fatal(err)
	}
	s = SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	rebuildFixture(t, f, s)
	result, err := f.manager.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(result) != 1 {
		t.Fatalf("SQL lifecycle search: %v %v", result, err)
	}
	scope := runtimescope.New(nil, nil, nil)
	defer func() { scope.Cancel(); scope.Wait() }()
	fresh, err := NewEmbeddings(scope, f.store, NewSQLGroupManager(db, "instance-a", scope), f.router, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.ensure(s); err != nil {
		t.Fatalf("compatible persisted index not reused: %v", err)
	}
	if err := f.store.UpdateEntry(MemoryEntry{ID: "row", Content: "changed again"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.ensure(s); !errors.Is(err, ErrEmbeddingNotReady) {
		t.Fatalf("empty persisted index not invalidated: %v", err)
	}
	fresh.Wait()
	result, err = fresh.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(result) != 1 || result[0].Content != "changed again" {
		t.Fatalf("empty persisted index did not self-heal: %v %v", result, err)
	}
	all, _ := f.store.ListAll()
	if _, err := ImportSQLSnapshot(db, "instance-a", Snapshot{PrivateEntries: all}, false); err != nil {
		t.Fatal(err)
	}
	if fp, _ := fresh.persistedFingerprint(context.Background(), s); fp != "" {
		t.Fatal("restoration retained derived index marker")
	}
}

func TestPostgresEmbeddingPartialChanges(t *testing.T) {
	db := newEmbeddingPostgresDB(t)
	exerciseEmbeddingPartialChanges(t, func(handler http.HandlerFunc) embeddingFixture {
		return newSQLEmbeddingFixture(t, db, handler)
	})
}

func TestPostgresEmbeddingTransientRecovery(t *testing.T) {
	db := newEmbeddingPostgresDB(t)
	exerciseEmbeddingTransientRecovery(t, func(handler http.HandlerFunc) embeddingFixture {
		return newSQLEmbeddingFixture(t, db, handler)
	})
}

func TestPostgresEmbeddingDimensionRecovery(t *testing.T) {
	for _, allPrivate := range []bool{false, true} {
		t.Run(fmt.Sprint("all-private-", allPrivate), func(t *testing.T) {
			db := newEmbeddingPostgresDB(t)
			exerciseEmbeddingDimensionRecovery(t, func(handler http.HandlerFunc) embeddingFixture {
				return newSQLEmbeddingFixture(t, db, handler)
			}, allPrivate)
		})
	}
}

func TestPostgresEmbeddingStaleQuery(t *testing.T) {
	db := newEmbeddingPostgresDB(t)
	exerciseEmbeddingStaleQuery(t, func(handler http.HandlerFunc) embeddingFixture {
		return newSQLEmbeddingFixture(t, db, handler)
	})
}

func TestPostgresEmbeddingCredentialRecovery(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		t.Run(fmt.Sprint("in-flight-", inFlight), func(t *testing.T) {
			db := newEmbeddingPostgresDB(t)
			exerciseEmbeddingCredentialRecovery(t, func(handler http.HandlerFunc) embeddingFixture {
				return newSQLEmbeddingFixture(t, db, handler)
			}, inFlight)
		})
	}
}
