package modelrouter

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"path/filepath"
	"testing"
)

func TestSQLModelRouterPersistsConfigurationAndManualSecret(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "models.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	manager := NewSQL(db, "")
	if err := manager.LoadError(); err != nil {
		t.Fatal(err)
	}
	cfg := manager.Draft()
	cfg.Endpoints = []Endpoint{{ID: "endpoint-a", DisplayName: "Endpoint", BaseURL: "https://example.com/v1", Enabled: true}}
	cfg.Models = []Model{{ID: "model-a", DisplayName: "Model", EndpointID: "endpoint-a", UpstreamModel: "model-a", Enabled: true, MaxInputTokens: 8192}}
	if err := manager.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetDraftEndpointSecret("endpoint-a", "synthetic-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Publish(); err != nil {
		t.Fatal(err)
	}
	manager = NewSQL(db, "test-instance")
	cfg = manager.Draft()
	cfg.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "model-a"}
	cfg.GroupOverrides = []GroupOverride{{Platform: "qq", GroupID: "test-group",
		Bindings: map[Workload]Binding{WorkloadDialogue: {Mode: BindingDisabled}}}}
	if err := manager.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Publish(); err != nil {
		t.Fatal(err)
	}
	reopened := NewSQL(db, "test-instance")
	if err := reopened.LoadError(); err != nil {
		t.Fatal(err)
	}
	active := reopened.Active()
	if len(active.Endpoints) != 1 || !active.Endpoints[0].APIKeyConfigured ||
		len(active.Models) != 1 || len(active.GroupOverrides) != 1 || active.Revision != 1 {
		t.Fatalf("model router SQL round trip failed: %#v", active)
	}
	if active.Models[0].MaxInputTokens != 8192 {
		t.Fatalf("model input limit was not recovered: %#v", active.Models[0])
	}
	secret, err := reopened.secrets.Resolve(active.Endpoints[0])
	if err != nil || secret != "synthetic-secret" {
		t.Fatalf("manual secret was not recovered: %v", err)
	}
}

func TestSQLModelRouterRejectsEnvSecretSource(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "models.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewSQL(db, "")
	if err := manager.LoadError(); err != nil {
		t.Fatal(err)
	}
	cfg := manager.Draft()
	cfg.Endpoints = []Endpoint{{ID: "endpoint-a", DisplayName: "Endpoint", BaseURL: "https://example.com/v1",
		APIKeySource: APIKeyStorageEnv, APIKeyRef: "SYNTHETIC_ENDPOINT_SECRET"}}
	if err := manager.SaveDraft(cfg); err == nil {
		t.Fatal("SQL model endpoint accepted process environment as a secret source")
	}
}
