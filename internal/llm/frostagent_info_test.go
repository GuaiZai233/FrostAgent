package llm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

// newRoutedRouter builds a router through the public draft/save/publish path.
// The dialogue binding is configured by the caller via mutate.
func newRoutedRouter(t *testing.T, mutate func(cfg *modelrouter.Configuration)) *modelrouter.Manager {
	t.Helper()
	router := modelrouter.New(filepath.Join(t.TempDir(), "router.json"))
	cfg := router.Draft()
	cfg.Endpoints = []modelrouter.Endpoint{{ID: "endpoint", DisplayName: "Endpoint", BaseURL: "https://example.com/v1", Enabled: true}}
	cfg.Models = []modelrouter.Model{
		{ID: "global", DisplayName: "Global", EndpointID: "endpoint", UpstreamModel: "global-upstream", Enabled: true},
		{ID: "group", DisplayName: "Group", EndpointID: "endpoint", UpstreamModel: "group-upstream", Enabled: true},
	}
	mutate(&cfg)
	if err := router.SaveDraft(cfg); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if _, err := router.Publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return router
}

func groupDialogueOverride(modelID string) modelrouter.GroupOverride {
	return modelrouter.GroupOverride{
		Platform: "onebot",
		GroupID:  "123",
		Bindings: map[modelrouter.Workload]modelrouter.Binding{
			modelrouter.WorkloadDialogue: {Mode: modelrouter.BindingModel, ModelID: modelID},
		},
	}
}

func runCtxFor(router *modelrouter.Manager, scope modelrouter.Scope) context.Context {
	ctx := router.WithSnapshot(context.Background(), router.Snapshot())
	return WithRunContext(ctx, RunContext{
		RouteScope:    scope,
		RouteSnapshot: router.Snapshot(),
	})
}

func TestDialogueModelForRunHonorsGroupOverrideOverGlobal(t *testing.T) {
	router := newRoutedRouter(t, func(cfg *modelrouter.Configuration) {
		cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "global"}
		cfg.GroupOverrides = []modelrouter.GroupOverride{groupDialogueOverride("group")}
	})

	got, err := DialogueModelForRun(runCtxFor(router, modelrouter.Scope{Platform: "onebot", GroupID: "123"}), router)
	if err != nil {
		t.Fatalf("group request: %v", err)
	}
	if got != "group-upstream" {
		t.Fatalf("group request reported %q, want the group override model", got)
	}

	got, err = DialogueModelForRun(runCtxFor(router, modelrouter.Scope{Platform: "onebot", GroupID: "999"}), router)
	if err != nil {
		t.Fatalf("other group request: %v", err)
	}
	if got != "global-upstream" {
		t.Fatalf("unrelated group reported %q, want the global model", got)
	}
}

func TestDialogueModelForRunReportsGroupOnlyBinding(t *testing.T) {
	router := newRoutedRouter(t, func(cfg *modelrouter.Configuration) {
		cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingDisabled}
		cfg.GroupOverrides = []modelrouter.GroupOverride{groupDialogueOverride("group")}
	})

	got, err := DialogueModelForRun(runCtxFor(router, modelrouter.Scope{Platform: "onebot", GroupID: "123"}), router)
	if err != nil {
		t.Fatalf("group-only binding reported unavailable: %v", err)
	}
	if got != "group-upstream" {
		t.Fatalf("group-only binding reported %q", got)
	}

	if _, err := DialogueModelForRun(runCtxFor(router, modelrouter.Scope{Platform: "onebot", GroupID: "999"}), router); err == nil {
		t.Fatal("ungrouped request should be disabled when only the group binding is enabled")
	}
}

func TestDialogueModelForRunWithoutRequestUsesGlobalRoute(t *testing.T) {
	router := newRoutedRouter(t, func(cfg *modelrouter.Configuration) {
		cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "global"}
		cfg.GroupOverrides = []modelrouter.GroupOverride{groupDialogueOverride("group")}
	})
	got, err := DialogueModelForRun(context.Background(), router)
	if err != nil {
		t.Fatalf("out-of-band query: %v", err)
	}
	if got != "global-upstream" {
		t.Fatalf("out-of-band query reported %q, want the global model", got)
	}
}

func TestDialogueModelForRunWithoutRouterIsUnavailable(t *testing.T) {
	if _, err := DialogueModelForRun(context.Background(), nil); err == nil {
		t.Fatal("nil router should be reported as unavailable")
	}
}
