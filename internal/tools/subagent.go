package tools

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/subagent"
	"context"
	"encoding/json"
	"fmt"
)

const SubAgentManagerPrompt = "你是子Agent的调度和管理工具。"

func SubAgentTool(provider core.LLMProvider) Tool {
	return Tool{
		name:        "use_subagent",
		description: "调用子agent。有下面几个子agent可调用：【Coder】用于生成代码。**在调用子agent之前，请使用send_message工具输出反馈，表示已指派子agent工作了。**",
		//json schema
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subagent_name": map[string]any{
					"type":        "string",
					"description": "要调用的子agent名称",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "发送给子代理的具体任务内容或代码片段",
				},
			},
			"required": []string{"subagent_name", "content"},
		},

		//工具执行逻辑
		executeContext: func(ctx context.Context, args string) (string, error) {
			var params struct {
				SubagentName string `json:"subagent_name"`
				Content      string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			if params.SubagentName == "Coder" {
				route := core.RouteContext{}
				if runContext, ok := llm.RunContextFromContext(ctx); ok {
					route.Platform = runContext.RouteScope.Platform
					route.GroupID = runContext.RouteScope.GroupID
				}
				return subagent.CallCoder(ctx, provider, route, params.Content)
			}
			return "", nil
		},
	}
}
