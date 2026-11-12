package astrbot

import (
	"context"
	"encoding/base64"
	"maps"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/chatflow"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

// NewConversation exposes the existing conversation processor over an in-process
// transport. Keeping the implementation beside its existing tests allows native
// adapters to share the complete behavior without a second conversation loop.
// No AstrBot service, plugin, WebSocket or network bridge is involved.
func NewConversation(engine *llm.Engine, adapterID string, sender chatflow.Sender, dispatcher core.IncomingDispatcher) chatflow.Processor {
	conn := newWSConn(nil, engine.Scope)
	conn.nativeSend, conn.transportID = sender, adapterID
	adapter := NewAdapter(engine)
	adapter.SetIncomingDispatcher(dispatcher)
	return &conversation{adapter: adapter, conn: conn}
}

type conversation struct {
	adapter *Adapter
	conn    *wsConn
}

func (p *conversation) Close() { _ = p.conn.Close() }

func (p *conversation) Submit(input chatflow.Input) <-chan struct{} {
	m := input.Message
	event := Event{Type: "event", EventType: "message", Platform: m.Platform,
		MessageID: m.ID, SessionID: m.SessionID, MessageType: m.MessageType,
		UserID: m.UserID, SenderName: m.SenderName, SenderCard: m.SenderCard,
		GroupID: m.GroupID, GroupName: m.GroupName, Content: m.Content,
		Attachments: m.Attachments, Metadata: maps.Clone(m.Metadata),
		IsWake: input.IsWake, IsAt: input.IsAt, Timestamp: m.CreatedAt.Unix(),
		StickerDeliveryReceipt: true}
	if event.Metadata == nil {
		event.Metadata = make(map[string]any)
	}
	if q := input.Quote; q != nil && q.SessionID == m.SessionID && q.Platform == m.Platform {
		event.Metadata["reply_message_id"] = q.ID
		node := astrBotQuoteNode{Context: map[string]any{"message_id": q.ID, "sender_id": q.UserID,
			"sender_name": q.SenderName, "content": q.Content}}
		for _, att := range q.Attachments {
			if att.Type != core.AttachmentTypeImage {
				continue
			}
			att.MessageID = q.ID
			event.Attachments = append(event.Attachments, att)
			data := map[string]any{"url": att.URL}
			if len(att.Content) > 0 {
				data = map[string]any{"base64": base64.StdEncoding.EncodeToString(att.Content)}
			}
			node.Segments = append(node.Segments, content.MessageSegment{Type: "image", Data: data})
		}
		event.nativeQuotes = []astrBotQuoteNode{node}
	}
	return p.adapter.submitEvent(p.conn, event)
}

func (c *wsConn) adapterID() string {
	if c.transportID != "" {
		return c.transportID
	}
	return "astrbot"
}

func (c *wsConn) deliverNative(request context.Context, action Action, turn *llm.SessionTurn) error {
	target := action.TargetID
	if target == "" {
		target = action.UserID
		if action.MessageType == "group" {
			target = action.GroupID
		}
	}
	delivery := chatflow.Delivery{Action: action.Action,
		Message: core.OutgoingMessage{Platform: action.Platform, MessageType: action.MessageType,
			TargetID: target, Content: action.Content, Attachments: action.Attachments}}
	for _, part := range action.Messages {
		delivery.Parts = append(delivery.Parts, chatflow.Part{Type: part.Type, Text: part.Text,
			MentionUserID: part.MentionUserID, MessageID: part.MessageID, Path: part.Path,
			URL: part.URL, IsSticker: part.IsSticker})
	}
	ctx, cancel := context.WithCancel(request)
	defer cancel()
	stopConnection := context.AfterFunc(c.Context(), cancel)
	defer stopConnection()
	if turn != nil {
		stop := context.AfterFunc(turn.Context(), cancel)
		defer stop()
	}
	if ctx.Err() != nil {
		return context.Canceled
	}
	return c.nativeSend(ctx, delivery)
}
