package astrbot

import (
	"FrostAgent/internal/admincmd"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"context"
	"errors"
	"fmt"
	"strings"
)

// extractAstrBotAdminCommand parses an AstrBot event to see if it is an administrator command candidate.
// It strictly requires a real @ targeting the bot (event.IsAt == true).
func extractAstrBotAdminCommand(event Event, prefix string) (cmd admincmd.ParsedCommand, isCandidate bool, err error) {
	if !event.IsAt {
		return cmd, false, nil
	}
	text := admincmd.StripLeadingMention(event.Content)
	return admincmd.ParseCandidate(text, prefix)
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
	action := Action{
		Type:           "action",
		Action:         "send_message",
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
				Type:      "action",
				Action:    "noop",
				SessionID: event.SessionID,
				Echo:      "reply_" + event.MessageID,
			})
		}
		return true // Non-admin: silently dropped without hints
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

	sessionID := sessionKey(event)
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
			_ = sendAstrBotAdminReply(event, conn, fmt.Sprintf("执行指令失败：%v", err), false)
		}
	}) {
		_ = sendAstrBotAdminReply(event, conn, "实例未就绪，无法执行指令。", false)
	}

	return true
}
