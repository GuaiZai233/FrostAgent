package llm

import (
	"FrostAgent/internal/memory"
	"context"
)

// RunContext contains request-local state that tools must not read from the
// shared Engine, otherwise concurrent sessions can cross-send or mix owners.
type RunContext struct {
	Owner     string
	OwnerType memory.OwnerType
	SendHook  func(toolResultJSON string)
}

type runContextKey struct{}

func withRunContext(ctx context.Context, runContext RunContext) context.Context {
	runContext.OwnerType = memory.NormalizeOwnerType(runContext.OwnerType)
	return context.WithValue(ctx, runContextKey{}, runContext)
}

// RunContextFromContext returns the request-local tool state, if the current
// agent entry point supplied one.
func RunContextFromContext(ctx context.Context) (RunContext, bool) {
	runContext, ok := ctx.Value(runContextKey{}).(RunContext)
	return runContext, ok
}
