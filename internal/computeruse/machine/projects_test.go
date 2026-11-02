package machine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/computeruse/bridge"
)

func testProjects(t *testing.T) (*Projects, Project) {
	t.Helper()
	root := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell, err = exec.LookPath("pwsh")
		if err != nil {
			shell, err = exec.LookPath("powershell")
		}
		if err != nil {
			t.Skip("PowerShell unavailable")
		}
	}
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, output)
		}
	}
	run("init", "-b", "main")
	run("-c", "user.name=SyntheticFixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "synthetic")
	p, err := OpenProjects(root, filepath.Join(t.TempDir(), "state"), git)
	if err != nil {
		t.Fatal(err)
	}
	project, err := p.Register(context.Background(), Project{ID: "synthetic-project", Path: repo, Baseline: "main", Shell: shell})
	if err != nil {
		t.Fatal(err)
	}
	return p, project
}

func TestWorktreesPreserveDirtyMainAndFencePause(t *testing.T) {
	p, project := testProjects(t)
	if err := os.WriteFile(filepath.Join(project.Path, "uncommitted"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := p.Prepare(context.Background(), project.ID, "synthetic-task", 1)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(w.Path, ".git"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("worktree must use a .git file", err)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "uncommitted")); !os.IsNotExist(err) {
		t.Fatal("dirty main was copied")
	}
	if err := p.Pause(w.TaskID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Prepare(context.Background(), project.ID, w.TaskID, 1); !errors.Is(err, ErrPaused) {
		t.Fatal("old prepare revoked pause", err)
	}
	if _, err := p.Prepare(context.Background(), project.ID, w.TaskID, 2); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenProjects(p.root, filepath.Dir(p.file), p.git)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Workspace(w.TaskID, 2); !errors.Is(err, ErrPaused) {
		t.Fatal("reboot resumed task")
	}
	if _, err := reopened.Prepare(context.Background(), project.ID, w.TaskID, 2); !errors.Is(err, ErrPaused) {
		t.Fatal("reboot accepted old generation")
	}
	if err := reopened.Pause("not-prepared", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Prepare(context.Background(), project.ID, "not-prepared", 3); !errors.Is(err, ErrPaused) {
		t.Fatal("delayed prepare escaped tombstone")
	}
	if err := reopened.Archive(project.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Path); err != nil {
		t.Fatal("archive removed files", err)
	}
}

func TestProjectValidationPrivacyAndCopy(t *testing.T) {
	p, project := testProjects(t)
	for _, remote := range []string{"ssh://user:synthetic-secret@host/repo", "https://user:synthetic-secret@host/%zz", "https://user:synthetic-secret@host/repo"} {
		project.Remote = remote
		if _, err := p.Register(context.Background(), project); !errors.Is(err, ErrInvalid) {
			t.Fatalf("credential remote accepted: %v", err)
		}
	}
	project.Remote = ""
	project.Environment = map[string]string{"PATH": os.Getenv("PATH")}
	if _, err := p.Register(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	list, _ := p.List()
	list[0].Environment["PATH"] = "mutated"
	list, _ = p.List()
	if list[0].Environment["PATH"] == "mutated" {
		t.Fatal("shared mutable map")
	}
	t.Setenv("SYNTHETIC_API_SECRET", "do-not-persist")
	w, err := p.Prepare(context.Background(), project.ID, "synthetic-task", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range w.Environment {
		if strings.Contains(v, "SYNTHETIC_API_SECRET") {
			t.Fatal("ambient secret inherited")
		}
	}
	data, _ := os.ReadFile(p.file)
	if strings.Contains(string(data), "do-not-persist") {
		t.Fatal("secret persisted")
	}
	project.Path = w.Path
	if _, err := p.Register(context.Background(), project); !errors.Is(err, ErrInvalid) {
		t.Fatal("worktree registered as main")
	}
}

func TestEnvironmentPinsDirectoryLinks(t *testing.T) {
	root := t.TempDir()
	version := filepath.Join(root, "version-one")
	link := filepath.Join(root, "current")
	if err := os.Mkdir(version, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(version, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	env := pinnedEnvironment(map[string]string{"PATH": link})
	want, err := filepath.EvalSymlinks(version)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range env {
		if strings.HasPrefix(strings.ToUpper(v), "PATH=") && strings.SplitN(v, "=", 2)[1] != want {
			t.Fatalf("mutable path not resolved: %s", v)
		}
	}
}

func TestWorktreeParentAliasCannotCreateOutsideRoot(t *testing.T) {
	p, project := testProjects(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(p.root, ".frost-worktrees")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := p.Prepare(context.Background(), project.ID, "synthetic-task", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("external parent alias accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, project.ID)); !os.IsNotExist(err) {
		t.Fatal("directory created outside root before rejection")
	}
}

func TestEnvironmentOmitsMutableVersionManagerProxies(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "shims")
	rust := filepath.Join(root, ".cargo", "bin")
	fixed := filepath.Join(root, "versions", "one", "bin")
	for _, dir := range []string{shim, rust, fixed} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := pinnedEnvironment(map[string]string{"PATH": strings.Join([]string{shim, rust, fixed}, string(os.PathListSeparator))})
	want, err := filepath.EvalSymlinks(fixed)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range env {
		if strings.HasPrefix(strings.ToUpper(v), "PATH=") && strings.SplitN(v, "=", 2)[1] != want {
			t.Fatalf("mutable version proxy inherited: %s", v)
		}
	}
}

func TestEnvironmentOmitsExecutableFileAliases(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	version := filepath.Join(root, "version-one.exe")
	if err := os.WriteFile(version, []byte("synthetic-not-executed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(version, filepath.Join(bin, "tool.exe")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, entry := range pinnedEnvironment(map[string]string{"PATH": bin}) {
		if strings.HasPrefix(strings.ToUpper(entry), "PATH=") && strings.SplitN(entry, "=", 2)[1] != "" {
			t.Fatal("mutable executable alias remained in task PATH")
		}
	}
}

func TestRegistrySaveFailureBlocksFurtherEffects(t *testing.T) {
	p, project := testProjects(t)
	p.file = filepath.Join(t.TempDir(), "missing", "state.json")
	if err := p.Archive(project.ID, true); err == nil {
		t.Fatal("expected persistence failure")
	}
	if _, err := p.Prepare(context.Background(), project.ID, "synthetic-task", 1); !errors.Is(err, ErrPaused) {
		t.Fatal("failed registry continued")
	}
}

func request(kind, task string, generation uint64, p Params) bridge.Request {
	data, _ := json.Marshal(p)
	return bridge.Request{Identity: bridge.Identity{Version: 1, OperationID: strings.ReplaceAll(kind, ".", "-") + "-synthetic", InstanceID: "synthetic-instance", ActorID: "synthetic-owner", TaskID: task, Generation: generation, Kind: kind}, TimeoutMS: 3000, Parameters: data}
}
