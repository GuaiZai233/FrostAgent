package instance

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstancesHaveSeparatedMaxIterations(t *testing.T) {
	m := testManager(t)

	// Instance A: uses default configuration
	infoA := create(t, m, "instance-default")
	if err := m.Enable(infoA.ID, true); err != nil {
		t.Fatal(err)
	}
	itemA := m.instances[infoA.ID]
	if itemA.runtime == nil || itemA.runtime.Engine == nil {
		t.Fatal("instance A runtime/engine is nil")
	}
	if itemA.runtime.Engine.MaxIterations != llm.DefaultMaxIterations {
		t.Fatalf("expected instance A MaxIterations to default to %d, got %d", llm.DefaultMaxIterations, itemA.runtime.Engine.MaxIterations)
	}
	if itemA.runtime.Engine.EffectiveMaxIterations() != llm.DefaultMaxIterations {
		t.Fatalf("expected instance A EffectiveMaxIterations to be %d, got %d", llm.DefaultMaxIterations, itemA.runtime.Engine.EffectiveMaxIterations())
	}

	// Instance B: configured with AGENT_MAX_ITERATIONS=12
	infoB := create(t, m, "instance-custom-12")
	itemB := m.instances[infoB.ID]
	if err := itemB.config.Update("AGENT_MAX_ITERATIONS", "12", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(infoB.ID, true); err != nil {
		t.Fatal(err)
	}
	if itemB.runtime == nil || itemB.runtime.Engine == nil {
		t.Fatal("instance B runtime/engine is nil")
	}
	if itemB.runtime.Engine.MaxIterations != 12 {
		t.Fatalf("expected instance B MaxIterations to be 12, got %d", itemB.runtime.Engine.MaxIterations)
	}
	if itemB.runtime.Engine.EffectiveMaxIterations() != 12 {
		t.Fatalf("expected instance B EffectiveMaxIterations to be 12, got %d", itemB.runtime.Engine.EffectiveMaxIterations())
	}

	// Instance C: configured with AGENT_MAX_ITERATIONS=50
	infoC := create(t, m, "instance-custom-50")
	itemC := m.instances[infoC.ID]
	if err := itemC.config.Update("AGENT_MAX_ITERATIONS", "50", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(infoC.ID, true); err != nil {
		t.Fatal(err)
	}
	if itemC.runtime == nil || itemC.runtime.Engine == nil {
		t.Fatal("instance C runtime/engine is nil")
	}
	if itemC.runtime.Engine.MaxIterations != 50 {
		t.Fatalf("expected instance C MaxIterations to be 50, got %d", itemC.runtime.Engine.MaxIterations)
	}
	if itemC.runtime.Engine.EffectiveMaxIterations() != 50 {
		t.Fatalf("expected instance C EffectiveMaxIterations to be 50, got %d", itemC.runtime.Engine.EffectiveMaxIterations())
	}

	// Ensure instances A, B, C are completely isolated from each other
	if itemA.runtime.Engine.EffectiveMaxIterations() == itemB.runtime.Engine.EffectiveMaxIterations() {
		t.Fatalf("instance A and B should have different max iterations")
	}
	if itemB.runtime.Engine.EffectiveMaxIterations() == itemC.runtime.Engine.EffectiveMaxIterations() {
		t.Fatalf("instance B and C should have different max iterations")
	}
}

func TestInstanceRestartRequiredOnMaxIterationsChange(t *testing.T) {
	if !instanceconfig.InstanceRestartKeys["AGENT_MAX_ITERATIONS"] {
		t.Fatal("AGENT_MAX_ITERATIONS must be registered in InstanceRestartKeys")
	}

	m := testManager(t)
	info := create(t, m, "instance-restart-test")
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}

	// Make an HTTP call through m.ServeHTTP to update the env var on this instance
	req := httptest.NewRequest(
		http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar",
		strings.NewReader(`{"key":"AGENT_MAX_ITERATIONS","value":"60"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateEnvVar failed with code %d: %s", w.Code, w.Body.String())
	}

	// Lookup instance info and check RestartRequired
	instances, _ := m.List()
	var updated *Info
	for i := range instances {
		if instances[i].ID == info.ID {
			updated = &instances[i]
			break
		}
	}
	if updated == nil {
		t.Fatal("instance not found")
	}
	if !updated.RestartRequired {
		t.Fatal("expected RestartRequired to be true after AGENT_MAX_ITERATIONS changed")
	}
}

func TestInstanceMaxIterationsLifecycleSemantics(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "instance-lifecycle-test")

	// Pre-configure AGENT_MAX_ITERATIONS=15 before enabling
	item := m.instances[info.ID]
	if err := item.config.Update("AGENT_MAX_ITERATIONS", "15", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}

	// 1. Initial running engine should use 15, and RestartRequired should be false
	if item.runtime.Engine.EffectiveMaxIterations() != 15 {
		t.Fatalf("expected initial EffectiveMaxIterations to be 15, got %d", item.runtime.Engine.EffectiveMaxIterations())
	}
	if item.runtime.Engine.MaxIterations != 15 {
		t.Fatalf("expected initial MaxIterations to be 15, got %d", item.runtime.Engine.MaxIterations)
	}
	instances, _ := m.List()
	for _, inst := range instances {
		if inst.ID == info.ID && inst.RestartRequired {
			t.Fatal("expected RestartRequired to be false initially")
		}
	}

	// 2. Update AGENT_MAX_ITERATIONS to 40 via Settings API
	req := httptest.NewRequest(
		http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.SettingsService/UpdateEnvVar",
		strings.NewReader(`{"key":"AGENT_MAX_ITERATIONS","value":"40"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateEnvVar failed with code %d: %s", w.Code, w.Body.String())
	}

	// 3. RestartRequired must now be true
	instances, _ = m.List()
	restartRequiredFound := false
	for _, inst := range instances {
		if inst.ID == info.ID {
			restartRequiredFound = inst.RestartRequired
			break
		}
	}
	if !restartRequiredFound {
		t.Fatal("expected RestartRequired to be true after AGENT_MAX_ITERATIONS update")
	}

	// 4. Verify running engine still uses old value (15), NOT the new value (40)
	if item.runtime.Engine.EffectiveMaxIterations() != 15 {
		t.Fatalf("running engine must continue using old EffectiveMaxIterations=15 before restart, got %d", item.runtime.Engine.EffectiveMaxIterations())
	}
	if item.runtime.Engine.MaxIterations != 15 {
		t.Fatalf("running engine must continue using old MaxIterations=15 before restart, got %d", item.runtime.Engine.MaxIterations)
	}

	// 5. Restart/re-enable instance
	if err := m.Enable(info.ID, false); err != nil {
		t.Fatalf("failed to disable instance: %v", err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatalf("failed to re-enable instance: %v", err)
	}

	// 6. Verify RestartRequired is now cleared
	instances, _ = m.List()
	for _, inst := range instances {
		if inst.ID == info.ID && inst.RestartRequired {
			t.Fatal("expected RestartRequired to be false after restart")
		}
	}

	// 7. Verify new running engine has the updated value (40)
	restartedItem := m.instances[info.ID]
	if restartedItem.runtime.Engine.EffectiveMaxIterations() != 40 {
		t.Fatalf("restarted engine must use new EffectiveMaxIterations=40, got %d", restartedItem.runtime.Engine.EffectiveMaxIterations())
	}
	if restartedItem.runtime.Engine.MaxIterations != 40 {
		t.Fatalf("restarted engine must use new MaxIterations=40, got %d", restartedItem.runtime.Engine.MaxIterations)
	}
}

func TestTemplateContainsDefaultMaxIterations(t *testing.T) {
	if !strings.Contains(instanceconfig.Template, "AGENT_MAX_ITERATIONS=35") {
		t.Fatalf("instanceconfig.Template should contain AGENT_MAX_ITERATIONS=35, got:\n%s", instanceconfig.Template)
	}
}
