package llm

import (
	"context"
	"strings"
	"testing"

	"FrostAgent/internal/skills"
	"FrostAgent/internal/storage"
)

func TestSkillsProgressiveLoadingManualInvocationAndReset(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec("INSERT INTO instances(id,name,created_at) VALUES ('synthetic-instance','Synthetic','now')"); err != nil {
		t.Fatal(err)
	}
	store := skills.NewStore(db, "synthetic-instance")
	var packages []skills.Package
	for _, spec := range []struct{ name, extra string }{{"automatic", ""}, {"manual", "disable-model-invocation: true\n"}, {"hidden", "user-invocable: false\n"}} {
		pkg, err := skills.ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: " + spec.name + "\ndescription: Testing\n" + spec.extra + "---\nINSTRUCTIONS-" + spec.name + " $ARGUMENTS")}, "")
		if err != nil {
			t.Fatal(err)
		}
		pkg.Info.Enabled = true
		packages = append(packages, pkg)
	}
	if err := store.ImportPackages(packages); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManager()
	session := manager.GetOrCreate("synthetic-session")
	engine := &Engine{Skills: store, SessionManager: manager}
	ctx := withRunContext(context.Background(), RunContext{SessionID: "synthetic-session"})
	base := []ChatMessage{{Role: "system", Content: "Primary system"}, {Role: "user", Content: "hello"}}
	messages, err := engine.skillMessages(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	catalog := messages[1].Content.(string)
	if !strings.Contains(catalog, "\"name\":\"automatic\"") || strings.Contains(catalog, "\"name\":\"manual\"") || strings.Contains(catalog, "INSTRUCTIONS-") {
		t.Fatalf("eager or incorrect catalog: %s", catalog)
	}
	requested := []ChatMessage{{Role: "user", Content: "/manual synthetic arguments"}}
	if err := engine.activateRequestedSkill(ctx, requested); err != nil {
		t.Fatal(err)
	}
	messages, err = engine.skillMessages(ctx, base)
	if err != nil || !strings.Contains(messages[1].Content.(string), "INSTRUCTIONS-manual synthetic arguments") {
		t.Fatalf("manual load: %v", err)
	}
	messages, err = engine.skillMessages(ctx, messages)
	if err != nil || len(messages) != 3 {
		t.Fatal("skill context duplicated")
	}
	if err := engine.activateRequestedSkill(ctx, []ChatMessage{{Role: "user", Content: "/hidden"}}); err == nil {
		t.Fatal("hidden skill manually invoked")
	}
	if err := store.SetEnabled("manual", false); err != nil {
		t.Fatal(err)
	}
	messages, err = engine.skillMessages(ctx, messages)
	if err != nil || strings.Contains(messages[1].Content.(string), "INSTRUCTIONS-manual") {
		t.Fatal("disabled skill remained in context")
	}
	if err := store.SetEnabled("manual", true); err != nil {
		t.Fatal(err)
	}
	if err := engine.activateRequestedSkill(ctx, requested); err != nil {
		t.Fatal(err)
	}
	session.Clear()
	messages, err = engine.skillMessages(ctx, messages)
	if err != nil || strings.Contains(messages[1].Content.(string), "INSTRUCTIONS-manual") {
		t.Fatal("reset did not clear active skills")
	}
	if len(base) != 2 || base[0].Content != "Primary system" {
		t.Fatal("source history mutated")
	}
	otherCtx := withRunContext(context.Background(), RunContext{SessionID: "synthetic-other-session"})
	manager.GetOrCreate("synthetic-other-session")
	messages, err = engine.skillMessages(otherCtx, base)
	if err != nil || strings.Contains(messages[1].Content.(string), "INSTRUCTIONS-manual") {
		t.Fatal("cross-session skill leakage")
	}
}

func TestSkillActivationRejectsStaleSessionEpoch(t *testing.T) {
	session := &SessionContext{}
	_, epoch, cancel := session.BeginRun(context.Background())
	defer cancel()
	if err := session.ActivateSkill("test", "revision", "", epoch); err != nil {
		t.Fatal(err)
	}
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	if err := session.ActivateSkill("test", "revision", "", epoch); err == nil {
		t.Fatal("old run reactivated skill after reset")
	}
	if session.HasActiveSkill("test", "revision") {
		t.Fatal("reset session retained an active skill")
	}
}

func TestManualSkillUsesCurrentRawMessageInsteadOfTransportContext(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec("INSERT INTO instances(id,name,created_at) VALUES ('synthetic-instance','Synthetic','now')"); err != nil {
		t.Fatal(err)
	}
	store := skills.NewStore(db, "synthetic-instance")
	pkg, err := skills.ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: manual\ndescription: Manual\ndisable-model-invocation: true\n---\nRAW-MANUAL-INSTRUCTIONS $ARGUMENTS")}, "")
	if err != nil {
		t.Fatal(err)
	}
	pkg.Info.Enabled = true
	if err := store.ImportPackages([]skills.Package{pkg}); err != nil {
		t.Fatal(err)
	}
	sessions := NewSessionManager()
	session := sessions.GetOrCreate("synthetic-session")
	provider := &mockTraceProvider{}
	engine := &Engine{Skills: store, SessionManager: sessions, Provider: provider, ModelName: "synthetic-model", MaxIterations: 2}
	wrapped := []ChatMessage{{Role: "user", Content: "User Message: /manual synthetic argument\n\n<system_context>metadata</system_context>"}}
	run := RunContext{SessionID: "synthetic-session", ActorUserID: "synthetic-actor"}
	run.MemorySource.Content = "/manual synthetic argument"
	result := engine.RunMessagesWithContext(wrapped, run)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	prompt, _ := session.LastPromptTrace()
	if !strings.Contains(prompt, "RAW-MANUAL-INSTRUCTIONS synthetic argument") || strings.Contains(prompt, "RAW-MANUAL-INSTRUCTIONS synthetic argument\n\n<system_context>") {
		t.Fatalf("manual input incorrectly derived: %s", prompt)
	}
	session.Clear()
	run.MemorySource.Content = "ordinary current message"
	result = engine.RunMessagesWithContext(wrapped, run)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	prompt, _ = session.LastPromptTrace()
	if strings.Contains(prompt, "RAW-MANUAL-INSTRUCTIONS") {
		t.Fatal("transport context invoked manual skill")
	}
}
