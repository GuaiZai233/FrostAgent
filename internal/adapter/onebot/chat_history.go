package onebot

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/model"
)

func historyMessageText(raw json.RawMessage) (string, string) {
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return historyCQText(message)
	}
	var text strings.Builder
	replyTo := ""
	for _, part := range ParseMessageSegments(raw) {
		switch part.Type {
		case "text":
			value, _ := part.Data["text"].(string)
			text.WriteString(value)
		case "reply":
			replyTo = fmt.Sprint(part.Data["id"])
		case "at":
			text.WriteString("[@" + fmt.Sprint(part.Data["qq"]) + "]")
		default:
			text.WriteString("[" + part.Type + "]")
		}
	}
	return text.String(), replyTo
}

func (c *wsConnection) recordIncomingHistory(event model.OneBotEvent) {
	if c.mock || c.engine == nil || c.engine.ChatHistory == nil || event.PostType != "message" {
		return
	}
	if event.UserID <= 0 || (event.MessageType == "group" && event.GroupID <= 0) {
		return
	}
	target := strconv.FormatInt(event.UserID, 10)
	if event.MessageType == "group" {
		target = strconv.FormatInt(event.GroupID, 10)
	}
	text, replyTo := historyMessageText(event.Message)
	record := chathistory.Record{
		Scope:    chathistory.Scope{Platform: "qq", MessageType: event.MessageType, TargetID: target},
		SenderID: strconv.FormatInt(event.UserID, 10), Role: "user", Content: text,
		ReplyTo: replyTo, DeliveryStatus: "received",
	}
	if event.MessageID != 0 {
		record.MessageID = strconv.FormatInt(int64(event.MessageID), 10)
	}
	if event.Time > 0 {
		record.OccurredAt = time.Unix(event.Time, 0).UTC()
	}
	if event.Sender != nil {
		record.SenderName = event.Sender.Nickname
		if event.Sender.Card != "" {
			record.SenderName = event.Sender.Card
		}
	}
	c.engine.ChatHistory.Record(record)
}

func (c *wsConnection) recordHistoryAction(action model.OneBotAction, response oneBotAPIResponse, status string) {
	if c.mock || c.engine == nil || c.engine.ChatHistory == nil {
		return
	}
	switch action.Action {
	case "send_msg", "send_group_msg", "send_private_msg", "send_group_forward_msg", "send_private_forward_msg", "send_forward_msg", "forward_group_single_msg", "forward_friend_single_msg":
	default:
		return
	}
	data, err := json.Marshal(action.Params)
	if err != nil {
		return
	}
	var params struct {
		GroupID    json.RawMessage `json:"group_id"`
		UserID     json.RawMessage `json:"user_id"`
		Message    json.RawMessage `json:"message"`
		Messages   json.RawMessage `json:"messages"`
		MessageID  json.RawMessage `json:"message_id"`
		AutoEscape bool            `json:"auto_escape"`
	}
	if json.Unmarshal(data, &params) != nil {
		return
	}
	kind, target := "private", historyRawID(params.UserID)
	if id := historyRawID(params.GroupID); id != "" && id != "0" {
		kind, target = "group", id
	}
	var text, replyTo, literal string
	if params.AutoEscape && json.Unmarshal(params.Message, &literal) == nil {
		text = literal
	} else {
		text, replyTo = historyMessageText(params.Message)
	}
	if len(params.Messages) > 0 {
		text += historyForwardText(params.Messages)
	} else if id := historyRawID(params.MessageID); id != "" {
		text += "[转发消息 " + id + "]"
	}
	var result struct {
		MessageID json.RawMessage `json:"message_id"`
	}
	_ = json.Unmarshal(response.Data, &result)
	c.engine.ChatHistory.Record(chathistory.Record{
		Scope:     chathistory.Scope{Platform: "qq", MessageType: kind, TargetID: target},
		MessageID: historyRawID(result.MessageID), SenderName: c.engine.Getenv("BOT_NAME"),
		Role: "assistant", Content: text, ReplyTo: replyTo, DeliveryStatus: status,
	})
}

func historyRawID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}
