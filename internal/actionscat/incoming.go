package actionscat

import (
	"FrostAgent/internal/core"
	"context"
	"strings"
	"time"
)

// MessageEvent is the platform-neutral ActionsCat dispatch payload.
type MessageEvent struct {
	Platform  string `json:"platform"`
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	GroupID   string `json:"group_id,omitempty"`
	Text      string `json:"text"`
}

// DispatchIncoming makes a single bounded attempt; it never retries an uncertain result.
func (c *Client) DispatchIncoming(ctx context.Context, message core.IncomingMessage) error {
	if !c.IsEnabled() || !c.IsConfigured() || strings.TrimSpace(message.Content) == "" ||
		(message.MessageType != "private" && message.MessageType != "group") {
		return nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.Dispatch(requestCtx, MessageEvent{
		Platform: message.Platform, SessionID: message.SessionID,
		UserID: message.UserID, GroupID: message.GroupID, Text: message.Content,
	})
}
