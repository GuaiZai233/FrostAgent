package llm

import (
	"context"
	"fmt"
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

	// A superseded pending input still carries its explicit manual-only skill
	// activation into the surviving input, without publishing arguments to B.
	session.Clear()
	first := session.ReserveSequenceTurn("A", true, false)
	next := session.ReserveSequenceTurn("A", true, false)
	first.Wait()
	firstMessages, _, _ := session.PrepareTurnMessages(first, "/manual queued-argument", "wrapped input", nil)
	run.Turn, run.Epoch = first, first.Epoch()
	run.MemorySource.Content = "/manual queued-argument"
	if old := engine.RunMessagesWithContext(firstMessages, run); !old.Silent {
		t.Fatalf("pending input ran: %+v", old)
	}
	first.Done()
	next.Wait()
	nextMessages, _, _ := session.PrepareTurnMessages(next, "next", "next", nil)
	run.Turn, run.Epoch = next, next.Epoch()
	run.MemorySource.Content = "ordinary follow-up"
	if result := engine.RunMessagesWithContext(nextMessages, run); result.Error != nil {
		t.Fatal(result.Error)
	}
	prompt, _ = session.LastPromptTrace()
	if !strings.Contains(prompt, "RAW-MANUAL-INSTRUCTIONS queued-argument") {
		t.Fatalf("missing resumed skill in parent prompt inspector: %s", prompt)
	}
	if session.HasActiveSkill("manual", pkg.Info.Revision) {
		t.Fatal("private skill activation leaked to shared session")
	}
	next.Done()

	// Storage failures during per-iteration Skills refresh must retain the
	// prepared checkpoint even when there is a nonempty public prefix.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	failing := session.ReserveSequenceTurn("B", true, false)
	failing.Wait()
	failingMessages, _, _ := session.PrepareTurnMessages(failing, "error-input", "error-input", nil)
	run.Turn, run.Epoch = failing, failing.Epoch()
	result = engine.RunMessagesWithContext(failingMessages, run)
	if result.Error == nil || !strings.Contains(fmt.Sprint(result.Transcript), "error-input") {
		t.Fatalf("failed refresh lost checkpoint: %+v", result)
	}
	failing.saveTranscript(nil) // Defensively retain checkpoint for malformed callers.
	failing.Done()
}

func TestManualSkillActivationReclaimsInvalidSlotsBeforeCapacityCheck(t *testing.T) {
	for _, change := range []string{"disabled", "deleted", "replaced", "all-valid"} {
		t.Run(change, func(t *testing.T) {
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
			for _, name := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
				pkg, err := skills.ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\ndescription: Synthetic\n---\nINSTRUCTIONS-" + name + " $ARGUMENTS")}, "")
				if err != nil {
					t.Fatal(err)
				}
				pkg.Info.Enabled = true
				packages = append(packages, pkg)
			}
			if err := store.ImportPackages(packages); err != nil {
				t.Fatal(err)
			}
			sessions := NewSessionManager()
			session := sessions.GetOrCreate("synthetic-session")
			engine := &Engine{Skills: store, SessionManager: sessions, Provider: &mockTraceProvider{}, ModelName: "synthetic-model", MaxIterations: 2}
			invoke := func(name string) AgentRunResult {
				prompt := "/" + name + " synthetic-" + name
				run := RunContext{SessionID: "synthetic-session", ActorUserID: "synthetic-actor"}
				run.MemorySource.Content = prompt
				return engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "User Message: " + prompt}}, run)
			}
			for _, pkg := range packages[:4] {
				if result := invoke(pkg.Info.Name); result.Error != nil {
					t.Fatalf("initial activation %s: %v", pkg.Info.Name, result.Error)
				}
			}
			before := session.skillSnapshot()
			if len(before) != 4 {
				t.Fatalf("expected four active skills, got %d", len(before))
			}
			switch change {
			case "disabled":
				err = store.SetEnabled("alpha", false)
			case "deleted":
				err = store.Delete("alpha")
			case "replaced":
				if err = store.Delete("alpha"); err != nil {
					t.Fatal(err)
				}
				replacement, parseErr := skills.ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: alpha\ndescription: Replacement\n---\nREPLACEMENT-INSTRUCTIONS")}, "")
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				replacement.Info.Enabled = true
				err = store.ImportPackages([]skills.Package{replacement})
			}
			if err != nil {
				t.Fatal(err)
			}
			// No ordinary message or prompt refresh occurs between mutation and /echo.
			result := invoke("echo")
			if change == "all-valid" {
				if result.Error == nil || !strings.Contains(result.Error.Error(), "最多加载 4 个技能") {
					t.Fatalf("valid capacity limit was bypassed: %+v", result)
				}
				if session.HasActiveSkill("echo", packages[4].Info.Revision) || len(session.skillSnapshot()) != 4 {
					t.Fatal("rejected activation changed the active skill set")
				}
			} else {
				if result.Error != nil {
					t.Fatalf("invalid skill still occupies a slot: %v", result.Error)
				}
				if !session.HasActiveSkill("echo", packages[4].Info.Revision) || len(session.skillSnapshot()) != 4 {
					t.Fatal("new skill did not take the invalid slot")
				}
				if _, exists := session.skillSnapshot()["alpha"]; exists {
					t.Fatal("invalid skill retained or replacement implicitly activated")
				}
				prompt, _ := session.LastPromptTrace()
				if !strings.Contains(prompt, "INSTRUCTIONS-echo synthetic-echo") || strings.Contains(prompt, "INSTRUCTIONS-alpha") || strings.Contains(prompt, "REPLACEMENT-INSTRUCTIONS") {
					t.Fatalf("unexpected active instructions: %s", prompt)
				}
			}
			after := session.skillSnapshot()
			for _, name := range []string{"bravo", "charlie", "delta"} {
				if after[name] != before[name] {
					t.Fatalf("valid skill changed: %s", name)
				}
			}
		})
	}
}

func TestSkillCapacityCleanupPreservesNewerActivation(t *testing.T) {
	session := &SessionContext{}
	_, epoch, cancel := session.BeginRun(context.Background())
	defer cancel()
	for _, name := range []string{"alpha", "bravo", "charlie", "delta"} {
		if err := session.ActivateSkill(name, "original", "", epoch); err != nil {
			t.Fatal(err)
		}
	}
	invalid := map[string]activeSkill{"alpha": session.skillSnapshot()["alpha"]}
	if err := session.ActivateSkill("alpha", "replacement", "new arguments", epoch); err != nil {
		t.Fatal(err)
	}
	if err := session.activateSkill("echo", "new", "", epoch, invalid); err == nil {
		t.Fatal("stale cleanup removed a newer activation to make room")
	}
	if !session.HasActiveSkill("alpha", "replacement") || session.HasActiveSkill("echo", "new") || len(session.skillSnapshot()) != 4 {
		t.Fatal("stale cleanup changed the current active skill set")
	}
}

func TestSkillCapacityCleanupRejectsStaleEpochBeforeMutation(t *testing.T) {
	session := &SessionContext{}
	_, epoch, cancel := session.BeginRun(context.Background())
	defer cancel()
	if err := session.ActivateSkill("alpha", "revision", "arguments", epoch); err != nil {
		t.Fatal(err)
	}
	invalid := session.skillSnapshot()
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	if err := session.ActivateSkill("alpha", "revision", "arguments", session.Epoch()); err != nil {
		t.Fatal(err)
	}
	if err := session.activateSkill("echo", "new", "", epoch, invalid); err == nil {
		t.Fatal("old run activated a skill after reset")
	}
	if !session.HasActiveSkill("alpha", "revision") || session.HasActiveSkill("echo", "new") || len(session.skillSnapshot()) != 1 {
		t.Fatal("old run removed a skill from the new session")
	}
}
