package modelrouter

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"FrostAgent/internal/core"
	"FrostAgent/internal/storage"
)

func catalogTestDB(t *testing.T, backend storage.Backend) *storage.DB {
	t.Helper()
	config := storage.Config{Backend: backend}
	if backend == storage.Postgres {
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
		schema := fmt.Sprintf("fa_catalog_%d", time.Now().UnixNano())
		if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
			admin.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
			if err != nil {
				t.Error(err)
			}
			admin.Close()
		})
		q := parsed.Query()
		q.Set("search_path", schema)
		parsed.RawQuery = q.Encode()
		config.DSN = parsed.String()
	}
	db, err := storage.OpenWithConfig(context.Background(), t.TempDir(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"instance-a", "instance-b"} {
		if _, err := db.SQL.Exec(db.Bind(`INSERT INTO instances(id,name,created_at) VALUES (?, ?, 'now')`), id, id); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedCatalog(t *testing.T, db *storage.DB, baseURL string) *Manager {
	t.Helper()
	catalog := NewSQL(db, "")
	cfg := catalog.Draft()
	cfg.Endpoints = []Endpoint{{ID: "shared", DisplayName: "Shared", BaseURL: baseURL, Enabled: true}}
	cfg.Models = []Model{
		{ID: "model-a", DisplayName: "Model A", EndpointID: "shared", UpstreamModel: "upstream-a", Enabled: true},
		{ID: "model-b", DisplayName: "Model B", EndpointID: "shared", UpstreamModel: "upstream-b", Enabled: true},
	}
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecret("shared", "synthetic-old-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func bindInstance(t *testing.T, manager *Manager, modelID string) {
	t.Helper()
	cfg := manager.Draft()
	cfg.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: modelID}
	if err := manager.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Publish(); err != nil {
		t.Fatal(err)
	}
}

func TestSharedCatalogAndIsolatedRoutes(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			db := catalogTestDB(t, backend)
			catalog := seedCatalog(t, db, "https://shared.invalid/v1")
			a, b := NewSQL(db, "instance-a"), NewSQL(db, "instance-b")
			bindInstance(t, a, "model-a")
			bindInstance(t, b, "model-b")
			cfg := a.Draft()
			cfg.Endpoints[0].BaseURL = "https://unwanted.invalid/v1"
			cfg.Models[0].UpstreamModel = "unwanted"
			cfg.GroupOverrides = []GroupOverride{{Platform: "qq", GroupID: "synthetic-group", Bindings: map[Workload]Binding{WorkloadDialogue: {Mode: BindingModel, ModelID: "model-b"}}}}
			if err := a.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := a.SetDraftEndpointSecret("shared", "unwanted"); err == nil {
				t.Fatal("instance mutated global key")
			}
			if _, err := a.ClearDraftEndpointSecret("shared"); err == nil {
				t.Fatal("instance cleared global key")
			}
			if _, err := a.Publish(); err != nil {
				t.Fatal(err)
			}
			for _, check := range []struct {
				manager *Manager
				scope   Scope
				model   string
			}{
				{a, Scope{}, "model-a"}, {a, Scope{Platform: "qq", GroupID: "synthetic-group"}, "model-b"}, {b, Scope{}, "model-b"},
			} {
				target, err := check.manager.Resolve(WorkloadDialogue, check.scope)
				if err != nil || target.ModelID != check.model || target.BaseURL != "https://shared.invalid/v1" {
					t.Fatalf("routing scope changed: %+v, %v", target, err)
				}
			}
			stale := NewSQL(db, "")
			cfg = catalog.Draft()
			cfg.Models[0].UpstreamModel = "changed"
			cfg.Endpoints[0].Enabled = false
			if err := catalog.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.Publish(); err != nil {
				t.Fatal(err)
			}
			if _, err := stale.Publish(); err == nil {
				t.Fatal("stale catalog overwrote published state")
			}
			if _, err := a.Resolve(WorkloadDialogue, Scope{}); err == nil {
				t.Fatal("disabled provider was still usable")
			}
			cfg = a.Draft()
			cfg.GlobalBindings[WorkloadVision] = Binding{Mode: BindingModel, ModelID: "model-b"}
			if err := a.SaveDraft(cfg); err != nil {
				t.Fatalf("disabled provider blocked unrelated route editing: %v", err)
			}
			if _, err := a.Publish(); err != nil {
				t.Fatal(err)
			}
			cfg = catalog.Draft()
			cfg.Models = cfg.Models[1:]
			if err := catalog.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.Publish(); err == nil {
				t.Fatal("deleted model still referenced by an instance")
			}
			catalog.DiscardDraft()
			if err := db.PrepareDeletion(context.Background(), "instance-a", 1); err != nil {
				t.Fatal(err)
			}
			if err := db.ConfirmDeletion(context.Background(), "instance-a"); err != nil {
				t.Fatal(err)
			}
			active, err := b.ActiveChecked()
			if err != nil || len(active.Endpoints) != 1 || len(active.Models) != 2 {
				t.Fatalf("instance deletion removed shared catalog: %v", err)
			}
			secret, err := b.secrets.Resolve(active.Endpoints[0])
			if err != nil || secret != "synthetic-old-key" {
				t.Fatal("instance deletion removed shared secret")
			}
		})
	}
}

func TestSharedProviderSnapshotKeepsMatchingCredential(t *testing.T) {
	db := catalogTestDB(t, storage.SQLite)
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	catalog := seedCatalog(t, db, server.URL+"/old/v1")
	a := NewSQL(db, "instance-a")
	bindInstance(t, a, "model-a")
	old := a.Snapshot()
	cfg := catalog.Draft()
	cfg.Endpoints[0].BaseURL = server.URL + "/new/v1"
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecret("shared", "synthetic-new-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	provider := a.Provider(WorkloadDialogue, false, time.Second)
	for _, ctx := range []context.Context{a.WithSnapshot(context.Background(), old), context.Background()} {
		if _, err := provider.Chat(ctx, core.ChatRequest{Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "test"}}}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0] != "/old/v1/chat/completions Bearer synthetic-old-key" || requests[1] != "/new/v1/chat/completions Bearer synthetic-new-key" {
		t.Fatalf("snapshot paired mismatched credentials: %v", requests)
	}
}

func TestSQLCatalogPublishFailureRollsBackCredentials(t *testing.T) {
	db := catalogTestDB(t, storage.SQLite)
	catalog := seedCatalog(t, db, "https://old.invalid/v1")
	cfg := catalog.Draft()
	cfg.Endpoints[0].BaseURL = "https://new.invalid/v1"
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecret("shared", "synthetic-new-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_key BEFORE UPDATE ON provider_secrets BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err == nil {
		t.Fatal("failing credential transaction committed")
	}
	reopened := NewSQL(db, "")
	active, err := reopened.ActiveChecked()
	if err != nil || active.Endpoints[0].BaseURL != "https://old.invalid/v1" || active.Revision != cfg.Revision {
		t.Fatalf("catalog partially published: %v", err)
	}
	secret, err := reopened.secrets.Resolve(active.Endpoints[0])
	if err != nil || secret != "synthetic-old-key" {
		t.Fatal("failed publication modified credentials")
	}
}

func TestSQLReadFailureNeverReturnsEffectiveCachedRoute(t *testing.T) {
	db := catalogTestDB(t, storage.SQLite)
	seedCatalog(t, db, "https://shared.invalid/v1")
	a := NewSQL(db, "instance-a")
	bindInstance(t, a, "model-a")
	db.Close()
	if _, err := a.ActiveChecked(); err == nil {
		t.Fatal("checked read hid database failure")
	}
	if _, _, err := a.State(); err == nil {
		t.Fatal("state read hid database failure")
	}
	if a.Snapshot().LoadError() == nil || len(a.Effective(Scope{})) != 0 {
		t.Fatal("cached routing survived database failure")
	}
	broken := NewSQL(db, "instance-a")
	if broken.Snapshot().LoadError() == nil {
		t.Fatal("initial database failure was lost")
	}
	if broken.secrets == nil {
		t.Fatal("initial failure left nil backend")
	}
}
