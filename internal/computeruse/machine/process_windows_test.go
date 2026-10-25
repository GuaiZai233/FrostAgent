//go:build windows

package machine

import (
	"context"
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
		if path := os.Getenv("FROST_SYNTHETIC_STARTED"); path != "" {
			if err := os.WriteFile(path, []byte("started"), 0600); err != nil {
				os.Exit(5)
			}
		}
		child := exec.Command(os.Args[0], "-test.run=^TestSyntheticJobHelper$")
		child.Env = append(os.Environ(), "FROST_SYNTHETIC_JOB=child")
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
	time.Sleep(delay)
	if err := os.WriteFile(os.Getenv("FROST_SYNTHETIC_MARKER"), []byte("synthetic"), 0600); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestGitHookDescendantsStayTracked(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "completion", true: "cancellation"}[cancelled], func(t *testing.T) {
			p, project := testProjects(t)
			marker := filepath.Join(t.TempDir(), "finished")
			started := filepath.Join(t.TempDir(), "started")
			quote := func(path string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "'\\''") + "'" }
			hook := "#!/bin/sh\nexport FROST_SYNTHETIC_JOB=parent\nexport FROST_SYNTHETIC_MARKER=" + quote(marker) +
				"\nexport FROST_SYNTHETIC_STARTED=" + quote(started) + "\n"
			if cancelled {
				hook += "export FROST_SYNTHETIC_LONG=1\n"
			}
			hook += "exec " + quote(os.Args[0]) + " '-test.run=^TestSyntheticJobHelper$'\n"
			if err := os.WriteFile(filepath.Join(project.Path, ".git", "hooks", "post-checkout"), []byte(hook), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := p.gitCommand(ctx, project.Path, "checkout", "-b", "synthetic-hook"); done <- err }()
			if cancelled {
				deadline := time.After(5 * time.Second)
			waiting:
				for {
					if _, err := os.Stat(started); err == nil {
						break waiting
					}
					select {
					case err := <-done:
						t.Fatalf("hook did not start: %v", err)
					case <-deadline:
						cancel()
						<-done
						t.Fatal("synthetic hook timed out")
					case <-time.After(10 * time.Millisecond):
					}
				}
				cancel()
			}
			err := <-done
			_, markerErr := os.Stat(marker)
			if cancelled {
				if err == nil || !os.IsNotExist(markerErr) {
					t.Fatalf("cancelled git descendant escaped: %v %v", err, markerErr)
				}
			} else if err != nil || markerErr != nil {
				t.Fatalf("git completed before its hook descendant: %v %v", err, markerErr)
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
