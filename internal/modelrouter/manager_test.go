package modelrouter

import (
	"path/filepath"
	"testing"
)

func TestSaveDraftPreservesGlobalReflectionInherit(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "model_router.json"))

	if err := manager.SaveDraft(manager.Draft()); err != nil {
		t.Fatalf("save default draft: %v", err)
	}

	binding := manager.Draft().GlobalBindings[WorkloadReflection]
	if binding.Mode != BindingInherit {
		t.Fatalf("reflection mode = %q, want %q", binding.Mode, BindingInherit)
	}
}
