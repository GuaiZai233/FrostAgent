package backup

import (
	"context"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/storage"
)

func TestInstanceZIPAfterSkillsV12Migration(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if db != nil {
			db.Close()
		}
	})
	const id = "synthetic-backup-instance"
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES (?,?, 'now')`, id, id); err != nil {
		t.Fatal(err)
	}
	catalog := modelrouter.NewSQL(db, "")
	cfg := catalog.Draft()
	cfg.Endpoints = []modelrouter.Endpoint{{ID: "synthetic-provider", DisplayName: "Synthetic", BaseURL: "https://model.invalid/v1", Enabled: true}}
	cfg.Models = []modelrouter.Model{{ID: "synthetic-model", DisplayName: "Synthetic", EndpointID: "synthetic-provider", UpstreamModel: "synthetic-model", Enabled: true}}
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	router := modelrouter.NewSQL(db, id)
	cfg = router.Draft()
	cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "synthetic-model"}
	if err := router.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Publish(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := memory.NewSQLStore(db, id).Save(memory.MemoryEntry{ID: "synthetic-memory", Owner: "synthetic-owner", Content: "preserved memory", Source: memory.SourceManual, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the other branch's v12 without replacing any user rows.
	for _, statement := range []string{
		`ALTER TABLE provider_models DROP COLUMN max_input_tokens`,
		`ALTER TABLE memory_entries DROP COLUMN summary_dirty`,
		`DROP TABLE computer_use_tasks`,
		`DROP TABLE computer_use_projects`,
		`DROP TABLE computer_use_config`,
		`UPDATE schema_meta SET version=12 WHERE id=1`,
	} {
		if _, err := db.SQL.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	images := t.TempDir()
	if _, err := BuildInstanceZIP(db, id, images); err == nil || !strings.Contains(err.Error(), "max_input_tokens") {
		t.Fatalf("fixture did not reproduce missing-column backup failure: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := BuildInstanceZIP(db, id, images)
	if err != nil {
		t.Fatalf("backup after migration: %v", err)
	}
	decoded, err := DecodeInstanceZIP(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Settings.ModelRouter.Models) != 1 || decoded.Settings.ModelRouter.Models[0].ID != "synthetic-model" {
		t.Fatal("backup lost the referenced model")
	}
	if len(decoded.Memories.PrivateEntries) != 1 || decoded.Memories.PrivateEntries[0].Content != "preserved memory" {
		t.Fatal("backup lost memory data")
	}
}
