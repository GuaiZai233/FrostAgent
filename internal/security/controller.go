package security

import (
	"FrostAgent/internal/core"
	"context"
	"errors"
	"path/filepath"
)

var (
	ErrLocked  = errors.New("principal is globally locked")
	ErrBlocked = errors.New("content blocked by watchdog")
)

const (
	RejectInspectorMsg = "FrostAgent 错误：Request rejected by security inspector: 不合适的内容！"
	RejectGatewayMsg   = "FrostAgent 错误：Request rejected by security gateway: 您已被封禁，请联系管理员。"
)

type Controller struct {
	Access   *AccessStore
	Watchdog *Watchdog
	Audit    *AuditStore
}

func NewController(dataDir string) *Controller {
	access := NewAccessStore(filepath.Join(dataDir, "security_access.json"))
	audit := NewAuditStore(filepath.Join(dataDir, "security_audit.jsonl"), 1000)
	return &Controller{Access: access, Audit: audit, Watchdog: NewWatchdog(access, audit)}
}

func (c *Controller) SetClassifier(classifier Classifier) {
	if c != nil && c.Watchdog != nil {
		c.Watchdog.SetClassifier(classifier)
	}
}

func (c *Controller) SetLLMProvider(provider core.LLMProvider, model string) {
	if c != nil && c.Watchdog != nil {
		c.Watchdog.SetLLMProvider(provider, model)
	}
}

func (c *Controller) SetInstanceProvider(instanceID string, provider core.LLMProvider, model string) {
	if c != nil && c.Watchdog != nil {
		c.Watchdog.SetInstanceProvider(instanceID, provider, model)
	}
}

func (c *Controller) SetInstanceClassifier(instanceID string, classifier Classifier) {
	if c != nil && c.Watchdog != nil {
		c.Watchdog.SetInstanceClassifier(instanceID, classifier)
	}
}

func (c *Controller) RemoveInstanceProvider(instanceID string) {
	if c != nil && c.Watchdog != nil {
		c.Watchdog.RemoveInstanceProvider(instanceID)
	}
}

// ForRuntime creates a scoped runtime Controller sharing global AccessStore and AuditStore,
// with a dedicated Watchdog using the given provider without mutating the parent Controller.
func (c *Controller) ForRuntime(provider core.LLMProvider, model string) *Controller {
	if c == nil {
		return nil
	}
	wd := NewWatchdog(c.Access, c.Audit)
	if provider != nil {
		wd.SetLLMProvider(provider, model)
	}
	return &Controller{
		Access:   c.Access,
		Audit:    c.Audit,
		Watchdog: wd,
	}
}

// ForInstance registers the instance provider on the shared controller and returns a scoped
// runtime Controller sharing global AccessStore and AuditStore.
func (c *Controller) ForInstance(instanceID string, provider core.LLMProvider, model string) *Controller {
	if c == nil {
		return nil
	}
	if instanceID != "" {
		c.SetInstanceProvider(instanceID, provider, model)
	}
	wd := NewWatchdog(c.Access, c.Audit)
	if provider != nil {
		wd.SetLLMProvider(provider, model)
	}
	return &Controller{
		Access:   c.Access,
		Audit:    c.Audit,
		Watchdog: wd,
	}
}

func NewControllerWithProvider(dataDir string, provider core.LLMProvider, model string) *Controller {
	ctrl := NewController(dataDir)
	ctrl.SetLLMProvider(provider, model)
	return ctrl
}

// GateIngress applies the global lock before any stateful ingress processing,
// then evaluates user-controlled content. Store failures are fail-closed.
func (c *Controller) CheckAccess(p Principal) error {
	if c == nil {
		return nil
	}
	if c.Access == nil {
		return errors.New("access control unavailable")
	}
	locked, _, err := c.Access.IsLocked(p)
	if err != nil {
		return err
	}
	if locked {
		return ErrLocked
	}
	return nil
}

func (c *Controller) Lock(p Principal, reason string) error {
	if c == nil || c.Access == nil {
		return errors.New("access control unavailable")
	}
	if err := c.Access.Lock(p, reason); err != nil {
		return err
	}
	if c.Audit != nil {
		_ = c.Audit.Append(AuditEvent{Principal: p, Stage: StageIngress, Source: SourceUserDirect, Action: WatchdogLock, Reason: reason, Hash: ContentHash(reason)})
	}
	return nil
}

func (c *Controller) Unlock(p Principal) error {
	if c == nil || c.Access == nil {
		return errors.New("access control unavailable")
	}
	if err := c.Access.Unlock(p); err != nil {
		return err
	}
	if c.Audit != nil {
		_ = c.Audit.Append(AuditEvent{Principal: p, Stage: StageIngress, Source: SourceUserDirect, Action: WatchdogPass, Reason: "manual unlock", Hash: ContentHash(p.Key())})
	}
	return nil
}

func (c *Controller) GateIngress(p Principal, content string, meta AuditEvent) WatchdogDecision {
	return c.GateIngressWithContext(context.Background(), p, content, meta)
}

func (c *Controller) GateIngressWithContext(ctx context.Context, p Principal, content string, meta AuditEvent) WatchdogDecision {
	if c == nil {
		return WatchdogDecision{Action: WatchdogPass}
	}
	if c.Access == nil || c.Watchdog == nil {
		return WatchdogDecision{Action: WatchdogBlock, Reason: "security control unavailable"}
	}
	locked, record, err := c.Access.IsLocked(p)
	if err != nil {
		return WatchdogDecision{Action: WatchdogBlock, Reason: "access-control state unavailable"}
	}
	if locked {
		meta.Principal = p
		meta.Stage = StageIngress
		meta.Source = SourceUserDirect
		meta.Action = WatchdogBlock
		meta.Reason = record.Reason
		meta.Hash = ContentHash(content)
		_ = c.Audit.Append(meta)
		return WatchdogDecision{Action: WatchdogBlock, Reason: ErrLocked.Error(), Event: meta}
	}
	return c.Watchdog.EvaluateWithContext(ctx, p, StageIngress, SourceUserDirect, content, meta)
}

// Evaluate passes content to the underlying Watchdog with explicit stage and provenance source.
func (c *Controller) Evaluate(p Principal, stage WatchdogStage, source WatchdogSource, content string, meta AuditEvent) WatchdogDecision {
	return c.EvaluateWithContext(context.Background(), p, stage, source, content, meta)
}

func (c *Controller) EvaluateWithContext(ctx context.Context, p Principal, stage WatchdogStage, source WatchdogSource, content string, meta AuditEvent) WatchdogDecision {
	if c == nil || c.Watchdog == nil {
		return WatchdogDecision{Action: WatchdogPass}
	}
	return c.Watchdog.EvaluateWithContext(ctx, p, stage, source, content, meta)
}

// EvaluateContext evaluates indirect context (such as quoted reply context or group running summary)
// at StageIngress with the given non-direct provenance source. If high-risk content is detected,
// it is blocked and audited without striking or locking the requesting principal.
func (c *Controller) EvaluateContext(p Principal, source WatchdogSource, content string, meta AuditEvent) WatchdogDecision {
	return c.Evaluate(p, StageIngress, source, content, meta)
}

func Blocks(action WatchdogAction) bool {
	return action == WatchdogBlock || action == WatchdogStrike || action == WatchdogLock
}

// IsLocked checks whether the principal is currently locked in the access store.
func (c *Controller) IsLocked(p Principal) bool {
	if c == nil || c.Access == nil {
		return false
	}
	locked, _, err := c.Access.IsLocked(p)
	return err == nil && locked
}

// RejectMessage returns the user-facing error message for a blocked security decision.
// If the principal is locked (in AccessStore, via WatchdogLock action, or with ErrLocked reason),
// it returns RejectGatewayMsg. Otherwise, it returns RejectInspectorMsg.
func (c *Controller) RejectMessage(p Principal, decision WatchdogDecision) string {
	if c != nil && c.IsLocked(p) {
		return RejectGatewayMsg
	}
	if decision.Action == WatchdogLock || decision.Reason == ErrLocked.Error() {
		return RejectGatewayMsg
	}
	return RejectInspectorMsg
}
