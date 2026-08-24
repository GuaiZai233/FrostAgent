package llm

import (
	"FrostAgent/internal/memory"
	"context"
)

// BillingRunState tracks request-level billing context and accumulates consumption across tool loop iterations.
type BillingRunState struct {
	Platform         string
	ExternalID       string
	DisplayName      string
	TaskID           string
	WelcomeGranted   bool
	TotalBilledMinor int64
	LastBalanceMinor int64
	BillingActive    bool
	IterationsBilled int
}

// RunContext contains request-local state that tools must not read from the
// shared Engine, otherwise concurrent sessions can cross-send or mix owners.
type RunContext struct {
	SessionID string
	Owner     string
	OwnerType memory.OwnerType
	SendHook  func(toolResultJSON string) error
	Billing   *BillingRunState
}

type runContextKey struct{}

func withRunContext(ctx context.Context, runContext RunContext) context.Context {
	runContext.OwnerType = memory.NormalizeOwnerType(runContext.OwnerType)
	return context.WithValue(ctx, runContextKey{}, runContext)
}

// WithRunContext attaches RunContext to a context.
func WithRunContext(ctx context.Context, runContext RunContext) context.Context {
	return withRunContext(ctx, runContext)
}

// RunContextFromContext returns the request-local tool state, if the current
// agent entry point supplied one.
func RunContextFromContext(ctx context.Context) (RunContext, bool) {
	runContext, ok := ctx.Value(runContextKey{}).(RunContext)
	return runContext, ok
}
