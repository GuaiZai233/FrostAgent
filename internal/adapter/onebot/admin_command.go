package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/admincmd"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/model"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/security"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// extractOneBotAdminCommand parses a OneBot event to see if it is an administrator command candidate.
// It strictly requires a real @ targeting the bot (matching event.SelfID) in the current message only.
func extractOneBotAdminCommand(event model.OneBotEvent, prefix string, scope *runtimescope.Scope) (cmd admincmd.ParsedCommand, isCandidate bool, err error) {
	selfIDStr := strconv.FormatInt(event.SelfID, 10)
	if selfIDStr == "0" || selfIDStr == "" {
		return cmd, false, nil
	}
	segments := ParseMessageSegments(event.Message)
	if len(segments) == 0 {
		return cmd, false, nil
	}

	hasRealAt := false
	var remainingSegments []content.MessageSegment

	for _, seg := range segments {
		if seg.Type == "at" {
			qqVal := seg.Data["qq"]
			var atQQ string
			switch v := qqVal.(type) {
			case string:
				atQQ = v
			case float64:
				atQQ = strconv.FormatFloat(v, 'f', -1, 64)
			case json.Number:
				atQQ = v.String()
			case int:
				atQQ = strconv.Itoa(v)
			case int64:
				atQQ = strconv.FormatInt(v, 10)
			}
			if atQQ == selfIDStr {
				hasRealAt = true
				continue // Strip the bot's @ component
			}
		}
		remainingSegments = append(remainingSegments, seg)
	}

	if !hasRealAt {
		return cmd, false, nil
	}

	text := extractUserText(remainingSegments, nil, scope)
	text = strings.TrimSpace(text)
	return admincmd.ParseCandidate(text, prefix)
}

func sendOneBotReply(event model.OneBotEvent, conn *wsConnection, text string) {
	if conn == nil || text == "" {
		return
	}
	action := "send_private_msg"
	type1 := "user_id"
	id := strconv.FormatInt(event.UserID, 10)
	if event.MessageType == "group" {
		action = "send_group_msg"
		type1 = "group_id"
		id = strconv.FormatInt(event.GroupID, 10)
	}
	sendDirectReply(action, type1, id, "echo_admin_cmd", event, conn, text)
}

func handleAdminCommand(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine) bool {
	if engine == nil {
		return false
	}
	prefix := engine.Getenv(admincmd.AdminCommandPrefixEnv)
	cmd, isCandidate, parseErr := extractOneBotAdminCommand(event, prefix, engine.Scope)
	if !isCandidate {
		return false
	}

	callerID := strconv.FormatInt(event.UserID, 10)
	if !admincmd.IsAdmin(callerID, engine.Scope) {
		engine.Log().Debug(logs.WEBSOCKET, fmt.Sprintf("OneBot: 非管理员 [%s] 触发指令候选，静默丢弃", callerID))
		return true // Non-admin: silently dropped without hints
	}

	if engine.Security != nil {
		principal, err := security.NewPrincipal("onebot", callerID)
		if err != nil {
			return true // Fail-closed: invalid principal
		}
		if err := engine.Security.CheckAccess(principal); err != nil {
			if errors.Is(err, security.ErrLocked) {
				sendOneBotReply(event, conn, security.RejectGatewayMsg)
			}
			return true // Fail-closed: locked or security error
		}
	}

	if parseErr != nil {
		sendOneBotReply(event, conn, fmt.Sprintf("%v\n\n%s", parseErr, admincmd.FormatUsage(prefix)))
		return true
	}

	owner := ""
	if event.MessageType == "group" {
		owner, _ = memory.OwnerForGroup(event.GroupID)
	} else {
		owner, _ = memory.OwnerForPrivate(callerID)
	}

	sessionID := historyKey(event)
	routeScope := oneBotRouteScope(event)

	if !engine.Go(func() {
		exec := admincmd.NewExecutor(engine)
		cmdCtx := admincmd.CommandContext{
			SessionID:    sessionID,
			Owner:        owner,
			IsGroup:      event.MessageType == "group",
			CallerUserID: callerID,
			RouteScope:   routeScope,
			Reply: func(ctx context.Context, text string, isIntermediate bool) error {
				sendOneBotReply(event, conn, text)
				return nil
			},
		}
		if err := exec.Execute(context.Background(), cmdCtx, cmd); err != nil {
			if !errors.Is(err, security.ErrLocked) {
				sendOneBotReply(event, conn, fmt.Sprintf("执行指令失败：%v", err))
			}
		}
	}) {
		sendOneBotReply(event, conn, "实例未就绪，无法执行指令。")
	}

	return true
}
