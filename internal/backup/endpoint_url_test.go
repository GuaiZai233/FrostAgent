package backup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestCompleteEndpointURLSurvivesBackup(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: "synthetic-instance", Name: "Synthetic", CreatedAt: time.Now()}, 2); err != nil {
		t.Fatal(err)
	}
	catalog := modelrouter.NewSQL(db, "")
	cfg := catalog.Draft()
	const fullURL = "https://synthetic-user:synthetic-url-password@relay.invalid/exact?route=fixture&key=synthetic-query-key"
	cfg.Endpoints = []modelrouter.Endpoint{{ID: "synthetic-endpoint", DisplayName: "Synthetic", BaseURL: fullURL, APIFormat: "custom", Enabled: true}}
	cfg.Models = []modelrouter.Model{{ID: "synthetic-model", DisplayName: "Synthetic", EndpointID: "synthetic-endpoint", UpstreamModel: "synthetic-upstream", Enabled: true}}
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecret("synthetic-endpoint", "synthetic-separate-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	routes := modelrouter.NewSQL(db, "synthetic-instance")
	cfg = routes.Draft()
	cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "synthetic-model"}
	if err := routes.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Publish(); err != nil {
		t.Fatal(err)
	}
	exported, err := ExportSettings(db, "synthetic-instance")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := exported.ModelRouter.Endpoints[0]
	if endpoint.BaseURL != fullURL || endpoint.APIFormat != "custom" {
		t.Fatal("complete URL or format lost")
	}
	if endpoint.APIKeyRef != "" || endpoint.APIKeySource != "" || endpoint.APIKeyConfigured {
		t.Fatal("independent credential metadata was exported")
	}
	if !strings.Contains(exported.SecretNotice, "URL") {
		t.Fatal("backup omitted URL credential notice")
	}
}
