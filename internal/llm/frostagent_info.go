package llm

import (
	"context"
	"errors"

	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

// DialogueModelForRun returns the upstream model name the current request would
// use for the dialogue workload. It resolves against the request's own route
// snapshot and scope, so group-specific bindings are honored. Calls without a
// request context (for example, an out-of-band status query) fall back to the
// global route with an empty scope.
func DialogueModelForRun(ctx context.Context, router *modelrouter.Manager) (string, error) {
	if router == nil {
		return "", errors.New("model router unavailable")
	}
	var snapshot *modelrouter.Snapshot
	var scope modelrouter.Scope
	if run, ok := RunContextFromContext(ctx); ok {
		snapshot = run.RouteSnapshot
		scope = run.RouteScope
	}
	if snapshot == nil {
		snapshot = router.Snapshot()
	}
	target, err := snapshot.Resolve(modelrouter.WorkloadDialogue, scope)
	if err != nil {
		return "", err
	}
	return target.UpstreamModel, nil
}
