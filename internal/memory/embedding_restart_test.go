package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/storage"
)

func TestPostgresEmbeddingCleanupAfterDisabledRestart(t *testing.T) {
	for _, action := range []string{"private-edit", "private-delete", "group-edit", "group-delete", "restore"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			db, config := newEmbeddingPostgresDBWithConfig(t)
			for _, id := range []string{"synthetic-instance-a", "synthetic-instance-b"} {
				if _, err := db.SQL.Exec(db.Bind(`INSERT INTO instances(id,name,created_at) VALUES (?,?,'now')`), id, id); err != nil {
					t.Fatal(err)
				}
			}
			vectorType, err := db.EnsureEmbeddingStorage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			type fixture struct {
				instance string
				scope    SearchScope
			}
			fixtures := []fixture{
				{"synthetic-instance-a", SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}},
				{"synthetic-instance-a", SearchScope{Type: ScopeGroup, Platform: "qq", GroupID: "synthetic-group"}.normalized()},
				{"synthetic-instance-a", SearchScope{Type: ScopeGroup, Platform: "other", GroupID: "synthetic-group"}.normalized()},
				{"synthetic-instance-b", SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}},
			}
			for _, f := range fixtures {
				brain := sqlBrainStore{db: db, instanceID: f.instance, scope: f.scope.Type, platform: f.scope.Platform, groupID: f.scope.GroupID}
				vectors := map[string]embeddedMemory{}
				for _, id := range []string{"target", "keep"} {
					entry := MemoryEntry{ID: id, Owner: "synthetic-owner", Content: id, Source: SourceManual, CreatedAt: time.Now(), UpdatedAt: time.Now()}
					if err := brain.appendEntries([]MemoryEntry{entry}); err != nil {
						t.Fatal(err)
					}
					vectors[id] = embeddedMemory{Hash: memoryHash(entry), Vector: []float32{1, 0}}
				}
				if err := (&Embeddings{db: db, instanceID: f.instance, vectorType: vectorType}).publish(ctx, f.scope, "synthetic-model", vectors); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			// Reopen the actual database without constructing an Embeddings manager
			// or calling EnsureEmbeddingStorage, as when the feature is disabled.
			db, err = storage.OpenWithConfig(ctx, t.TempDir(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			private := NewSQLStore(db, "synthetic-instance-a")
			group, err := NewSQLGroupManager(db, "synthetic-instance-a", nil).GetGroupStoreForPlatform("qq", "synthetic-group")
			if err != nil {
				t.Fatal(err)
			}
			changed := MemoryEntry{ID: "target", Content: "edited content"}
			switch action {
			case "private-edit":
				err = private.UpdateEntry(changed)
			case "private-delete":
				err = private.Delete("target")
			case "group-edit":
				err = group.UpdateEntry(changed)
			case "group-delete":
				err = group.Delete("target")
			case "restore":
				_, err = ImportSQLSnapshot(db, "synthetic-instance-a", Snapshot{}, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			for index, f := range fixtures {
				want := 2
				if action == "restore" && f.instance == "synthetic-instance-a" {
					want = 0
				} else if (index == 0 && (action == "private-edit" || action == "private-delete")) ||
					(index == 1 && (action == "group-edit" || action == "group-delete")) {
					want = 1
				}
				var count int
				if err := db.SQL.QueryRow(db.Bind(`SELECT count(*) FROM memory_embeddings
					WHERE instance_id=? AND scope_type=? AND platform=? AND group_id=?`),
					f.instance, f.scope.Type, f.scope.Platform, f.scope.GroupID).Scan(&count); err != nil || count != want {
					t.Fatalf("fixture %d vectors after %s: got %d, want %d: %v", index, action, count, want, err)
				}
			}
		})
	}
}

func TestPostgresEmbeddingRebuildAfterVectorStorageLoss(t *testing.T) {
	for _, loss := range []struct{ name, statement string }{
		{"table", `DROP TABLE memory_embeddings`},
		{"column", `ALTER TABLE memory_embeddings DROP COLUMN embedding`},
	} {
		t.Run(loss.name, func(t *testing.T) {
			exercisePostgresEmbeddingStorageLoss(t, loss.statement, os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN"))
		})
	}
}

func TestPostgresEmbeddingRebuildAfterExtensionReinstall(t *testing.T) {
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
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
	t.Cleanup(func() { admin.Close() })
	// Extension removal affects the whole database, so never run it in the
	// shared test database even though the source fixture has its own schema.
	name := fmt.Sprintf("fa_vector_loss_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP DATABASE ` + name + ` WITH (FORCE)`); err != nil {
			t.Errorf("remove extension recovery database: %v", err)
		}
	})
	parsed.Path = "/" + name
	query := parsed.Query()
	query.Del("search_path")
	parsed.RawQuery = query.Encode()
	extensionAdmin, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = extensionAdmin.Exec(`CREATE EXTENSION vector`)
	extensionAdmin.Close()
	if err != nil {
		t.Fatal(err)
	}
	exercisePostgresEmbeddingStorageLoss(t, `DROP EXTENSION vector CASCADE`, parsed.String())
}

func exercisePostgresEmbeddingStorageLoss(t *testing.T, lossStatement, base string) {
	t.Helper()
	ctx := context.Background()
	db, config := newEmbeddingPostgresDBAt(t, base)
	f := newSQLEmbeddingFixture(t, db, embeddingHandler)
	seedEmbeddingMemory(t, f.store, "synthetic-memory", "synthetic-owner", "remembered tea")
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	rebuildFixture(t, f, s)
	before, err := f.manager.Search(ctx, s, "tea", 20)
	if err != nil || len(before) != 1 {
		t.Fatalf("initial index: %v %v", before, err)
	}
	// Simulate loss of the derived vectors, retaining the previously valid
	// fingerprint and source hash. The source memories must remain untouched.
	if _, err := db.SQL.Exec(lossStatement); err != nil {
		t.Fatal(err)
	}
	var markers int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM memory_embedding_scopes`).Scan(&markers); err != nil || markers != 1 {
		t.Fatalf("missing orphan scope fixture: count=%d: %v", markers, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.OpenWithConfig(ctx, t.TempDir(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if lossStatement == `DROP TABLE memory_embeddings` && db.EmbeddingStorageReady.Load() {
		t.Fatal("missing vector table was reported ready after reopening")
	}
	if lossStatement == `DROP EXTENSION vector CASCADE` {
		// Deployment, not application initialization, reinstalls the extension.
		if _, err := db.SQL.Exec(`CREATE EXTENSION vector WITH SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	}
	scope := runtimescope.New(nil, nil, nil)
	defer func() { scope.Cancel(); scope.Wait() }()
	store := NewSQLStore(db, "synthetic-recovery-instance")
	restored, err := NewEmbeddings(scope, store, NewSQLGroupManager(db, "synthetic-recovery-instance", scope), f.router, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, searchErr := restored.Search(ctx, s, "tea", 20)
	if !errors.Is(searchErr, ErrEmbeddingNotReady) {
		t.Fatalf("lost vector storage did not trigger a rebuild: %v", searchErr)
	}
	restored.Wait()
	result, err := restored.Search(ctx, s, "tea", 20)
	if err != nil || len(result) != 1 || result[0].ID != "synthetic-memory" || result[0].Content != "remembered tea" {
		t.Fatalf("search did not rebuild the missing index: %v %v", result, err)
	}
	// A subsequent enable/storage check must preserve the healthy rebuilt index.
	if _, err := db.EnsureEmbeddingStorage(ctx); err != nil {
		t.Fatal(err)
	}
	if fp, err := restored.persistedFingerprint(ctx, s); err != nil || fp == "" {
		t.Fatalf("healthy scope was discarded: fingerprint=%q: %v", fp, err)
	}
	if result, err := restored.Search(ctx, s, "tea", 20); err != nil || len(result) != 1 {
		t.Fatalf("healthy index was not reusable: %v %v", result, err)
	}
}
