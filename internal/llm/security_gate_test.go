package llm

import (
	"errors"
	"testing"

	"FrostAgent/internal/security"
)

func TestEngineRejectsLockedActorBeforeProviderExecution(t *testing.T) {
	controller := security.NewController(t.TempDir())
	principal, err := security.NewPrincipal("test-platform", "actor-under-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Access.Lock(principal, "test lock"); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{Security: controller}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		ActorPlatform: "test-platform",
		ActorUserID:   "actor-under-test",
	})
	if !result.Silent || !errors.Is(result.Error, security.ErrLocked) {
		t.Fatalf("locked actor should be rejected before execution: silent=%v err=%v", result.Silent, result.Error)
	}
}
