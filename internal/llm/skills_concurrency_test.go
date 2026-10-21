package llm

import (
	"context"
	"maps"
	"strings"
	"testing"

	"FrostAgent/internal/skills"
	"FrostAgent/internal/storage"
)

func skillConcurrencyFixture(t *testing.T) (*Engine, *SessionContext, *skills.Store, context.Context) {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SQL.Exec("INSERT INTO instances(id,name,created_at) VALUES ('synthetic-instance','Synthetic','now')"); err != nil {
		t.Fatal(err)
	}
	store := skills.NewStore(db, "synthetic-instance")
	sessions := NewSessionManager()
	session := sessions.GetOrCreate("synthetic-session")
	_, epoch, cancel := session.BeginRun(context.Background())
	t.Cleanup(cancel)
	for i, name := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		pkg, err := skills.ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\ndescription: Synthetic\n---\nINSTRUCTIONS-" + name + " $ARGUMENTS")}, "")
		if err != nil {
			t.Fatal(err)
		}
		pkg.Info.Enabled = true
		if err := store.ImportPackages([]skills.Package{pkg}); err != nil {
			t.Fatal(err)
		}
		if i < 4 {
			if err := session.ActivateSkill(name, pkg.Info.Revision, "synthetic arguments", epoch); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx := withRunContext(context.Background(), RunContext{SessionID: "synthetic-session", Epoch: epoch})
	return &Engine{Skills: store, SessionManager: sessions}, session, skills.NewStore(db, "synthetic-instance"), ctx
}

func TestSkillCleanupRetriesAfterReenableBeforeMutation(t *testing.T) {
	for _, path := range []string{"manual", "prompt"} {
		t.Run(path, func(t *testing.T) {
			engine, session, writer, ctx := skillConcurrencyFixture(t)
			before, epoch := session.skillSnapshotAtEpoch()
			if err := writer.SetEnabled("alpha", false); err != nil {
				t.Fatal(err)
			}
			// Freeze the exact F2 interleaving: disabled snapshot, completed
			// re-enable through another Store, then the production mutation.
			snapshot, err := engine.Skills.Snapshot("alpha", "bravo", "charlie", "delta")
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := snapshot.Instructions["alpha"]; exists {
				t.Fatal("snapshot did not observe disabled alpha")
			}
			if err := writer.SetEnabled("alpha", true); err != nil {
				t.Fatal(err)
			}
			var applied bool
			if path == "manual" {
				applied, err = engine.activateSkillSnapshot(ctx, "echo", "", snapshot)
			} else {
				applied, err = engine.reconcileSkillSnapshot(ctx, snapshot, session, maps.Clone(before), epoch)
			}
			if applied || err != nil {
				t.Fatalf("stale snapshot was not retried: applied=%v err=%v", applied, err)
			}
			if !maps.Equal(before, session.skillSnapshot()) {
				t.Fatal("old disabled observation removed a re-enabled activation")
			}
			if path == "manual" {
				err := engine.activateRequestedSkill(ctx, []ChatMessage{{Role: "user", Content: "/echo"}})
				if err == nil || !strings.Contains(err.Error(), "最多加载 4 个技能") {
					t.Fatalf("fresh state did not enforce valid capacity: %v", err)
				}
			} else {
				messages, err := engine.skillMessages(ctx, nil)
				if err != nil || !strings.Contains(messages[0].Content.(string), "INSTRUCTIONS-alpha synthetic arguments") {
					t.Fatalf("re-enabled instructions were lost: %v", err)
				}
			}
			if !maps.Equal(before, session.skillSnapshot()) {
				t.Fatal("fresh retry changed valid activations")
			}
		})
	}
}

func TestManualSkillTargetUsesSameStateAsCleanup(t *testing.T) {
	engine, session, writer, ctx := skillConcurrencyFixture(t)
	before := session.skillSnapshot()
	snapshot, err := engine.Skills.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.SetEnabled("alpha", false); err != nil {
		t.Fatal(err)
	}
	if applied, err := engine.activateSkillSnapshot(ctx, "alpha", "new arguments", snapshot); applied || err != nil {
		t.Fatalf("stale target accepted: applied=%v err=%v", applied, err)
	}
	err = engine.activateRequestedSkill(ctx, []ChatMessage{{Role: "user", Content: "/alpha new arguments"}})
	if err == nil || !strings.Contains(err.Error(), "已禁用") || !maps.Equal(before, session.skillSnapshot()) {
		t.Fatalf("disabled target changed session: %v", err)
	}
}

func TestPromptSkillCleanupRejectsChangedSessionSnapshot(t *testing.T) {
	for _, change := range []string{"arguments", "revision", "reset"} {
		t.Run(change, func(t *testing.T) {
			engine, session, writer, ctx := skillConcurrencyFixture(t)
			if err := writer.SetEnabled("alpha", false); err != nil {
				t.Fatal(err)
			}
			active, epoch := session.skillSnapshotAtEpoch()
			snapshot, err := engine.Skills.Snapshot("alpha", "bravo", "charlie", "delta")
			if err != nil {
				t.Fatal(err)
			}
			current := active["alpha"]
			switch change {
			case "arguments":
				current.Arguments = "new arguments"
			case "revision":
				current.Revision = "new-revision"
			case "reset":
				if err := session.ResetSession(nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := session.ActivateSkill("alpha", current.Revision, current.Arguments, session.Epoch()); err != nil {
				t.Fatal(err)
			}
			before := session.skillSnapshot()
			applied, err := engine.reconcileSkillSnapshot(ctx, snapshot, session, active, epoch)
			if change == "reset" {
				if err == nil {
					t.Fatal("old run accepted after reset")
				}
			} else if applied || err != nil {
				t.Fatalf("changed session not retried: applied=%v err=%v", applied, err)
			}
			if !maps.Equal(before, session.skillSnapshot()) {
				t.Fatal("old prompt iteration removed newer session state")
			}
		})
	}
}
