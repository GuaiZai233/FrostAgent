package subagent

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"context"
	"encoding/json"
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
		logs.Error(logs.SYSTEM, err.Error())
		return "", err
	}
	if content, ok := responseMsg.Message.Content.(string); ok {
		return content, nil
	}
	bytes, _ := json.Marshal(responseMsg.Message.Content)
	return string(bytes), nil
}
