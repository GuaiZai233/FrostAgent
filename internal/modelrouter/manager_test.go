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

func TestGroupOverridePlatformParity(t *testing.T) {
	cfg := defaultConfiguration()
	cfg.Endpoints = []Endpoint{{ID: "endpoint", DisplayName: "Endpoint", BaseURL: "https://example.com/v1", Enabled: true}}
	cfg.Models = []Model{
		{ID: "global", DisplayName: "Global", EndpointID: "endpoint", UpstreamModel: "global", Enabled: true},
		{ID: "group_custom", DisplayName: "Group Custom", EndpointID: "endpoint", UpstreamModel: "group_custom", Enabled: true},
	}
	cfg.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "global"}
	// Override configured with platform "qq"
	cfg.GroupOverrides = []GroupOverride{{
		Platform: "qq",
		GroupID:  "test_grp_42",
		Bindings: map[Workload]Binding{WorkloadDialogue: {Mode: BindingModel, ModelID: "group_custom"}},
	}}

	// Resolving under AstrBot's "aiocqhttp"
	targetAstrBot, err := resolveConfiguration(cfg, WorkloadDialogue, Scope{Platform: "aiocqhttp", GroupID: "test_grp_42"})
	if err != nil {
		t.Fatalf("resolve astrbot scope: %v", err)
	}
	if targetAstrBot.ModelID != "group_custom" {
		t.Fatalf("astrbot target model = %q, want group_custom", targetAstrBot.ModelID)
	}

	// Resolving under native OneBot's "onebot"
	targetOneBot, err := resolveConfiguration(cfg, WorkloadDialogue, Scope{Platform: "onebot", GroupID: "test_grp_42"})
	if err != nil {
		t.Fatalf("resolve onebot scope: %v", err)
	}
	if targetOneBot.ModelID != "group_custom" {
		t.Fatalf("onebot target model = %q, want group_custom", targetOneBot.ModelID)
	}
}

func TestUnconfiguredCopiedCredentialIsPlannedAsDeletion(t *testing.T) {
	actions := PlanCredentialPromotions(
		"guaitech.frostagent/transaction/a1b2c3d4/test",
		[]CredentialChange{{Target: "guaitech.frostagent/endpoint/endpoint_test"}},
		nil,
	)
	if len(actions) != 1 || !actions[0].Delete || actions[0].StagedTarget != "" {
		t.Fatalf("empty credential must not depend on a nonexistent staged secret: %+v", actions)
	}
}
