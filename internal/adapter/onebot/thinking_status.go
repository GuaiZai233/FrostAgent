package onebot

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/thinking"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"time"
)

func (c *wsConnection) startThinkingStatus(event model.OneBotEvent, engine *llm.Engine, turn *llm.SessionTurn) func() {
	if c == nil || c.mock || event.MessageType != "private" {
		return func() {}
	}
	return thinking.Start(c.Context(), turn.Context(), thinking.Interval,
		func() bool { return engine.Getenv(thinking.EnvKey) == "true" },
		func(ctx context.Context) error {
			_, err := c.sendActionAndWaitContext(model.OneBotAction{
				Action: "set_input_status",
				Params: map[string]any{"user_id": event.UserID, "event_type": 1},
			}, 2*time.Second, turn, ctx)
			if err != nil && ctx.Err() == nil && !turn.Interrupted() {
				engine.Log().Warn(logs.WEBSOCKET, "OneBot: 无法展示思考状态，本轮停止续发: "+err.Error())
			}
			return err
		})
}
