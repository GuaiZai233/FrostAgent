package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"FrostAgent/internal/llm"
)

// NewMemoryTool creates a tool that manages the current request's memory owner.
func NewMemoryTool(engine *llm.Engine) Tool {
	return Tool{
		name:        "memory",
		description: "管理你的记忆。可以写入新记忆（write）、搜索记忆（search）、列出自己的记忆（list），或在后台整理自己的记忆主题（reflect）。警告：请谨慎使用list，除非用户明确要求！",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型：write（写入）、search（搜索）、list（列出）、reflect（后台反思整理）",
					"enum":        []string{"write", "search", "list", "reflect"},
				},
				"content": map[string]any{
					"type":        "string",
					"description": "写入的记忆内容（action=write 时必填）",
				},
				"tags": map[string]any{
					"type":        "array",
					"description": "write 时写入标签，search 时搜索标签（如[\"maimai\",\"游戏版本\"]）。每个标签应是实体、主题、属性、版本名、别名或同义词，去掉\"当前、什么、帮我查\"等无检索价值的语气词，固定名称保持完整不要拆分。1～6个。",
					"items":       map[string]any{"type": "string", "minLength": 1},
					"minItems":    1,
					"maxItems":    6,
				},
			},
			"required": []string{"action"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			var params struct {
				Action  string   `json:"action"`
				Content string   `json:"content"`
				Tags    []string `json:"tags"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			runContext, ok := llm.RunContextFromContext(ctx)
			if !ok || runContext.Owner == "" {
				return "无法获取当前用户信息", nil
			}
			currentUser := runContext.Owner

			switch params.Action {
			case "write":
				if params.Content == "" {
					return "写入记忆需要提供 content 参数", nil
				}
				if engine.MemoryWriter == nil {
					return "记忆写入功能未启用", nil
				}
				if err := engine.MemoryWriter.WriteByOwner(
					currentUser,
					runContext.OwnerType,
					params.Content,
					params.Tags,
				); err != nil {
					return fmt.Sprintf("写入失败: %v", err), nil
				}
				return "记忆已写入", nil

			case "search":
				// Clean and validate tags
				tags := cleanSearchTags(params.Tags)
				if len(tags) == 0 {
					return "搜索需要提供 tags 参数（1～6个标签）", nil
				}
				if len(tags) > 6 {
					return "搜索最多支持 6 个不同的 tags 标签", nil
				}
				if engine.MemoryReader == nil {
					return "记忆搜索功能未启用", nil
				}
				entries, err := engine.MemoryReader.SearchByTags(context.Background(), tags)
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

			case "reflect":
				if engine.MemoryReflections == nil {
					return "记忆反思功能未启用", nil
				}
				status, started, err := engine.MemoryReflections.Start(currentUser)
				if err != nil {
					return fmt.Sprintf("启动记忆反思失败: %v", err), nil
				}
				if !started {
					target := status.Owner
					if target == "" {
						target = "全部用户"
					}
					return fmt.Sprintf("记忆反思任务正在运行（范围：%s）", target), nil
				}
				return "记忆反思任务已在后台启动，完成后会更新主题索引", nil

			default:
				return "未知操作，支持：write、search、list、reflect", nil
			}
		},
	}
}

// cleanSearchTags trims whitespace, lowercases, deduplicates, and filters empty tags.
func cleanSearchTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	result := make([]string, 0, len(tags))
	for _, t := range tags {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if !seen[lower] {
			seen[lower] = true
			result = append(result, lower)
		}
	}
	return result
}
