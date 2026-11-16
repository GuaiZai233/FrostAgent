package tools

import (
	"context"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

// reportToolFailure records a known failure, never inferring errors from tool
// output. Successful output and task stdout/stderr retain their own contracts.
func reportToolFailure(ctx context.Context, summary string, err error) string {
	run, _ := llm.RunContextFromContext(ctx)
	return runtimescope.FromContext(ctx).Log().ReportError(logs.TOOL, summary, err, run.SessionID)
}
