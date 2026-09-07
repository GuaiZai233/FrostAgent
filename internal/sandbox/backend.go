package sandbox

import (
	"context"
	"time"
)

// Backend defines the neutral boundary for isolated sandbox environments.
// FrostAgent core interacts exclusively with this interface and never executes
// arbitrary model-generated commands on the host machine.
type Backend interface {
	// Exec executes a command inside the isolated sandbox for the given session.
	// Command failure (exit_code != 0) and timeout (timed_out == true) return
	// a valid ExecResult with a nil error; errors represent infrastructure,
	// transport, authentication, or protocol failures.
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)

	// Release terminates and cleans up the sandbox instance associated with the session.
	Release(ctx context.Context, sessionID string) error

	// Health checks whether the sandbox runtime infrastructure is reachable and operational.
	Health(ctx context.Context) error
}

// ExecRequest specifies the execution parameters for a command.
type ExecRequest struct {
	SessionID string
	Command   string
	Cwd       string
	Timeout   time.Duration
}

// ExecResult contains the structured result of a command execution.
type ExecResult struct {
	Stdout          string
	Stderr          string
	ExitCode        *int
	TimedOut        bool
	StdoutTruncated bool
	StderrTruncated bool
	Duration        time.Duration
}
