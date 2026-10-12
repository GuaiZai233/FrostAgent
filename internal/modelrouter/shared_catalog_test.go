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

func TestCatalogDraftOwnerKeepsEndpointAndSecretTogether(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			db := catalogTestDB(t, backend)
			catalog := seedCatalog(t, db, "https://old.invalid/v1")
			active, draft, err := catalog.StateForOwner("page-a")
			if err != nil {
				t.Fatal(err)
			}
			if err := catalog.SaveDraftForOwner(active, "no-op-page"); err != nil {
				t.Fatal(err)
			}
			if err := catalog.SaveDraftForOwner(active, "page-b"); err != nil {
				t.Fatalf("no-op save reserved the shared catalog: %v", err)
			}
			draft.Endpoints[0].BaseURL = "https://new.invalid/v1"
			if err := catalog.SaveDraftForOwner(draft, "page-a"); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.SetDraftEndpointSecretForOwner("shared", "synthetic-new-key", "page-a"); err != nil {
				t.Fatal(err)
			}
			previewEndpoint, _, previewKey, err := catalog.previewEndpointForOwner("shared", "", "page-a")
			if err != nil || previewEndpoint.BaseURL != "https://new.invalid/v1" || previewKey != "synthetic-new-key" {
				t.Fatalf("owner preview lost matching URL and key: %+v, %v", previewEndpoint, err)
			}
			if _, _, _, err := catalog.previewEndpointForOwner("shared", "", "page-b"); err == nil {
				t.Fatal("other page previewed staged URL and key")
			}
			if _, _, _, err := catalog.previewEndpointForOwner("shared", "", ""); err == nil {
				t.Fatal("ownerless client previewed staged URL and key")
			}
			otherActive, otherDraft, err := catalog.StateForOwner("page-b")
			if err != nil || otherActive.Endpoints[0].BaseURL != active.Endpoints[0].BaseURL ||
				otherDraft.Endpoints[0].BaseURL != active.Endpoints[0].BaseURL ||
				catalog.HasDraftSecretChangesForOwner("page-b") {
				t.Fatalf("other page inherited pending catalog or key: active=%+v draft=%+v err=%v", otherActive, otherDraft, err)
			}
			if err := catalog.SaveDraftForOwner(otherDraft, "page-b"); err == nil {
				t.Fatal("other page replaced the owned catalog draft")
			}
			if _, err := catalog.PublishForOwner("page-b"); err == nil {
				t.Fatal("other page published the pending key with its old endpoint")
			}
			if _, err := catalog.DiscardDraftForOwner("page-b"); err == nil {
				t.Fatal("other page discarded the owned catalog draft")
			}
			routes := NewSQL(db, "instance-b")
			binding := routes.Draft()
			binding.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "model-b"}
			if err := routes.SaveDraft(binding); err != nil {
				t.Fatal(err)
			}
			if _, err := routes.Publish(); err != nil {
				t.Fatal(err)
			}
			var key string
			if err := db.SQL.QueryRow(db.Bind(`SELECT secret FROM provider_secrets WHERE ref = ?`), "endpoint/shared").Scan(&key); err != nil || key != "synthetic-old-key" {
				t.Fatalf("route publish changed shared key: %v", err)
			}
			if cfg := catalog.Active(); cfg.Endpoints[0].BaseURL != "https://old.invalid/v1" {
				t.Fatal("route publish changed shared endpoint")
			}
			if _, err := catalog.PublishForOwner("page-a"); err != nil {
				t.Fatal(err)
			}
			if cfg := catalog.Active(); cfg.Endpoints[0].BaseURL != "https://new.invalid/v1" {
				t.Fatal("owner did not publish its endpoint")
			}
			if err := db.SQL.QueryRow(db.Bind(`SELECT secret FROM provider_secrets WHERE ref = ?`), "endpoint/shared").Scan(&key); err != nil || key != "synthetic-new-key" {
				t.Fatalf("owner did not publish matching key: %v", err)
			}
			abandoned := catalog.Draft()
			abandoned.Endpoints[0].BaseURL = "https://abandoned.invalid/v1"
			if err := catalog.SaveDraftForOwner(abandoned, "page-a"); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.SetDraftEndpointSecretForOwner("shared", "synthetic-abandoned-key", "page-a"); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.ResetSharedDraft(); err != nil {
				t.Fatal(err)
			}
			if err := catalog.SaveDraftForOwner(abandoned, "page-a"); err == nil {
				t.Fatal("reset owner published an old draft after its key was discarded")
			}
			if err := db.SQL.QueryRow(db.Bind(`SELECT secret FROM provider_secrets WHERE ref = ?`), "endpoint/shared").Scan(&key); err != nil || key != "synthetic-new-key" {
				t.Fatalf("reset published abandoned key: %v", err)
			}
			recovered := catalog.Draft()
			recovered.Endpoints[0].DisplayName = "Recovered Provider"
			if err := catalog.SaveDraftForOwner(recovered, "page-b"); err != nil {
				t.Fatalf("reset did not release draft owner: %v", err)
			}
			if _, err := catalog.PublishForOwner("page-b"); err != nil {
				t.Fatal(err)
			}
			if current := catalog.Active(); current.Endpoints[0].BaseURL != "https://new.invalid/v1" || current.Endpoints[0].DisplayName != "Recovered Provider" {
				t.Fatalf("recovery combined the abandoned URL with published catalog: %+v", current)
			}
		})
	}
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
