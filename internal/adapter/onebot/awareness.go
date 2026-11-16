package onebot

import (
	"context"
	"fmt"
	"strconv"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

// Select exactly one real transport. Browser mock sessions are never a route
// for scheduled work, and ambiguous connections must never receive broadcasts.
func (a *Adapter) awarenessConnection() *wsConnection {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var selected *wsConnection
	for c := range a.conns {
		if c.mock || c.Context().Err() != nil {
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
		return fmt.Errorf("主动感知没有唯一可用的 OneBot 连接")
	}
	id, err := strconv.ParseInt(target.ID, 10, 64)
	if err != nil || id <= 0 || proactive.NormalizePlatform(target.Platform) != "qq" {
		return fmt.Errorf("主动感知的 OneBot 目标无效")
	}
	var messages []tools.Msg
	for _, part := range parts {
		messages = append(messages, tools.Msg{Type: part.Type, Text: part.Text, MentionUserID: part.MentionUserID})
	}
	segments, err := tools.BuildOneBotMessage(messages)
	if err != nil {
		return err
	}
	action, key := "send_private_msg", "user_id"
	if target.Kind == "group" {
		action, key = "send_group_msg", "group_id"
	}
	_, err = conn.sendActionAndWaitTurn(model.OneBotAction{Action: action, Params: map[string]any{key: id, "message": segments}}, actionACKTimeout(conn.Scope), turn)
	return err
}
