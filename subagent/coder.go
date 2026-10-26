package subagent

import (
	"context"
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

const CoderPrompt = "你是编程助手。"

func CallCoder(ctx context.Context, provider core.LLMProvider, route core.RouteContext, contentBlocks string) (string, error) {
	responseMsg, err := provider.Chat(ctx, core.ChatRequest{
		Messages: []core.ChatMessage{
			{Role: core.RoleSystem, Content: CoderPrompt},
			{Role: core.RoleUser, Content: contentBlocks},
		},
		Route: route,
	})
	if err != nil {
		runtimescope.FromContext(ctx).Log().Error(logs.SYSTEM, err.Error())
		return "", err
	}
	if content, ok := responseMsg.Message.Content.(string); ok {
		return content, nil
	}
	bytes, _ := json.Marshal(responseMsg.Message.Content)
	return string(bytes), nil
}
