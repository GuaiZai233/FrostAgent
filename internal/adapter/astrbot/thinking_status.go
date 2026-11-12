package astrbot

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/thinking"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

func (c *wsConn) startThinkingStatus(event Event, engine *llm.Engine, turn *llm.SessionTurn) func() {
	supported, _ := event.Metadata["thinking_status_supported"].(bool)
	if c == nil || c.mock || event.MessageType != "private" || !supported {
		return func() {}
	}
	return thinking.Start(c.Context(), turn.Context(), thinking.Interval,
		func() bool { return engine.Getenv(thinking.EnvKey) == "true" },
		func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			err := c.writeTurnJSON(Action{
				Type: "action", Action: "thinking_status", Platform: event.Platform,
				SessionID: event.SessionID, MessageType: event.MessageType, UserID: event.UserID,
				IsIntermediate: true, Echo: "hook_" + event.MessageID,
			}, turn)
			if err != nil && ctx.Err() == nil && !turn.Interrupted() {
				engine.Log().Warn(logs.WEBSOCKET, "AstrBot: 无法展示思考状态，本轮停止续发: "+err.Error())
			}
			return err
		})
}
