package llm

import "FrostAgent/internal/security"

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

func (e *Engine) securityBlocks(run RunContext, stage security.WatchdogStage, source security.WatchdogSource, content, tool string) bool {
	if e.Security == nil {
		return false
	}
	if e.securityAccess(run) != nil || e.Security.Watchdog == nil {
		return true
	}
	p, err := runPrincipal(run)
	if err != nil {
		return true
	}
	instance := e.InstanceID
	if instance == "" {
		instance = run.InstanceID
	}
	decision := e.Security.Watchdog.Evaluate(p, stage, source, content, security.AuditEvent{
		Instance: instance, Session: run.SessionID, Tool: tool,
	})
	return security.Blocks(decision.Action)
}
