package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"strconv"
	"strings"
)

// extractOneBotAdminCommand parses a OneBot event to see if it is an administrator command candidate.
// Private commands need no mention. Group commands require a real @ targeting
// event.SelfID, except the log lookup command.
func extractOneBotAdminCommand(event model.OneBotEvent, prefix string, scope *runtimescope.Scope) (cmd admincmd.ParsedCommand, isCandidate bool, err error) {
	_ = scope
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

	var b strings.Builder
	for _, seg := range remainingSegments {
		switch seg.Type {
		case "text":
			if text, ok := seg.Data["text"].(string); ok {
				b.WriteString(text)
			}
		case "at":
			qqVal := seg.Data["qq"]
			var atQQ string
			switch v := qqVal.(type) {
			case string:
				atQQ = strings.TrimSpace(v)
			case float64:
				atQQ = strconv.FormatFloat(v, 'f', -1, 64)
			case json.Number:
				atQQ = v.String()
			case int:
				atQQ = strconv.Itoa(v)
			case int64:
				atQQ = strconv.FormatInt(v, 10)
			}
			b.WriteString(" ")
			b.WriteString(atQQ)
			b.WriteString(" ")
		default:
			b.WriteString(" [")
			b.WriteString(seg.Type)
			b.WriteString("] ")
		}
	}
	text := strings.TrimSpace(b.String())
	cmd, isCandidate, err = admincmd.ParseCandidate(text, prefix)
	if event.MessageType != "private" && !hasRealAt && cmd.Type != admincmd.CmdLog {
		return admincmd.ParsedCommand{}, false, nil
	}
	return cmd, isCandidate, err
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

func handleAdminCommand(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine, previousError *previousErrorReply) bool {
	if conn != nil && conn.mock {
		return false
	}
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
				sendOneBotReply(event, conn, engine.Log().ReportError(logs.SYSTEM, security.RejectGatewayMsg, err))
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

	sessionID := conn.historyKey(event)
	routeScope := oneBotRouteScope(event)

	if !engine.Go(func() {
		if cmd.Type == admincmd.CmdLog && len(cmd.Args) == 0 {
			id, active := previousError.resolve(engine.Context(), conn.Context())
			if !active {
				return
			}
			if id == "" {
				sendOneBotReply(event, conn, admincmd.FormatUsage(prefix))
				return
			}
			cmd.Args = []string{id}
		}
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
			ReplyPrivate: func(ctx context.Context, text string) error {
				_, err := conn.SendActionAndWait(model.OneBotAction{
					Action: "send_private_msg",
					Params: map[string]any{
						"user_id": event.UserID,
						"message": []map[string]any{{"type": "text", "data": map[string]any{"text": text}}},
					},
				}, actionACKTimeout(conn.Scope))
				return err
			},
		}
		if err := exec.Execute(context.Background(), cmdCtx, cmd); err != nil {
			if !errors.Is(err, security.ErrLocked) {
				sendOneBotReply(event, conn, engine.Log().ReportError(logs.SYSTEM, "管理员指令执行失败", err, sessionID))
			}
		}
	}) {
		sendOneBotReply(event, conn, engine.Log().ReportError(logs.SYSTEM, "管理员指令执行失败：实例未就绪", nil, sessionID))
	}

	return true
}
