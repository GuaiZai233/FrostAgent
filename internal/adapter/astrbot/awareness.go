package astrbot

import (
	"context"
	"fmt"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
)

func (a *Adapter) awarenessConnection() *wsConn {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var selected *wsConn
	for c := range a.conns {
		if c.mock || c.isClosed() || c.Context().Err() != nil {
			continue
		}
		if selected != nil {
			return nil
		}
		selected = c
	}
	return selected
}

func (a *Adapter) AwarenessReady() bool { return a.awarenessConnection() != nil }

func (a *Adapter) SendAwareness(ctx context.Context, target llm.AwarenessTarget, parts []proactive.AwarenessMessage, turn *llm.SessionTurn) error {
	conn := a.awarenessConnection()
	if conn == nil || ctx.Err() != nil {
		return fmt.Errorf("主动感知没有唯一可用的 AstrBot 连接")
	}
	// Durable QQ identities and whitelist aliases are canonical. AstrBot's QQ
	// transport uses aiocqhttp even before an inbound event establishes a route.
	platform := target.Platform
	if memory.IsQQPlatform(platform) {
		platform = astrBotQQPlatform
	}
	action := Action{Type: "action", Action: "send_message", Platform: platform, MessageType: target.Kind,
		SessionID: target.SessionID, TargetID: target.ID, suppressAutoMention: true, suppressAutoReply: true}
	if target.Kind == "group" {
		action.GroupID = target.ID
	} else {
		action.UserID = target.ID
	}
	for _, part := range parts {
		action.Messages = append(action.Messages, ActionMessage{Type: part.Type, Text: part.Text, MentionUserID: part.MentionUserID})
	}
	return conn.writeTurnJSON(action, turn)
}
