package llm

import (
	"errors"
	"fmt"

	"FrostAgent/internal/logs"
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
		errType := security.ErrorType(accessErr)
		safeSummary := security.SafeErrorSummary(accessErr)
		p, pErr := runPrincipal(run)
		if pErr == nil {
			e.Log().Error(logs.SYSTEM, fmt.Sprintf("安全控制存储状态异常 (Fail-Closed): principal=%s error_type=%s reason=%s eval_id=%s", p.Key(), errType, safeSummary, evalID))
		} else {
			e.Log().Error(logs.SYSTEM, fmt.Sprintf("安全控制存储状态异常 (Fail-Closed): error_type=%s reason=%s eval_id=%s", errType, safeSummary, evalID))
		}
		return true, security.WatchdogDecision{
			Action:       security.WatchdogBlock,
			Reason:       fmt.Sprintf("access control unavailable: %s", safeSummary),
			IsFailure:    true,
			EvaluationID: evalID,
			ErrorType:    errType,
			SafeSummary:  safeSummary,
		}
	}
	if e.Security.Watchdog == nil {
		evalID := security.GenerateEvaluationID(stage)
		e.Log().Error(logs.SYSTEM, fmt.Sprintf("安全控制网关未配置 (Fail-Closed): error_type=unconfigured reason=watchdog is nil eval_id=%s", evalID))
		return true, security.WatchdogDecision{
			Action:       security.WatchdogBlock,
			Reason:       "watchdog unconfigured",
			IsFailure:    true,
			EvaluationID: evalID,
			ErrorType:    "unconfigured",
			SafeSummary:  "watchdog is nil",
		}
	}
	p, err := runPrincipal(run)
	if err != nil {
		evalID := security.GenerateEvaluationID(stage)
		errType := security.ErrorType(err)
		safeSummary := security.SafeErrorSummary(err)
		e.Log().Error(logs.SYSTEM, fmt.Sprintf("安全控制状态异常 (Fail-Closed): error_type=%s reason=%s eval_id=%s", errType, safeSummary, evalID))
		return true, security.WatchdogDecision{
			Action:       security.WatchdogBlock,
			Reason:       "invalid principal",
			IsFailure:    true,
			EvaluationID: evalID,
			ErrorType:    errType,
			SafeSummary:  safeSummary,
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
