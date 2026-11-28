package onebot

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

const msgIDCommand = "/msgid"

// recordBotMessageRef links a message the bot just delivered to the logs that
// produced it, so operators can recover the original text from its RefID.
// Failures are never allowed to affect delivery.
func (c *wsConnection) recordBotMessageRef(sessionID string, action model.OneBotAction, response oneBotAPIResponse, viaTool bool) {
	if c == nil || c.mock || c.engine == nil {
		return
	}
	switch action.Action {
	case "send_msg", "send_group_msg", "send_private_msg", "send_group_forward_msg", "send_private_forward_msg", "send_forward_msg", "forward_group_single_msg", "forward_friend_single_msg":
	default:
		return
	}
	var data map[string]any
	if json.Unmarshal(response.Data, &data) != nil {
		return
	}
	messageID, ok := numericMessageID(data["message_id"])
	if !ok || messageID == 0 {
		return
	}
	store := c.engine.Log()
	ref := logs.MessageRef{
		Platform:         "onebot",
		SessionID:        sessionID,
		MessageID:        strconv.FormatInt(messageID, 10),
		Action:           action.Action,
		LLMResponseLogID: store.LatestEntryID(logs.LLM_RESPONSE, sessionID),
	}
	if viaTool {
		ref.ToolLogID = store.LatestEntryID(logs.TOOL, sessionID)
	}
	if params, ok := action.Params.(map[string]any); ok {
		if message, exists := params["message"]; exists {
			ref.Message, _ = json.Marshal(message)
		} else if messages, exists := params["messages"]; exists {
			ref.Message, _ = json.Marshal(messages)
		}
	}
	store.RecordMessageRef(ref)
}

// msgIDCommandReplyID reports whether event is exactly the /msgid command and
// returns the message ID it quotes ("" when it quotes nothing). Mentioning the
// bot is optional; any other component disqualifies the message.
func msgIDCommandReplyID(event model.OneBotEvent) (string, bool) {
	if event.PostType != "message" ||
		(event.MessageType != "group" && event.MessageType != "private") ||
		event.PokeID != "" {
		return "", false
	}
	var text strings.Builder
	replyID := ""
	for _, segment := range ParseMessageSegments(event.Message) {
		switch segment.Type {
		case "text":
			value, ok := segment.Data["text"].(string)
			if !ok {
				return "", false
			}
			text.WriteString(value)
		case "at":
			if event.SelfID <= 0 || fmt.Sprint(segment.Data["qq"]) != strconv.FormatInt(event.SelfID, 10) {
				return "", false
			}
		case "reply":
			if replyID == "" {
				replyID = strings.TrimSpace(fmt.Sprint(segment.Data["id"]))
			}
		default:
			return "", false
		}
	}
	if strings.TrimSpace(text.String()) != msgIDCommand {
		return "", false
	}
	return replyID, true
}

func msgIDReplyText(body string) string {
	return "[ FrostAgent Debug ]\n" + body
}

// handleMsgIDCommand consumes /msgid before session creation, routing or any
// model-backed classification. It only reports a RefID for a message the bot
// delivered; the logs behind it stay reachable only through the backend.
func handleMsgIDCommand(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine) bool {
	replyID, ok := msgIDCommandReplyID(event)
	if !ok {
		return false
	}
	if conn == nil || conn.mock || engine == nil {
		return true
	}
	done, entered := engine.Enter()
	if !entered {
		return true
	}
	defer done()
	if engine.Context().Err() != nil || conn.Context().Err() != nil {
		return true
	}
	engine.RecordIncomingMessage()
	if event.UserID <= 0 || event.UserID == event.SelfID ||
		(event.MessageType == "group" && event.GroupID <= 0) {
		return true
	}
	principal, err := security.NewPrincipal("onebot", strconv.FormatInt(event.UserID, 10))
	if err != nil || engine.Security.CheckAccess(principal) != nil {
		return true
	}
	body := "请回复（引用）Bot 发出的消息后再发送 /msgid"
	if replyID != "" {
		if refID, found := engine.Log().FindMessageRefByMessageID(conn.historyKey(event), replyID); found {
			body = "当前消息 RefID: " + refID
		} else {
			body = "未找到该消息的 RefID：可能不是 Bot 发出的消息、发送于该功能上线之前，或日志已被清理"
		}
	}
	actionName, idKey, id := "send_private_msg", "user_id", event.UserID
	if event.MessageType == "group" {
		actionName, idKey, id = "send_group_msg", "group_id", event.GroupID
	}
	// Write only to the originating transport, without reply decorations.
	action := model.OneBotAction{
		Action: actionName,
		Params: map[string]any{
			idKey:     id,
			"message": []tools.OneBotSegment{{Type: "text", Data: map[string]any{"text": msgIDReplyText(body)}}},
		},
		Echo: fmt.Sprintf("msgid_%d", event.MessageID),
	}
	data, err := json.Marshal(action)
	if err == nil && engine.Context().Err() == nil {
		err = conn.WriteMessage(websocket.TextMessage, data)
		if err == nil {
			conn.recordHistoryAction(action, oneBotAPIResponse{}, "sent")
		}
	}
	if err != nil {
		engine.Log().Warn(logs.WEBSOCKET, fmt.Sprintf("OneBot /msgid 发送失败: %v", err))
	}
	return true
}
