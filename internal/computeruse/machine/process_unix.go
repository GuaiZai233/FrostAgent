//go:build !windows

package machine

import (
	"context"
	"os/exec"
)

func shellArgs(command string) []string { return []string{"-c", command} }
func prepareProcess(cmd *exec.Cmd)      {}

// The production bridge is Windows-only. This helper supports portable
// registry tests, not a promise of Unix desktop/process containment.
func runProcessTree(_ context.Context, cmd *exec.Cmd) error { return cmd.Run() }
