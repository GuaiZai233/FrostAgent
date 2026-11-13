package astrbot

import (
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
)

func (c *wsConn) recordIncomingHistory(event Event) {
	if c.mock || c.engine == nil || c.engine.ChatHistory == nil ||
		(event.Type != "" && event.Type != "event") ||
		(event.EventType != "" && event.EventType != "message") {
		return
	}
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	target := event.UserID
	if event.MessageType == "group" {
		target = event.GroupID
	}
	incoming := ToIncomingMessage(event)
	text := event.Content
	for _, attachment := range event.Attachments {
		if attachment.MessageID != "" && attachment.MessageID != event.MessageID {
			continue
		}
		text += "[" + string(attachment.Type) + "]"
	}
	replyTo, _ := event.Metadata["reply_message_id"].(string)
	name := event.SenderName
	if event.SenderCard != "" {
		name = event.SenderCard
	}
	c.engine.ChatHistory.Record(chathistory.Record{
		Scope:     chathistory.Scope{Platform: platform, MessageType: event.MessageType, TargetID: target},
		MessageID: event.MessageID, SenderID: event.UserID, SenderName: name,
		Role: "user", Content: text, ReplyTo: replyTo,
		OccurredAt: incoming.CreatedAt, DeliveryStatus: "received",
	})
}

func (c *wsConn) recordHistoryAction(action Action, status string) {
	if c.mock || c.engine == nil || c.engine.ChatHistory == nil || action.Action != "send_message" {
		return
	}
	platform := action.Platform
	if platform == "" {
		platform = "astrbot"
	}
	target := action.TargetID
	if target == "" {
		target = action.UserID
		if action.MessageType == "group" {
			target = action.GroupID
		}
	}
	text := action.Content
	if len(action.Messages) > 0 {
		var parts strings.Builder
		for _, part := range action.Messages {
			switch part.Type {
			case "plain", "text":
				parts.WriteString(part.Text)
			case "mention_user":
				parts.WriteString("[@" + part.MentionUserID + "]")
			case "quote", "reply":
			default:
				parts.WriteString("[" + part.Type + "]")
			}
		}
		text = parts.String()
	} else {
		for _, attachment := range action.Attachments {
			text += "[" + string(attachment.Type) + "]"
		}
	}
	c.engine.ChatHistory.Record(chathistory.Record{
		Scope:      chathistory.Scope{Platform: platform, MessageType: action.MessageType, TargetID: target},
		SenderName: c.engine.Getenv("BOT_NAME"), Role: "assistant", Content: text,
		ReplyTo: action.ReplyMessageID, DeliveryStatus: status,
	})
}
