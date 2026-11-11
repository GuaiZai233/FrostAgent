package modelrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestV15RetainsFormatsAndDoesNotMisrouteNativeRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Error("old release sent a request using the wrong protocol")
	}))
	defer server.Close()
	manager := New(filepath.Join(t.TempDir(), "router.json"))
	cfg := manager.Draft()
	cfg.Endpoints = []Endpoint{{ID: "synthetic-endpoint", DisplayName: "Synthetic", BaseURL: server.URL + "/exact?route=fixture", APIFormat: "custom", Enabled: true}}
	cfg.Models = []Model{{ID: "synthetic-model", DisplayName: "Synthetic", EndpointID: "synthetic-endpoint", UpstreamModel: "synthetic", Enabled: true}}
	cfg.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "synthetic-model"}
	if err := manager.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Publish(); err != nil {
		t.Fatal(err)
	}
	cfg = manager.Draft()
	cfg.Endpoints[0].APIFormat = ""
	cfg.Endpoints[0].DisplayName = "Edited by old UI"
	if err := manager.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Publish(); err != nil {
		t.Fatal(err)
	}
	reopened := New(manager.path)
	if reopened.Active().Endpoints[0].APIFormat != "custom" {
		t.Fatal("old UI cleared the API format")
	}
	if _, err := reopened.Provider(WorkloadDialogue, false, 0).Chat(context.Background(), core.ChatRequest{}); err == nil || !strings.Contains(err.Error(), "v16") {
		t.Fatalf("missing unsupported protocol diagnostic: %v", err)
	}
	if _, _, err := reopened.TestModel("synthetic-model"); err == nil {
		t.Fatal("old preview called an unsupported protocol")
	}
	if requests != 0 {
		t.Fatal("native request escaped the compatibility guard")
	}
}
