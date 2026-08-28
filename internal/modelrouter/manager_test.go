package modelrouter

import (
	"path/filepath"
	"testing"
)

func TestSaveDraftPreservesGlobalReflectionFollowDialogue(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "model_router.json"))

	if err := manager.SaveDraft(manager.Draft()); err != nil {
		t.Fatalf("save default draft: %v", err)
	}

	binding := manager.Draft().GlobalBindings[WorkloadReflection]
	if binding.Mode != BindingFollowDialogue {
		t.Fatalf("reflection mode = %q, want %q", binding.Mode, BindingFollowDialogue)
	}
}

func TestFollowDialogueUsesEffectiveGroupDialogue(t *testing.T) {
	cfg := defaultConfiguration()
	cfg.Endpoints = []Endpoint{{ID: "endpoint", DisplayName: "Endpoint", BaseURL: "https://example.com/v1", Enabled: true}}
	cfg.Models = []Model{
		{ID: "global", DisplayName: "Global", EndpointID: "endpoint", UpstreamModel: "global", Enabled: true},
		{ID: "group", DisplayName: "Group", EndpointID: "endpoint", UpstreamModel: "group", Enabled: true},
	}
	cfg.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "global"}
	cfg.GlobalBindings[WorkloadVision] = Binding{Mode: BindingFollowDialogue}
	cfg.GroupOverrides = []GroupOverride{{
		Platform: "onebot",
		GroupID:  "123",
		Bindings: map[Workload]Binding{WorkloadDialogue: {Mode: BindingModel, ModelID: "group"}},
	}}

	target, err := resolveConfiguration(cfg, WorkloadVision, Scope{Platform: "onebot", GroupID: "123"})
	if err != nil {
		t.Fatalf("resolve vision: %v", err)
	}
	if target.ModelID != "group" {
		t.Fatalf("vision model = %q, want group dialogue model", target.ModelID)
	}
}
