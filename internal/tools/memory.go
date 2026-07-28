package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"FrostAgent/internal/llm"
)

// NewMemoryTool creates a tool that allows the LLM to actively manage memories.
// It reads the current user ID from engine.CurrentUserID (set by RunMessagesWithUser).
func NewMemoryTool(engine *llm.Engine) Tool {
	return Tool{
		name:        "memory",
		description: "管理你的记忆。可以写入新记忆（write）、搜索记忆（search）、列出自己的记忆（list）。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型：write（写入）、search（搜索）、list（列出）",
					"enum":        []string{"write", "search", "list"},
				},
				"content": map[string]any{
					"type":        "string",
					"description": "写入的记忆内容（action=write 时必填）",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "搜索关键词（action=search 时必填）",
				},
				"tags": map[string]any{
					"type":        "array",
					"description": "记忆标签（action=write 时可选）",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"action"},
		},
		execute: func(args string) (string, error) {
			var params struct {
				Action  string   `json:"action"`
				Content string   `json:"content"`
				Query   string   `json:"query"`
				Tags    []string `json:"tags"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			currentUser := engine.CurrentUserID
			if currentUser == "" {
				return "无法获取当前用户信息", nil
			}

			switch params.Action {
			case "write":
				if params.Content == "" {
					return "写入记忆需要提供 content 参数", nil
				}
				if engine.MemoryWriter == nil {
					return "记忆写入功能未启用", nil
				}
				if err := engine.MemoryWriter.Write(currentUser, params.Content, params.Tags); err != nil {
					return fmt.Sprintf("写入失败: %v", err), nil
				}
				return "记忆已写入", nil

			case "search":
				if params.Query == "" {
					return "搜索需要提供 query 参数", nil
				}
				if engine.MemoryReader == nil {
					return "记忆搜索功能未启用", nil
				}
				entries, err := engine.MemoryReader.Recall(context.Background(), params.Query)
				if err != nil {
					return fmt.Sprintf("搜索失败: %v", err), nil
				}
				filtered := engine.MemoryGateway.Filter(entries, currentUser)
				if len(filtered) == 0 {
					return "未找到相关记忆", nil
				}
				result, _ := json.Marshal(filtered)
				return string(result), nil

			case "list":
				if engine.MemoryGateway == nil {
					return "记忆功能未启用", nil
				}
				entries, err := engine.MemoryReader.Recall(context.Background(), "")
				if err != nil {
					return fmt.Sprintf("列出失败: %v", err), nil
				}
				filtered := engine.MemoryGateway.Filter(entries, currentUser)
				if len(filtered) == 0 {
					return "你还没有任何记忆", nil
				}
				result, _ := json.Marshal(filtered)
				return string(result), nil

			default:
				return "未知操作，支持：write、search、list", nil
			}
		},
	}
}