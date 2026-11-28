package onebot

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

const (
	tempChatCommand = "/tempchat"
	tempChatUsage   = "用法：/tempchat [-np] [-nm] <内容>\n-np：忽略人格提示词（系统提示词、示例对话、记忆索引）\n-nm：忽略记忆与历史对话\n临时对话不会写入对话历史、记忆与聊天档案。"
)

// tempChatRequest is a parsed "/tempchat [-np] [-nm] <prompt>" message.
type tempChatRequest struct {
	options llm.TempChat
	// event is the inbound event with the command and its flags removed, so the
	// rest of the pipeline sees only what the user wants to ask.
	event model.OneBotEvent
	// empty reports that nothing follows the command, only an optional @ or reply.
	empty bool
}

// parseTempChatCommand recognises /tempchat at the start of a message. Only a
// reply, an @ of the bot and blank text may precede it, so a message that merely
// mentions the word elsewhere is left alone. Flags must directly follow the
// command and may appear in any order.
func parseTempChatCommand(event model.OneBotEvent) (*tempChatRequest, bool) {
	if event.PostType != "message" ||
		(event.MessageType != "group" && event.MessageType != "private") ||
		event.PokeID != "" || (len(event.Messages) > 0 && string(event.Messages) != "null") {
		return nil, false
	}
	segments := ParseMessageSegments(event.Message)
	var (
		prefix []content.MessageSegment
		found  = -1
		rest   string
	)
	for i, segment := range segments {
		switch segment.Type {
		case "reply":
			prefix = append(prefix, segment)
			continue
		case "at":
			if event.SelfID <= 0 || fmt.Sprint(segment.Data["qq"]) != strconv.FormatInt(event.SelfID, 10) {
				return nil, false
			}
			prefix = append(prefix, segment)
			continue
		case "text":
			text, ok := segment.Data["text"].(string)
			if !ok {
				return nil, false
			}
			trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
			if trimmed == "" {
				continue
			}
			after, ok := strings.CutPrefix(trimmed, tempChatCommand)
			if !ok || (after != "" && !unicode.IsSpace([]rune(after)[0])) {
				return nil, false
			}
			found, rest = i, after
		default:
			return nil, false
		}
		break
	}
	if found < 0 {
		return nil, false
	}

	request := &tempChatRequest{}
	for {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		token := rest
		if end := strings.IndexFunc(rest, unicode.IsSpace); end >= 0 {
			token = rest[:end]
		}
		switch strings.ToLower(token) {
		case "-np":
			request.options.NoPersonality = true
		case "-nm":
			request.options.NoMemory = true
		default:
			token = ""
		}
		if token == "" {
			break
		}
		rest = rest[len(token):]
	}

	rebuilt := append([]content.MessageSegment(nil), prefix...)
	if rest != "" {
		rebuilt = append(rebuilt, content.MessageSegment{Type: "text", Data: map[string]any{"text": rest}})
	}
	rebuilt = append(rebuilt, segments[found+1:]...)
	request.empty = rest == "" && found+1 >= len(segments)

	message, err := json.Marshal(rebuilt)
	if err != nil {
		return nil, false
	}
	request.event = event
	request.event.Message = message
	return request, true
}

// replyTempChatUsage tells the sender how to use /tempchat. Like /msgid it is
// written only to the originating transport, without reply decorations, and
// never reaches the model or any history.
func replyTempChatUsage(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine) {
	if conn == nil || conn.mock || engine == nil || event.UserID <= 0 || event.UserID == event.SelfID ||
		(event.MessageType == "group" && event.GroupID <= 0) {
		return
	}
	principal, err := security.NewPrincipal("onebot", strconv.FormatInt(event.UserID, 10))
	if err != nil || engine.Security.CheckAccess(principal) != nil {
		return
	}
	actionName, idKey, id := "send_private_msg", "user_id", event.UserID
	if event.MessageType == "group" {
		actionName, idKey, id = "send_group_msg", "group_id", event.GroupID
	}
	action := model.OneBotAction{
		Action: actionName,
		Params: map[string]any{
			idKey:     id,
			"message": []tools.OneBotSegment{{Type: "text", Data: map[string]any{"text": tempChatUsage}}},
		},
		Echo: fmt.Sprintf("tempchat_usage_%d", event.MessageID),
	}
	data, err := json.Marshal(action)
	if err == nil && engine.Context().Err() == nil {
		err = conn.WriteMessage(websocket.TextMessage, data)
	}
	if err != nil {
		engine.Log().Warn(logs.WEBSOCKET, fmt.Sprintf("OneBot /tempchat 用法提示发送失败: %v", err))
	}
}

// tempChatQuoteGuidance returns the sentence telling the model that the message
// the user replied to came from a temporary conversation, or "" when it did not.
// Only the directly quoted message counts; older ancestors stay plain context.
func (c *wsConnection) tempChatQuoteGuidance(event model.OneBotEvent, quoted resolvedReplyContext) string {
	if c == nil || c.mock || c.engine == nil || quoted.MessageID == "" || quoted.SessionID != c.historyKey(event) {
		return ""
	}
	mark, ok := c.engine.Log().FindTempChatMark(quoted.SessionID, quoted.MessageID)
	if !ok {
		return ""
	}
	return llm.TempChatQuotedGuidance(mark)
}

// tempChatOrigin converts run options into the metadata carried by every action
// the run sends. It is nil for ordinary runs.
func tempChatOrigin(t *llm.TempChat) *model.TempChatOrigin {
	if t == nil {
		return nil
	}
	return &model.TempChatOrigin{NoPersonality: t.NoPersonality, NoMemory: t.NoMemory}
}
