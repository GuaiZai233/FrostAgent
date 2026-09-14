package llm

import (
	"errors"
	"fmt"

	"FrostAgent/internal/security"
)

func (e *Engine) securityAccess(run RunContext) error {
	if e.Security == nil {
		return nil
	}
	p, err := runPrincipal(run)
	if err != nil {
		return err
	}
	return e.Security.CheckAccess(p)
}

func runPrincipal(run RunContext) (security.Principal, error) {
	platform := run.ActorPlatform
	if platform == "" {
		platform = run.RouteScope.Platform
	}
	return security.NewPrincipal(platform, run.ActorUserID)
}

func (e *Engine) securityEvaluate(run RunContext, stage security.WatchdogStage, source security.WatchdogSource, content, tool string) (bool, security.WatchdogDecision) {
	if e.Security == nil {
		return false, security.WatchdogDecision{}
	}
	if accessErr := e.securityAccess(run); accessErr != nil {
		evalID := security.GenerateEvaluationID(stage)
		if errors.Is(accessErr, security.ErrLocked) {
			return true, security.WatchdogDecision{
				Action:       security.WatchdogBlock,
				Reason:       "access denied / locked",
				EvaluationID: evalID,
			}
		}
		return true, security.WatchdogDecision{
			Action:       security.WatchdogBlock,
			Reason:       fmt.Sprintf("access control unavailable: %s", security.SafeErrorSummary(accessErr)),
			IsFailure:    true,
			EvaluationID: evalID,
			ErrorType:    security.ErrorType(accessErr),
			SafeSummary:  security.SafeErrorSummary(accessErr),
		}
	}
	if e.Security.Watchdog == nil {
		return true, security.WatchdogDecision{
			Action:       security.WatchdogBlock,
			Reason:       "watchdog unconfigured",
			IsFailure:    true,
			EvaluationID: security.GenerateEvaluationID(stage),
			ErrorType:    "unconfigured",
			SafeSummary:  "watchdog is nil",
		}
	}
	p, err := runPrincipal(run)
	if err != nil {
		return true, security.WatchdogDecision{
			Action:       security.WatchdogBlock,
			Reason:       "invalid principal",
			IsFailure:    true,
			EvaluationID: security.GenerateEvaluationID(stage),
			ErrorType:    security.ErrorType(err),
			SafeSummary:  security.SafeErrorSummary(err),
		}
	}
	instance := e.InstanceID
	if instance == "" {
		instance = run.InstanceID
	}
	decision := e.Security.Watchdog.EvaluateWithContext(e.Context(), p, stage, source, content, security.AuditEvent{
		Instance: instance, Session: run.SessionID, Tool: tool,
	})
	return security.Blocks(decision.Action), decision
}

func (e *Engine) securityBlocks(run RunContext, stage security.WatchdogStage, source security.WatchdogSource, content, tool string) bool {
	blocked, _ := e.securityEvaluate(run, stage, source, content, tool)
	return blocked
}
