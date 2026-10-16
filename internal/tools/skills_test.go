package tools

import (
	"context"
	"strings"
	"testing"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/skills"
	"FrostAgent/internal/storage"
)

func TestSkillToolsLoadResourceAndRejectDisabledOrManualOnly(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec("INSERT INTO instances(id,name,created_at) VALUES ('synthetic-instance','Synthetic','now')"); err != nil {
		t.Fatal(err)
	}
	store := skills.NewStore(db, "synthetic-instance")
	pkg, err := skills.ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: tool-test\ndescription: Tool testing\n---\nRead references/test.md"), "references/test.md": []byte("synthetic reference")}, "")
	if err != nil {
		t.Fatal(err)
	}
	pkg.Info.Enabled = true
	if err := store.ImportPackages([]skills.Package{pkg}); err != nil {
		t.Fatal(err)
	}
	manager := llm.NewSessionManager()
	manager.GetOrCreate("synthetic-session")
	toolset := SkillTools(store, nil, manager)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "synthetic-session"})
	if _, err := toolset[1].ExecuteContext(ctx, `{"name":"tool-test","path":"references/test.md"}`); err == nil {
		t.Fatal("unloaded resource readable")
	}
	if _, err := toolset[0].ExecuteContext(ctx, `{"name":"tool-test"}`); err != nil {
		t.Fatal(err)
	}
	if text, err := toolset[1].ExecuteContext(ctx, `{"name":"tool-test","path":"references/test.md"}`); err != nil || text != "synthetic reference" {
		t.Fatalf("resource result %q: %v", text, err)
	}
	if _, err := toolset[1].ExecuteContext(ctx, `{"name":"tool-test","path":"../escape"}`); err == nil {
		t.Fatal("unsafe resource lookup succeeded")
	}
	if _, err := toolset[2].ExecuteContext(ctx, `{"name":"tool-test","command":"echo test"}`); err == nil {
		t.Fatal("script ran without sandbox")
	}
	if err := store.SetEnabled("tool-test", false); err != nil {
		t.Fatal(err)
	}
	if _, err := toolset[1].ExecuteContext(ctx, `{"name":"tool-test","path":"references/test.md"}`); err == nil {
		t.Fatal("disabled resource accessible")
	}
	if _, err := toolset[0].ExecuteContext(context.Background(), `{"name":"tool-test"}`); err == nil {
		t.Fatal("missing session accepted")
	}
	manual, err := skills.ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: manual\ndescription: Manual\ndisable-model-invocation: true\n---\nManual instructions")}, "")
	if err != nil {
		t.Fatal(err)
	}
	manual.Info.Enabled = true
	if err := store.ImportPackages([]skills.Package{manual}); err != nil {
		t.Fatal(err)
	}
	if _, err := toolset[0].ExecuteContext(ctx, `{"name":"manual"}`); err == nil || !strings.Contains(err.Error(), "手动") {
		t.Fatal("manual-only skill auto-loaded")
	}
}
