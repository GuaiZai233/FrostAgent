package machine

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type boundedOutput struct {
	mu        sync.Mutex
	value     strings.Builder
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	available := b.limit - b.value.Len()
	if len(data) > available {
		data = data[:available]
		b.truncated = true
	}
	b.value.Write(data)
	return n, nil
}
func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	value := b.value.String()
	if b.truncated {
		value += "\n[output truncated]"
	}
	return value
}
func runShell(ctx context.Context, w Workspace, command string) (string, int, error) {
	cmd := exec.CommandContext(ctx, w.Shell, shellArgs(command)...)
	cmd.Dir = w.Path
	cmd.Env = append([]string(nil), w.Environment...)
	cmd.WaitDelay = 2 * time.Second
	prepareProcess(cmd)
	out := &boundedOutput{limit: 256 << 10}
	cmd.Stdout, cmd.Stderr = out, out
	err := runProcessTree(ctx, cmd)
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return out.String(), code, err
}
