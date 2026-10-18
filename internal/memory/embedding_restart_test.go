package memory

import (
	"context"
	"testing"
	"time"

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
