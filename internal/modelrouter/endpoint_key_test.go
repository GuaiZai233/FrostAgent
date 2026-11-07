package modelrouter

import (
	"path/filepath"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestEndpointAPIKeyUsesPublishedCredential(t *testing.T) {
	for _, backend := range []string{"file", "sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var catalog, reader *Manager
			if backend == "file" {
				catalog = New(filepath.Join(t.TempDir(), "router.json"))
				reader = catalog
			} else {
				db := catalogTestDB(t, storage.Backend(backend))
				catalog = NewSQL(db, "")
				reader = NewSQL(db, "instance-a")
			}
			cfg := catalog.Draft()
			cfg.Endpoints = []Endpoint{{ID: "fixture-provider", DisplayName: "Fixture", BaseURL: "https://example.invalid/v1", Enabled: true}}
			if err := catalog.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.SetDraftEndpointSecret("fixture-provider", "synthetic-published-key"); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.EndpointAPIKey("fixture-provider"); err == nil {
				t.Fatal("unpublished provider was accessible")
			}
			if _, err := catalog.Publish(); err != nil {
				t.Fatal(err)
			}
			assertKey := func(want string) {
				t.Helper()
				key, err := reader.EndpointAPIKey("fixture-provider")
				if err != nil || key != want {
					t.Fatalf("published credential did not match: %v", err)
				}
			}
			assertKey("synthetic-published-key")
			if _, err := catalog.SetDraftEndpointSecret("fixture-provider", "synthetic-rotated-key"); err != nil {
				t.Fatal(err)
			}
			assertKey("synthetic-published-key")
			if _, err := catalog.Publish(); err != nil {
				t.Fatal(err)
			}
			assertKey("synthetic-rotated-key")
			cfg = catalog.Draft()
			cfg.Endpoints[0].Enabled = false
			if err := catalog.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.Publish(); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"fixture-provider", "missing-provider"} {
				if key, err := reader.EndpointAPIKey(id); err == nil || key != "" {
					t.Fatal("disabled or missing provider returned a credential")
				}
			}
		})
	}
}
