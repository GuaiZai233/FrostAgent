package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/storage"
)

type zipEntry struct {
	name, content string
	mode          os.FileMode
}

func archiveFor(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(member, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestImportHarnessLayoutsAndMetadata(t *testing.T) {
	for _, prefix := range []string{"", "review/", "repo/.claude/skills/review/", "repo/.agents/skills/review/", "repo/.codex/skills/review/", ".gemini/skills/review/", "repo/skills/review/"} {
		t.Run(prefix, func(t *testing.T) {
			packages, err := ParseZIP(archiveFor(t, zipEntry{prefix + "SKILL.md", "---\nname: review\ndescription: Review code\ndisable-model-invocation: true\nallowed-tools: [Read, Bash]\ncontext: fork\n---\nReview $ARGUMENTS", 0600}, zipEntry{prefix + "references/check.md", "reference", 0600}))
			if err != nil {
				t.Fatal(err)
			}
			pkg := packages[0]
			if pkg.Info.Name != "review" || !pkg.Info.ManualOnly || pkg.Info.Enabled || len(pkg.Info.Warnings) != 2 || string(pkg.Files["references/check.md"]) != "reference" {
				t.Fatalf("unexpected package: %+v", pkg.Info)
			}
		})
	}
	packages, err := ParseZIP(archiveFor(t, zipEntry{"review/SKILL.md", "---\ndescription: Review\n---\nInstructions", 0600}))
	if err != nil || packages[0].Info.Name != "review" {
		t.Fatalf("Claude directory fallback: %v", err)
	}
}

func TestUnsafeAndAmbiguousZIPRejected(t *testing.T) {
	skill := zipEntry{"review/SKILL.md", "---\nname: review\ndescription: Review\n---\nInstructions", 0600}
	for _, bad := range []zipEntry{
		{"../escape", "", 0600}, {"/absolute", "", 0600}, {"C:/drive", "", 0600}, {"review/../escape", "", 0600},
		{"review\\script", "", 0600}, {"review/link", "elsewhere", os.ModeSymlink | 0777},
		{"review/link/", "", os.ModeSymlink | 0777},
		{"review/SKILL.md", "duplicate", 0600}, {"REVIEW/skill.md", "case collision", 0600},
		{"review/nested/SKILL.md", "---\nname: nested\ndescription: Nested\n---\nInstructions", 0600},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if _, err := ParseZIP(archiveFor(t, skill, bad)); err == nil {
				t.Fatal("unsafe ZIP accepted")
			}
		})
	}
	if _, err := ParseZIP(archiveFor(t, zipEntry{"SKILL.md", "---\nname: review\ndescription: Review\n---\n" + strings.Repeat("x", MaxInstructionBytes), 0600})); err == nil {
		t.Fatal("oversized instructions accepted")
	}
	if _, err := ParseZIP(archiveFor(t, skill, zipEntry{"review/assets/huge", strings.Repeat("x", MaxPackageBytes), 0600})); err == nil {
		t.Fatal("oversized expanded skill accepted")
	}
}

func skillTestStore(t *testing.T) (*storage.DB, *Store) {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SQL.Exec("INSERT INTO instances(id,name,created_at) VALUES ('synthetic-instance','Synthetic','now'), ('synthetic-other','Other','now')"); err != nil {
		t.Fatal(err)
	}
	return db, NewStore(db, "synthetic-instance")
}

func TestStoreImportAtomicIsolationPersistenceAndDelete(t *testing.T) {
	db, store := skillTestStore(t)
	first := archiveFor(t, zipEntry{"one/SKILL.md", "---\nname: one\ndescription: One\n---\nOne instructions", 0600})
	infos, err := store.ImportZIP(first)
	if err != nil || len(infos) != 1 || infos[0].Enabled {
		t.Fatalf("import: %v %+v", err, infos)
	}
	if err := store.SetEnabled("one", true); err != nil {
		t.Fatal(err)
	}
	pkg, err := NewStore(db, "synthetic-instance").Enabled("one")
	if err != nil || pkg.Body != "One instructions" {
		t.Fatalf("persisted package: %v", err)
	}
	if list, err := NewStore(db, "synthetic-other").List(); err != nil || len(list) != 0 {
		t.Fatalf("instance leakage: %v", err)
	}
	duplicate := archiveFor(t, zipEntry{"new/SKILL.md", "---\nname: new\ndescription: New\n---\nNew instructions", 0600}, zipEntry{"one/SKILL.md", "---\nname: one\ndescription: One\n---\nReplacement", 0600})
	if _, err := store.ImportZIP(duplicate); err == nil {
		t.Fatal("conflict accepted")
	}
	if infos, err := store.List(); err != nil || len(infos) != 1 {
		t.Fatalf("partial import: %v %+v", err, infos)
	}
	if err := store.SetEnabled("one", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enabled("one"); err == nil {
		t.Fatal("disabled skill available")
	}
	if err := store.Delete("one"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("one"); err == nil {
		t.Fatal("deleted skill available")
	}
	if _, err := store.ImportZIP(first); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec("DELETE FROM instances WHERE id = 'synthetic-instance'"); err != nil {
		t.Fatal(err)
	}
	if infos, err := store.List(); err != nil || len(infos) != 0 {
		t.Fatal("instance deletion left skills")
	}
}

type recordingSandbox struct {
	calls  []sandbox.ExecRequest
	failAt int
	code   int
}

func (b *recordingSandbox) Exec(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	b.calls = append(b.calls, req)
	code := 0
	if b.failAt == len(b.calls) {
		code = 1
	}
	result := sandbox.ExecResult{ExitCode: &code}
	if len(b.calls) == 1 {
		result.Stdout = "/sandbox/frostagent-skill-Synthetic123\n"
	}
	if len(b.calls) == 4 {
		code = b.code
		result.Stdout = "script output"
	}
	return result, nil
}
func (*recordingSandbox) Release(context.Context, string) error { return nil }
func (*recordingSandbox) Health(context.Context) error          { return nil }

func TestSandboxTransfersResourcesAndPreservesCommandFailure(t *testing.T) {
	pkg, err := ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: run\ndescription: Run\n---\nRun scripts/task.py"), "scripts/task.py": []byte("print('resource')")}, "")
	if err != nil {
		t.Fatal(err)
	}
	backend := &recordingSandbox{code: 7}
	result, cwd, err := Run(context.Background(), backend, "synthetic-session", pkg, "python3 scripts/task.py", 30*time.Second)
	if err != nil || result.ExitCode == nil || *result.ExitCode != 7 || cwd == "" {
		t.Fatalf("command result: %+v %v", result, err)
	}
	if len(backend.calls) != 4 || backend.calls[3].Cwd != cwd || !strings.HasSuffix(backend.calls[3].Command, "python3 scripts/task.py") || !strings.Contains(backend.calls[3].Command, "CLAUDE_SKILL_DIR=") {
		t.Fatalf("unexpected calls: %+v", backend.calls)
	}
	for _, call := range backend.calls {
		if call.SessionID != "synthetic-session" {
			t.Fatal("session routing lost")
		}
	}
	backend = &recordingSandbox{failAt: 2}
	if _, _, err := Run(context.Background(), backend, "synthetic-session", pkg, "python3 scripts/task.py", 30*time.Second); err == nil || len(backend.calls) != 2 {
		t.Fatal("preparation failure did not stop execution")
	}
	if _, _, err := Run(context.Background(), nil, "synthetic-session", pkg, "echo test", 30*time.Second); err == nil {
		t.Fatal("nil backend succeeded")
	}
	if _, _, err := Run(context.Background(), backend, "", pkg, "echo test", 30*time.Second); err == nil {
		t.Fatal("missing session succeeded")
	}
}

func TestExecutableFlagsSurviveImportStoreAndValidation(t *testing.T) {
	_, store := skillTestStore(t)
	archive := archiveFor(t,
		zipEntry{"sample/SKILL.md", "---\nname: sample\ndescription: Sample\n---\nRun ./scripts/task.sh", 0644},
		zipEntry{"sample/scripts/task.sh", "#!/bin/sh\nprintf 'synthetic\\n'\n", os.ModeSetuid | 0755},
		zipEntry{"sample/references/plain.txt", "plain", 0644},
	)
	if _, err := store.ImportZIP(archive); err != nil {
		t.Fatal(err)
	}
	packages, err := store.Export()
	if err != nil || len(packages) != 1 {
		t.Fatalf("export: %v", err)
	}
	pkg := packages[0]
	if len(pkg.Executables) != 1 || pkg.Executables[0] != "scripts/task.sh" {
		t.Fatalf("executable flags lost: %v", pkg.Executables)
	}
	plain, err := ParsePackage(pkg.Files, pkg.Info.Name)
	if err != nil || plain.Info.Revision == pkg.Info.Revision {
		t.Fatal("executable flag missing from revision")
	}
	for _, flags := range [][]string{{"missing.sh"}, {"../escape"}, {"scripts/task.sh", "scripts/task.sh"}} {
		corrupt := pkg
		corrupt.Executables = flags
		if _, err := ValidatePackages([]Package{corrupt}); err == nil {
			t.Fatalf("invalid executable metadata accepted: %v", flags)
		}
	}
}
