package llm

import (
	"context"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

// GroupContextPrompt renders a refreshed snapshot using the same indirect
// context gates as adapter ingress. Rejected or staged content is never promoted
// to a direct user instruction, and warnings remain request-local.
func (e *Engine) GroupContextPrompt(ctx context.Context, run RunContext, snapshot GroupContextSnapshot) (string, string, error) {
	if !GroupCompactAllowed(e.Getenv, run.SessionID) {
		snapshot = WithoutCompactSummary(snapshot)
	}
	var notice string
	vetted := func(content string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if content == "" || e.Security == nil {
			return content, nil
		}
		principal, err := runPrincipal(run)
		if err != nil {
			return "", err
		}
		meta := security.AuditEvent{Instance: e.InstanceID, Session: run.SessionID}
		var decision security.WatchdogDecision
		if run.Mock {
			decision = e.Security.EvaluateContextDryRun(principal, security.SourceGroupContext, content, meta)
		} else {
			decision = e.Security.EvaluateContextWithContext(ctx, principal, security.SourceGroupContext, content, meta)
		}
		if decision.IsFailure {
			e.Log().Warn(logs.SYSTEM, "挂起后的群聊上下文因安全审查服务异常被隔离剔除: eval_id="+decision.EvaluationID)
			return "", nil
		}
		if security.Blocks(decision.Action) || decision.Action == security.WatchdogFilter {
			return decision.SanitizedContent, nil
		}
		if decision.Action == security.WatchdogWarn && decision.WarningNotice != "" {
			if notice != "" {
				notice += "\n\n"
			}
			notice += decision.WarningNotice
		}
		return content, nil
	}
	var prompt strings.Builder
	if snapshot.CompactDisabled {
		prompt.WriteString("\n\n" + GroupCompactStatusPrompt)
	}
	summary, err := vetted(snapshot.RunningSummary)
	if err != nil {
		return "", "", err
	}
	if summary != "" {
		prompt.WriteString("\n\n<group_running_summary>\n" + summary + "\n</group_running_summary>")
	}
	recent, err := vetted(FormatRecentGroupMessagesContext(snapshot.RecentStructuredMessages))
	if err != nil {
		return "", "", err
	}
	if recent != "" {
		prompt.WriteString("\n\n" + recent)
	}
	return prompt.String(), notice, nil
}

func replaceCurrentPrompt(messages []ChatMessage, content any) []ChatMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			messages[i].Content = content
			break
		}
	}
	return messages
}
