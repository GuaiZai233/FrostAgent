package astrbot

import (
	"FrostAgent/internal/admincmd"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/security"
	"context"
	"errors"
	"fmt"
	"strings"
)

func extractMentionedUserIDs(metadata map[string]any) []string {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata["mentioned_user_ids"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		res := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				res = append(res, strings.TrimSpace(s))
			}
		}
		return res
	}
	return nil
}

// extractAstrBotAdminCommand parses an AstrBot event to see if it is an administrator command candidate.
// It strictly requires a real @ targeting the bot (event.IsAt == true).
func extractAstrBotAdminCommand(event Event, prefix string) (cmd admincmd.ParsedCommand, isCandidate bool, err error) {
	if !event.IsAt {
		return cmd, false, nil
	}
	text := admincmd.StripLeadingMention(event.Content)
	cmd, isCandidate, err = admincmd.ParseCandidate(text, prefix)
	if !isCandidate {
		return cmd, false, nil
	}

	if cmd.Type == admincmd.CmdBan || cmd.Type == admincmd.CmdUnban {
		mentionedUserIDs := extractMentionedUserIDs(event.Metadata)
		if len(mentionedUserIDs) > 1 {
			return cmd, true, fmt.Errorf("%s 指令格式错误，不能同时指定多个提及目标", cmd.Type)
		}
		if len(mentionedUserIDs) == 1 {
			target := mentionedUserIDs[0]
			if strings.EqualFold(target, "all") || target == "全体成员" || target == "0" {
				return cmd, true, fmt.Errorf("%s 指令不支持对全体成员执行操作", cmd.Type)
			}
			if len(cmd.Args) == 0 {
				cmd.Args = []string{target}
				cmd.RawArgs = target
				err = nil
			} else if len(cmd.Args) == 1 {
				arg := strings.TrimSpace(cmd.Args[0])
				if strings.HasPrefix(arg, "@") || strings.HasPrefix(arg, "[") {
					cmd.Args[0] = target
					cmd.RawArgs = target
					err = nil
				} else if arg == target {
					cmd.Args[0] = target
					cmd.RawArgs = target
					err = nil
				} else {
					return cmd, true, fmt.Errorf("%s 指令目标冲突：文本参数 %q 与提及目标不一致", cmd.Type, arg)
				}
			} else {
				return cmd, true, fmt.Errorf("%s 指令格式错误，不能同时指定多个目标或参数", cmd.Type)
			}
		}
	}
	return cmd, isCandidate, err
}

func sendAstrBotAdminReply(event Event, conn *wsConn, text string, isIntermediate bool) error {
	if conn == nil {
		return errors.New("connection is nil")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("message content is empty")
	}
	targetID := event.UserID
	if event.MessageType == "group" {
		targetID = event.GroupID
	}
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	action := Action{
		Type:           "action",
		Action:         "send_message",
		Platform:       platform,
		SessionID:      sessionKey(event),
		TargetID:       targetID,
		MessageType:    event.MessageType,
		GroupID:        event.GroupID,
		UserID:         event.UserID,
		Content:        text,
		IsIntermediate: isIntermediate,
		Echo:           fmt.Sprintf("reply_%s", event.MessageID),
		ReplyMessageID: event.MessageID,
	}
	return conn.WriteJSON(action)
}

func handleAdminCommand(conn *wsConn, event Event, engine *llm.Engine) bool {
	if conn != nil && conn.mock {
		return false
	}
	if engine == nil {
		return false
	}
	prefix := engine.Getenv(admincmd.AdminCommandPrefixEnv)
	cmd, isCandidate, parseErr := extractAstrBotAdminCommand(event, prefix)
	if !isCandidate {
		return false
	}

	callerID := strings.TrimSpace(event.UserID)
	if !admincmd.IsAdmin(callerID, engine.Scope) {
		engine.Log().Debug(logs.WEBSOCKET, fmt.Sprintf("AstrBot: 非管理员 [%s] 触发指令候选，静默丢弃", callerID))
		if conn != nil {
			_ = conn.WriteJSON(Action{
				Type:        "action",
				Action:      "noop",
				SubType:     "admin_silent_drop",
				SuppressLLM: true,
				SessionID:   event.SessionID,
				Echo:        "reply_" + event.MessageID,
			})
		}
		return true // Non-admin: silently dropped without hints
	}

	if engine.Security != nil {
		platform := event.Platform
		if platform == "" {
			platform = "astrbot"
		}
		principal, err := security.NewPrincipal(platform, callerID)
		if err != nil {
			return true // Fail-closed: invalid principal
		}
		if err := engine.Security.CheckAccess(principal); err != nil {
			if errors.Is(err, security.ErrLocked) {
				_ = sendAstrBotAdminReply(event, conn, security.RejectGatewayMsg, false)
			}
			return true // Fail-closed: locked or security error
		}
	}

	if parseErr != nil {
		_ = sendAstrBotAdminReply(event, conn, fmt.Sprintf("%v\n\n%s", parseErr, admincmd.FormatUsage(prefix)), false)
		return true
	}

	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	owner := ""
	if event.MessageType == "group" {
		owner, _ = memory.OwnerForPlatformGroup(platform, event.GroupID)
	} else {
		owner, _ = memory.OwnerForPlatformPrivate(platform, callerID)
	}

	sessionID := conn.sessionKey(event)
	routeScope := astrBotRouteScope(event)

	if !engine.Go(func() {
		exec := admincmd.NewExecutor(engine)
		cmdCtx := admincmd.CommandContext{
			SessionID:    sessionID,
			Owner:        owner,
			IsGroup:      event.MessageType == "group",
			CallerUserID: callerID,
			RouteScope:   routeScope,
			Reply: func(ctx context.Context, replyText string, isIntermediate bool) error {
				return sendAstrBotAdminReply(event, conn, replyText, isIntermediate)
			},
		}
		if err := exec.Execute(context.Background(), cmdCtx, cmd); err != nil {
			if !errors.Is(err, security.ErrLocked) {
				_ = sendAstrBotAdminReply(event, conn, fmt.Sprintf("执行指令失败：%v", err), false)
			}
		}
	}) {
		_ = sendAstrBotAdminReply(event, conn, "实例未就绪，无法执行指令。", false)
	}

	return true
}
