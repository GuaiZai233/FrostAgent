package onebot

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/runtimeinfo"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func isRuntimeStatusCommand(event model.OneBotEvent) bool {
	if event.PostType != "message" || event.MessageType != "group" || event.PokeID != "" {
		return false
	}
	var text strings.Builder
	for _, segment := range ParseMessageSegments(event.Message) {
		switch segment.Type {
		case "text":
			value, ok := segment.Data["text"].(string)
			if !ok {
				return false
			}
			text.WriteString(value)
		case "at":
			if event.SelfID <= 0 || fmt.Sprint(segment.Data["qq"]) != strconv.FormatInt(event.SelfID, 10) {
				return false
			}
		case "reply":
			// Only current top-level text counts; never resolve the quoted message.
		default:
			return false
		}
	}
	return strings.TrimSpace(text.String()) == runtimeinfo.Command
}

// handleRuntimeStatusCommand consumes reserved commands before session creation,
// group routing, admin-prefix parsing or any model-backed security classification.
func handleRuntimeStatusCommand(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine) bool {
	if !isRuntimeStatusCommand(event) {
		return false
	}
	if conn == nil || conn.mock || engine == nil {
		return true
	}
	done, ok := engine.Enter()
	if !ok {
		return true
	}
	defer done()
	if engine.Context().Err() != nil || conn.Context().Err() != nil {
		return true
	}
	engine.RecordIncomingMessage()
	if !runtimeinfo.Enabled(engine.Getenv(runtimeinfo.EnabledEnv)) || event.GroupID <= 0 ||
		event.UserID <= 0 || event.UserID == event.SelfID {
		return true
	}
	principal, err := security.NewPrincipal("onebot", strconv.FormatInt(event.UserID, 10))
	if err != nil || engine.Security.CheckAccess(principal) != nil {
		return true
	}
	// Write only to the originating transport, without group reply decorations.
	action := model.OneBotAction{
		Action: "send_group_msg",
		Params: map[string]any{
			"group_id": event.GroupID,
			"message": []tools.OneBotSegment{{Type: "text", Data: map[string]any{
				"text": runtimeinfo.Format(engine.Version, engine.StartedAt, time.Now()),
			}}},
		},
		Echo: fmt.Sprintf("runtime_status_%d", event.MessageID),
	}
	data, err := json.Marshal(action)
	if err == nil && engine.Context().Err() == nil {
		err = conn.WriteMessage(websocket.TextMessage, data)
	}
	if err != nil {
		engine.Log().Warn(logs.WEBSOCKET, fmt.Sprintf("OneBot 运行信息发送失败: %v", err))
	}
	return true
}
