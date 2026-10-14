package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/sandbox"
)

func TestSandboxBundlePreservesFilesAndAvoidsTransferNameCollision(t *testing.T) {
	files := map[string][]byte{
		"SKILL.md":                    []byte("---\nname: sample\ndescription: Sample\n---\nRun scripts/task.py"),
		"scripts/task.py":             []byte("print('synthetic')"),
		"assets/image.bin":            {0, 1, 255},
		".frostagent-transfer-0":      []byte("legitimate resource"),
		".frostagent-transfer-1/file": []byte("legitimate directory"),
	}
	pkg, err := ParsePackage(files, "", "scripts/task.py")
	if err != nil {
		t.Fatal(err)
	}
	backend := &recordingSandbox{}
	if _, _, err := Run(context.Background(), backend, "synthetic-session", pkg, "python3 scripts/task.py", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 4 {
		t.Fatalf("unexpected request count: %d", len(backend.calls))
	}
	transfer := backend.calls[1].Command
	if !strings.HasSuffix(transfer, "'.frostagent-transfer-2'") {
		t.Fatal("transfer file conflicts with bundled resources")
	}
	parts := strings.Split(transfer, "'")
	if len(parts) < 4 {
		t.Fatal("invalid transfer command")
	}
	data, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != len(files) {
		t.Fatal("missing sandbox resources")
	}
	for _, member := range reader.File {
		executable := member.Name == "scripts/task.py"
		if (member.Mode().Perm()&0111 != 0) != executable || member.Mode()&os.ModeSetuid != 0 {
			t.Fatalf("incorrect executable ZIP permissions: %s %v", member.Name, member.Mode())
		}
		stream, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || !bytes.Equal(data, files[member.Name]) {
			t.Fatalf("resource changed: %s %v", member.Name, err)
		}
	}
}

func TestPackageRejectsFileDirectoryConflict(t *testing.T) {
	_, err := ParsePackage(map[string][]byte{
		"SKILL.md": []byte("---\nname: sample\ndescription: Sample\n---\nInstructions"),
		"scripts":  []byte("file"), "scripts/task.py": []byte("script"),
	}, "")
	if err == nil {
		t.Fatal("file/directory conflict accepted")
	}
}

type functionalSandbox struct {
	execute func(context.Context, sandbox.ExecRequest) (sandbox.ExecResult, error)
}

func (b functionalSandbox) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	return b.execute(ctx, req)
}
func (functionalSandbox) Release(context.Context, string) error { return nil }
func (functionalSandbox) Health(context.Context) error          { return nil }

func sandboxTestPackage(t *testing.T) Package {
	t.Helper()
	pkg, err := ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: sample\ndescription: Sample\n---\nRun script")}, "")
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestSandboxRemainingBudgetPreservesTimeoutResponse(t *testing.T) {
	caller, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	callerDeadline, _ := caller.Deadline()
	previous := 250 * time.Millisecond
	calls := 0
	backend := functionalSandbox{execute: func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
		calls++
		if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(callerDeadline) {
			t.Fatal("execution budget cut off the backend's response envelope")
		}
		if req.Timeout <= 0 || req.Timeout > previous || (calls > 1 && req.Timeout == previous) {
			t.Fatalf("remaining budget did not decrease: %v after %v", req.Timeout, previous)
		}
		previous = req.Timeout
		code := 0
		if calls == 4 {
			timer := time.NewTimer(req.Timeout + 5*time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return sandbox.ExecResult{}, ctx.Err()
			case <-timer.C:
				return sandbox.ExecResult{TimedOut: true, Stdout: "partial output", Stderr: "remote timeout"}, nil
			}
		}
		time.Sleep(time.Millisecond)
		result := sandbox.ExecResult{ExitCode: &code}
		if calls == 1 {
			result.Stdout = "/sandbox/frostagent-skill-Synthetic123\n"
		}
		return result, nil
	}}
	result, _, err := Run(caller, backend, "synthetic-session", sandboxTestPackage(t), "synthetic-command", 250*time.Millisecond)
	if err != nil || !result.TimedOut || result.Stdout != "partial output" || result.Stderr != "remote timeout" || calls != 4 {
		t.Fatalf("lost timeout response: %+v calls=%d err=%v", result, calls, err)
	}
}

func TestSandboxPreparationTimeoutDoesNotRunScript(t *testing.T) {
	t.Run("budget exhausted between requests", func(t *testing.T) {
		calls := 0
		backend := functionalSandbox{execute: func(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			calls++
			time.Sleep(req.Timeout + time.Millisecond)
			code := 0
			return sandbox.ExecResult{ExitCode: &code, Stdout: "/sandbox/frostagent-skill-Synthetic123\n"}, nil
		}}
		result, _, err := Run(context.Background(), backend, "synthetic-session", sandboxTestPackage(t), "synthetic-command", 5*time.Millisecond)
		if err != nil || !result.TimedOut || calls != 1 || !strings.Contains(result.Stderr, "脚本未执行") {
			t.Fatalf("exhausted preparation budget: %+v calls=%d err=%v", result, calls, err)
		}
	})
	t.Run("remote preparation timed out", func(t *testing.T) {
		calls := 0
		backend := functionalSandbox{execute: func(_ context.Context, _ sandbox.ExecRequest) (sandbox.ExecResult, error) {
			calls++
			if calls == 2 {
				return sandbox.ExecResult{TimedOut: true, Stderr: "remote timeout"}, nil
			}
			code := 0
			return sandbox.ExecResult{ExitCode: &code, Stdout: "/sandbox/frostagent-skill-Synthetic123\n"}, nil
		}}
		result, _, err := Run(context.Background(), backend, "synthetic-session", sandboxTestPackage(t), "synthetic-command", time.Second)
		if err != nil || !result.TimedOut || calls != 2 || !strings.Contains(result.Stderr, "remote timeout") || !strings.Contains(result.Stderr, "脚本未执行") {
			t.Fatalf("remote preparation timeout: %+v calls=%d err=%v", result, calls, err)
		}
	})
}

func TestSandboxCallerCancellationStopsExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	backend := functionalSandbox{execute: func(context.Context, sandbox.ExecRequest) (sandbox.ExecResult, error) {
		calls++
		return sandbox.ExecResult{}, nil
	}}
	if _, _, err := Run(ctx, backend, "synthetic-session", sandboxTestPackage(t), "synthetic-command", time.Second); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled caller reached backend: calls=%d err=%v", calls, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	backend.execute = func(context.Context, sandbox.ExecRequest) (sandbox.ExecResult, error) {
		calls++
		cancel()
		code := 0
		return sandbox.ExecResult{ExitCode: &code, Stdout: "/sandbox/frostagent-skill-Synthetic123\n"}, nil
	}
	if _, _, err := Run(ctx, backend, "synthetic-session", sandboxTestPackage(t), "synthetic-command", time.Second); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation during preparation ignored: calls=%d err=%v", calls, err)
	}
}

func TestSandboxDirectExecutableScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct POSIX shebang execution is covered on Linux")
	}
	for _, command := range []string{"sh", "python3"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is unavailable", command)
		}
	}
	packages, err := ParseZIP(archiveFor(t,
		zipEntry{"sample/SKILL.md", "---\nname: sample\ndescription: Sample\n---\nRun ./scripts/task.sh", 0644},
		zipEntry{"sample/scripts/task.sh", "#!/bin/sh\nprintf 'synthetic-script-output\\n'\n", os.ModeSetuid | 0755},
		zipEntry{"sample/references/plain.txt", "plain resource", 0644},
	))
	if err != nil {
		t.Fatal(err)
	}
	// This test backend executes only the authored fixture and generated setup
	// commands in a disposable directory; production uses the isolated backend.
	root := t.TempDir()
	calls := 0
	backend := functionalSandbox{execute: func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
		calls++
		if calls == 1 {
			code := 0
			return sandbox.ExecResult{ExitCode: &code, Stdout: "/sandbox/frostagent-skill-Synthetic123\n"}, nil
		}
		process := exec.CommandContext(ctx, "sh", "-c", req.Command)
		process.Dir = root
		var stdout, stderr bytes.Buffer
		process.Stdout, process.Stderr = &stdout, &stderr
		err := process.Run()
		code := 0
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				return sandbox.ExecResult{}, err
			}
			code = exitErr.ExitCode()
		}
		return sandbox.ExecResult{ExitCode: &code, Stdout: stdout.String(), Stderr: stderr.String()}, nil
	}}
	result, _, err := Run(context.Background(), backend, "synthetic-session", packages[0], "./scripts/task.sh", 5*time.Second)
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || result.Stdout != "synthetic-script-output\n" {
		t.Fatalf("direct shebang command failed: %+v err=%v", result, err)
	}
	script, err := os.Stat(filepath.Join(root, "scripts/task.sh"))
	if err != nil || script.Mode().Perm() != 0755 || script.Mode()&os.ModeSetuid != 0 {
		t.Fatalf("unsafe or missing executable permissions: %v %v", script, err)
	}
	plain, err := os.Stat(filepath.Join(root, "references/plain.txt"))
	if err != nil || plain.Mode().Perm()&0111 != 0 {
		t.Fatalf("plain resource became executable: %v %v", plain, err)
	}
}
