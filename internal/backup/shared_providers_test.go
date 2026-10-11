package backup

import (
	"context"
	"testing"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/storage"
)

func TestInstanceSettingsReuseSharedProvidersAndRemapConflicts(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"source", "target"} {
		if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES (?, ?, 'now')`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	catalog := modelrouter.NewSQL(db, "")
	cfg := catalog.Draft()
	cfg.Endpoints = []modelrouter.Endpoint{{ID: "provider", DisplayName: "Provider", BaseURL: "https://original.invalid/v1", Enabled: true}}
	cfg.Models = []modelrouter.Model{
		{ID: "model", DisplayName: "Model", EndpointID: "provider", UpstreamModel: "synthetic-model", Enabled: true},
		{ID: "unused", DisplayName: "Unused", EndpointID: "provider", UpstreamModel: "unused", Enabled: true},
	}
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecret("provider", "synthetic-shared-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	source := modelrouter.NewSQL(db, "source")
	cfg = source.Draft()
	cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "model"}
	if err := source.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Publish(); err != nil {
		t.Fatal(err)
	}
	saved, err := ExportSettings(db, "source")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.ModelRouter.Endpoints) != 1 || len(saved.ModelRouter.Models) != 1 || saved.ModelRouter.Endpoints[0].APIKeySource != "" || saved.ModelRouter.Endpoints[0].APIKeyRef != "" || saved.ModelRouter.Endpoints[0].APIKeyConfigured {
		t.Fatal("export leaked unreferenced models or credential metadata")
	}
	staleTarget := modelrouter.NewSQL(db, "target")
	if err := ImportSettings(db, "target", saved); err != nil {
		t.Fatal(err)
	}
	if _, err := staleTarget.Publish(); err == nil {
		t.Fatal("pre-import draft overwrote restored route")
	}
	active, err := modelrouter.NewSQL(db, "target").ActiveChecked()
	if err != nil || len(active.Models) != 2 || active.GlobalBindings[modelrouter.WorkloadDialogue].ModelID != "model" || !active.Endpoints[0].APIKeyConfigured {
		t.Fatalf("same catalog import did not reuse provider: %v", err)
	}
	var secret string
	if err := db.SQL.QueryRow(`SELECT secret FROM provider_secrets WHERE ref = 'endpoint/provider'`).Scan(&secret); err != nil || secret != "synthetic-shared-secret" {
		t.Fatal("import erased existing shared credential")
	}
	// A foreign backup reusing our IDs with different definitions must be given
	// new identities and must not inherit our credentials.
	saved.ModelRouter.Endpoints[0].BaseURL = "https://foreign.invalid/v1"
	if err := ImportSettings(db, "target", saved); err != nil {
		t.Fatal(err)
	}
	target := modelrouter.NewSQL(db, "target")
	resolved, err := target.Resolve(modelrouter.WorkloadDialogue, modelrouter.Scope{})
	if err != nil || resolved.EndpointID == "provider" || resolved.ModelID == "model" || resolved.BaseURL != "https://foreign.invalid/v1" {
		t.Fatalf("foreign backup hijacked global identities: %+v, %v", resolved, err)
	}
	active, err = target.ActiveChecked()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range active.Endpoints {
		if e.ID == resolved.EndpointID && e.APIKeyConfigured {
			t.Fatal("foreign backup borrowed an existing credential")
		}
	}
	original, err := source.Resolve(modelrouter.WorkloadDialogue, modelrouter.Scope{})
	if err != nil || original.BaseURL != "https://original.invalid/v1" {
		t.Fatalf("import changed source routing: %v", err)
	}
	// Fail after the catalog and routes have been written in the transaction.
	// Neither those writes nor the settings replacement may become visible.
	var beforeCount int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM provider_endpoints`).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	beforeModel := resolved.ModelID
	saved.Dialogues = []llm.DialogueExample{{ID: "synthetic-dialogue", User: "test", Preferred: "reply"}}
	saved.ModelRouter.Endpoints[0].BaseURL = "https://rollback.invalid/v1"
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_import BEFORE INSERT ON dialogues BEGIN SELECT RAISE(ABORT, 'synthetic import failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := ImportSettings(db, "target", saved); err == nil {
		t.Fatal("broken import unexpectedly committed")
	}
	var afterCount int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM provider_endpoints`).Scan(&afterCount); err != nil || afterCount != beforeCount {
		t.Fatalf("failed import left shared providers: %v", err)
	}
	resolved, err = target.Resolve(modelrouter.WorkloadDialogue, modelrouter.Scope{})
	if err != nil || resolved.ModelID != beforeModel {
		t.Fatalf("failed import changed instance route: %v", err)
	}
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_import`); err != nil {
		t.Fatal(err)
	}
	saved.Dialogues = nil
	// Orphan secrets are preserved by upgrades, but are not a credential source
	// for newly imported providers.
	if _, err := db.SQL.Exec(`INSERT INTO provider_secrets(ref,secret) VALUES ('endpoint/orphan', 'synthetic-orphan-secret')`); err != nil {
		t.Fatal(err)
	}
	saved.ModelRouter.Endpoints[0].ID = "orphan"
	saved.ModelRouter.Models[0].EndpointID = "orphan"
	if err := ImportSettings(db, "target", saved); err != nil {
		t.Fatal(err)
	}
	resolved, err = target.Resolve(modelrouter.WorkloadDialogue, modelrouter.Scope{})
	if err != nil || resolved.EndpointID == "orphan" {
		t.Fatalf("import adopted an orphan credential: %v", err)
	}
	var count int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM provider_secrets WHERE ref = ?`, resolved.APIKeyRef).Scan(&count); err != nil || count != 0 {
		t.Fatalf("imported endpoint has a credential: %v", err)
	}
}
