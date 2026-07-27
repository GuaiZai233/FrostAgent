package tools

import (
	"FrostAgent/internal/memory"
	"encoding/json"
	"fmt"
)

// NewMemoryTool creates a tool that allows the LLM to actively manage memories.
// currentUserFunc returns the current user ID (injected from the adapter layer).
func NewMemoryTool(store *memory.Store, gateway *memory.Gateway, currentUserFunc func() string) Tool {
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

			currentUser := currentUserFunc()
			if currentUser == "" {
				return "无法获取当前用户信息", nil
			}

			switch params.Action {
			case "write":
				if params.Content == "" {
					return "写入记忆需要提供 content 参数", nil
				}
				writer := memory.NewWriter(store)
				if err := writer.Write(currentUser, params.Content, params.Tags); err != nil {
					return fmt.Sprintf("写入失败: %v", err), nil
				}
				return "记忆已写入", nil

			case "search":
				if params.Query == "" {
					return "搜索需要提供 query 参数", nil
				}
				entries, err := store.Search(params.Query, 5)
				if err != nil {
					return fmt.Sprintf("搜索失败: %v", err), nil
				}
				filtered := gateway.Filter(entries, currentUser)
				if len(filtered) == 0 {
					return "未找到相关记忆", nil
				}
				result, _ := json.Marshal(filtered)
				return string(result), nil

			case "list":
				entries, err := store.ListByOwner(currentUser)
				if err != nil {
					return fmt.Sprintf("列出失败: %v", err), nil
				}
				if len(entries) == 0 {
					return "你还没有任何记忆", nil
				}
				result, _ := json.Marshal(entries)
				return string(result), nil

			default:
				return "未知操作，支持：write、search、list", nil
			}
		},
	}
}
