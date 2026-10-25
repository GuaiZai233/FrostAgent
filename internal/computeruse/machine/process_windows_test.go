//go:build windows

package machine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only synthetic test executables run here. No desktop, services, toolchain
// installation, autologon, or user's existing processes are touched.
func TestSyntheticJobHelper(t *testing.T) {
	mode := os.Getenv("FROST_SYNTHETIC_JOB")
	if mode == "" {
		return
	}
	if mode == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestSyntheticJobHelper$")
		child.Env = append(os.Environ(), "FROST_SYNTHETIC_JOB=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		prepareProcess(child)
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	delay := 200 * time.Millisecond
	if os.Getenv("FROST_SYNTHETIC_LONG") == "1" {
		delay = 30 * time.Second
	}
	if value := os.Getenv("FROST_SYNTHETIC_DELAY"); value != "" {
		var err error
		delay, err = time.ParseDuration(value)
		if err != nil {
			os.Exit(6)
		}
	}
	if path := os.Getenv("FROST_SYNTHETIC_CHILD_STARTED"); path != "" {
		if err := os.WriteFile(path, []byte("started"), 0600); err != nil {
			os.Exit(5)
		}
	}
	time.Sleep(delay)
	if output := os.Getenv("FROST_SYNTHETIC_OUTPUT"); output != "" {
		if _, err := fmt.Fprintln(os.Stdout, "late stdout"); err != nil {
			os.Exit(7)
		}
		if _, err := fmt.Fprintln(os.Stderr, "late stderr"); err != nil {
			os.Exit(7)
		}
		if output == "bounded" {
			if _, err := fmt.Fprint(os.Stdout, strings.Repeat("x", 1<<20)); err != nil {
				os.Exit(7)
			}
		}
	}
	if err := os.WriteFile(os.Getenv("FROST_SYNTHETIC_MARKER"), []byte("synthetic"), 0600); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestPrepareWaitsForGitHookDescendantOutput(t *testing.T) {
	p, project := testProjects(t)
	marker := filepath.Join(t.TempDir(), "finished")
	quote := func(path string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "'\\''") + "'" }
	hook := "#!/bin/sh\nexport FROST_SYNTHETIC_JOB=parent\nexport FROST_SYNTHETIC_OUTPUT=normal\nexport FROST_SYNTHETIC_DELAY=4s\n" +
		"export FROST_SYNTHETIC_MARKER=" + quote(marker) + "\nexec " + quote(os.Args[0]) + " '-test.run=^TestSyntheticJobHelper$'\n"
	if err := os.WriteFile(filepath.Join(project.Path, ".git", "hooks", "post-checkout"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	w, err := p.Prepare(ctx, project.ID, "synthetic-output-task", 1)
	if err != nil {
		t.Fatal("worktree creation must drain the complete hook process tree", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("worktree registered before descendant finished writing", err)
	}
	registered, err := p.Workspace(w.TaskID, w.Generation)
	if err != nil || registered.Path != w.Path {
		t.Fatalf("completed worktree was not registered: %+v %v", registered, err)
	}
	reopened, err := OpenProjects(p.root, filepath.Dir(p.file), p.git)
	if err != nil {
		t.Fatal(err)
	}
	if stored, ok := reopened.state.Workspaces[w.TaskID]; !ok || stored.Path != w.Path {
		t.Fatal("completed worktree registration was not persisted")
	}
}

func TestShellDescendantOutputLifetime(t *testing.T) {
	for _, mode := range []string{"completion", "bounded", "cancellation", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			_, project := testProjects(t)
			marker := filepath.Join(t.TempDir(), "finished")
			started := filepath.Join(t.TempDir(), "started")
			delay, output := "4s", "normal"
			if mode == "bounded" {
				delay, output = "10ms", "bounded"
			} else if mode == "cancellation" || mode == "timeout" {
				delay = "30s"
			}
			w := Workspace{Path: project.Path, Shell: project.Shell, Environment: append(pinnedEnvironment(nil),
				"FROST_SYNTHETIC_JOB=child", "FROST_SYNTHETIC_DELAY="+delay, "FROST_SYNTHETIC_OUTPUT="+output,
				"FROST_SYNTHETIC_MARKER="+marker, "FROST_SYNTHETIC_CHILD_STARTED="+started)}
			// Redirecting stdin makes ProcessStartInfo pass explicit inherited
			// stdout/stderr handles rather than falling back to a console. Let
			// PowerShell exit while its child retains the bridge's output pipes.
			command := "$p = [System.Diagnostics.ProcessStartInfo]::new(); $p.FileName = '" + strings.ReplaceAll(os.Args[0], "'", "''") +
				"'; $p.Arguments = '-test.run=^TestSyntheticJobHelper$'; $p.UseShellExecute = $false; $p.CreateNoWindow = $true; $p.RedirectStandardInput = $true; $child = [System.Diagnostics.Process]::Start($p); $child.StandardInput.Close()"
			limit := 15 * time.Second
			if mode == "timeout" {
				limit = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			type result struct {
				output string
				code   int
				err    error
			}
			done := make(chan result, 1)
			go func() {
				output, code, err := runShell(ctx, w, command)
				done <- result{output, code, err}
			}()
			if mode == "cancellation" || mode == "timeout" {
				deadline := time.After(4 * time.Second)
			waiting:
				for {
					if _, err := os.Stat(started); err == nil {
						break waiting
					}
					select {
					case got := <-done:
						t.Fatalf("shell descendant did not start: %+v", got)
					case <-deadline:
						t.Fatal("synthetic shell descendant did not start")
					case <-time.After(10 * time.Millisecond):
					}
				}
				if mode == "cancellation" {
					cancel()
				}
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(16 * time.Second):
				t.Fatal("shell output pipes did not close after process-tree termination")
			}
			_, markerErr := os.Stat(marker)
			if mode == "cancellation" || mode == "timeout" {
				want := context.Canceled
				if mode == "timeout" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(got.err, want) || !os.IsNotExist(markerErr) {
					t.Fatalf("cancelled shell descendant escaped: %+v %v", got, markerErr)
				}
				return
			}
			if got.err != nil || got.code != 0 || markerErr != nil || !strings.Contains(got.output, "late stdout") || !strings.Contains(got.output, "late stderr") {
				t.Fatalf("shell did not drain successful descendant output: code=%d err=%v marker=%v bytes=%d", got.code, got.err, markerErr, len(got.output))
			}
			if mode == "bounded" && (len(got.output) != (256<<10)+len("\n[output truncated]") || !strings.HasSuffix(got.output, "\n[output truncated]")) {
				t.Fatalf("descendant output not bounded: %d bytes", len(got.output))
			}
		})
	}
}

func TestGitHookDescendantsStayTracked(t *testing.T) {
	for _, mode := range []string{"completion", "cancellation", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			p, project := testProjects(t)
			marker := filepath.Join(t.TempDir(), "finished")
			started := filepath.Join(t.TempDir(), "started")
			quote := func(path string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "'\\''") + "'" }
			hook := "#!/bin/sh\nexport FROST_SYNTHETIC_JOB=parent\nexport FROST_SYNTHETIC_MARKER=" + quote(marker) +
				"\nexport FROST_SYNTHETIC_CHILD_STARTED=" + quote(started) + "\nexport FROST_SYNTHETIC_OUTPUT=normal\n"
			if mode != "completion" {
				hook += "export FROST_SYNTHETIC_LONG=1\n"
			}
			hook += "exec " + quote(os.Args[0]) + " '-test.run=^TestSyntheticJobHelper$'\n"
			if err := os.WriteFile(filepath.Join(project.Path, ".git", "hooks", "post-checkout"), []byte(hook), 0700); err != nil {
				t.Fatal(err)
			}
			limit := 8 * time.Second
			if mode == "timeout" {
				limit = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			done := make(chan error, 1)
			var output string
			go func() {
				var err error
				output, err = p.gitCommand(ctx, project.Path, "checkout", "-b", "synthetic-hook")
				done <- err
			}()
			if mode != "completion" {
				deadline := time.After(4 * time.Second)
			waiting:
				for {
					if _, err := os.Stat(started); err == nil {
						break waiting
					}
					select {
					case err := <-done:
						t.Fatalf("hook did not start: %v", err)
					case <-deadline:
						t.Fatal("synthetic hook timed out")
					case <-time.After(10 * time.Millisecond):
					}
				}
				if mode == "cancellation" {
					cancel()
				}
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(9 * time.Second):
				t.Fatal("git output pipes did not close after process-tree termination")
			}
			_, markerErr := os.Stat(marker)
			if mode != "completion" {
				want := context.Canceled
				if mode == "timeout" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) || !os.IsNotExist(markerErr) {
					t.Fatalf("cancelled git descendant escaped: %v %v", err, markerErr)
				}
			} else if err != nil || markerErr != nil {
				t.Fatalf("git completed before its hook descendant: %v %v", err, markerErr)
			} else if !strings.Contains(output, "late stdout") || !strings.Contains(output, "late stderr") {
				t.Fatal("git hook descendant output was not captured")
			}
		})
	}
}
func TestJobTracksDescendantsAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "descendant", true: "cancelled"}[cancelled], func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "done")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if cancelled {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
			}
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSyntheticJobHelper$")
			cmd.Env = append(os.Environ(), "FROST_SYNTHETIC_JOB=parent", "FROST_SYNTHETIC_MARKER="+marker)
			if cancelled {
				cmd.Env = append(cmd.Env, "FROST_SYNTHETIC_LONG=1")
			}
			prepareProcess(cmd)
			err := runProcessTree(ctx, cmd)
			if cancelled {
				if err == nil {
					t.Fatal("cancelled process reported success")
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("cancelled descendant escaped")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatal("reported completion before descendant", err)
				}
			}
		})
	}
}
