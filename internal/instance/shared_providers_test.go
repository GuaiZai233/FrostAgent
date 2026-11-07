package instance

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

func TestProviderAPIExistsWithoutInstanceAndRejectsInstanceCredentialEdits(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	server := httptest.NewServer(m)
	defer server.Close()
	ctx := context.Background()
	global := frostagentv1connect.NewModelRouterServiceClient(server.Client(), server.URL)
	state, err := global.GetState(ctx, connect.NewRequest(&v1.GetStateRequest{}))
	if err != nil || state.Msg.GetLoadError() != "" {
		t.Fatalf("zero-instance provider API: %v", err)
	}
	cfg := state.Msg.Active
	cfg.Endpoints = []*v1.ModelEndpoint{{Id: "shared", DisplayName: "Shared", BaseUrl: "https://example.invalid/v1", Enabled: true}}
	cfg.Models = []*v1.ModelTarget{{Id: "model", DisplayName: "Model", EndpointId: "shared", UpstreamModel: "synthetic", Enabled: true}}
	withoutOwner, err := global.SaveDraft(ctx, connect.NewRequest(&v1.SaveDraftRequest{Configuration: cfg}))
	if err != nil || withoutOwner.Msg.Success {
		t.Fatalf("ownerless global draft was accepted: %v, %v", withoutOwner, err)
	}
	saveReq := connect.NewRequest(&v1.SaveDraftRequest{Configuration: cfg})
	saveReq.Header().Set("X-FrostAgent-Catalog-Draft", "synthetic-page")
	saved, err := global.SaveDraft(ctx, saveReq)
	if err != nil || !saved.Msg.Success {
		t.Fatalf("global draft: %v, %v", saved, err)
	}
	keyReq := connect.NewRequest(&v1.SetDraftEndpointSecretRequest{EndpointId: "shared", ApiKey: "synthetic-provider-key"})
	keyReq.Header().Set("X-FrostAgent-Catalog-Draft", "synthetic-page")
	key, err := global.SetDraftEndpointSecret(ctx, keyReq)
	if err != nil || !key.Msg.Success {
		t.Fatalf("global credential: %v, %v", key, err)
	}
	publishReq := connect.NewRequest(&v1.PublishRequest{})
	publishReq.Header().Set("X-FrostAgent-Catalog-Draft", "synthetic-page")
	published, err := global.Publish(ctx, publishReq)
	if err != nil || !published.Msg.Success {
		t.Fatalf("global publish: %v, %v", published, err)
	}
	info, err := m.Create("Synthetic")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %+v, %v", info, err)
	}
	instance := frostagentv1connect.NewModelRouterServiceClient(server.Client(), server.URL+"/instances/"+info.ID)
	state, err = instance.GetState(ctx, connect.NewRequest(&v1.GetStateRequest{}))
	if err != nil || len(state.Msg.Active.Endpoints) != 1 {
		t.Fatalf("instance cannot read shared catalog: %v", err)
	}
	cleared, err := instance.ClearDraftEndpointSecret(ctx, connect.NewRequest(&v1.ClearDraftEndpointSecretRequest{EndpointId: "shared"}))
	if err != nil || cleared.Msg.Success || cleared.Msg.Error == "" {
		t.Fatalf("instance credential edit was accepted: %v, %v", cleared, err)
	}
}

func TestDeletingInstanceKeepsPublishedSharedCredential(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Synthetic")
	if err != nil || info.Error != "" {
		t.Fatalf("create: %+v %v", info, err)
	}
	catalog := modelrouter.NewSQL(m.db, "")
	cfg := catalog.Draft()
	cfg.Endpoints = []modelrouter.Endpoint{{ID: "provider", DisplayName: "Provider", BaseURL: "https://example.invalid/v1", Enabled: true}}
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecret("provider", "synthetic-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	var secret string
	if err := m.db.SQL.QueryRow(`SELECT secret FROM provider_secrets WHERE ref = 'endpoint/provider'`).Scan(&secret); err != nil || secret != "synthetic-key" {
		t.Fatalf("instance deletion removed shared credential: %v", err)
	}
}
