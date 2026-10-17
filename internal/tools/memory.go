package tools

import (
	"FrostAgent/internal/core"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
)

// NewMemoryTool creates a tool that manages the current request's memory owner.
func NewMemoryTool(engine *llm.Engine) Tool {
	return Tool{
		name:        "memory",
		description: "管理你的记忆。可以写入新记忆（write）、搜索记忆（search）、列出自己的记忆（list）、在后台整理记忆主题（reflect），或在群聊中设置已观察成员的优先称呼（set_member_name）。警告：请谨慎使用list，除非用户明确要求！",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型：write（写入）、search（搜索）、list（列出）、reflect（后台反思整理）、set_member_name（设置当前群成员优先称呼）",
					"enum":        []string{"write", "search", "list", "reflect", "set_member_name"},
				},
				"user_id": map[string]any{
					"type":        "string",
					"description": "set_member_name 时可选；目标成员的 QQ 号。不填则使用当前发言者的可信身份。只允许当前群已观察的成员。",
				},
				"preferred_name": map[string]any{
					"type":        "string",
					"description": "set_member_name 时必填；新的优先称呼。传空字符串可清除优先称呼，恢复使用 QQ 昵称。不要仅依据群名片设定称呼。",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "写入的记忆内容（action=write 时必填）",
				},
				"source_media": map[string]any{
					"type":        "string",
					"enum":        []string{"text", "image"},
					"description": "write 时的事实来源：从当前图片识别结果记住的内容使用 image；直接来自当前用户文字使用 text。图片内容不作为发图者原话。",
				},
				"evidence": map[string]any{
					"type":        "string",
					"description": "write 时可提供当前用户文字或图片识别结果中的原文片段。图文混合消息中，写入文字摘要时应提供文字原文作为 evidence。",
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
		executeResult: func(ctx context.Context, args string) (core.ToolExecutionResult, error) {
			var params struct {
				Action        string   `json:"action"`
				Content       string   `json:"content"`
				Tags          []string `json:"tags"`
				SourceMedia   string   `json:"source_media"`
				Evidence      string   `json:"evidence"`
				UserID        string   `json:"user_id"`
				PreferredName *string  `json:"preferred_name"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return textResult(""), fmt.Errorf("参数解析失败: %w", err)
			}

			runContext, ok := llm.RunContextFromContext(ctx)
			if !ok || runContext.Owner == "" {
				return textResult("无法获取当前用户信息"), nil
			}
			currentUser := runContext.Owner
			groupID := runContext.RouteScope.GroupID

			switch params.Action {
			case "set_member_name":
				if groupID == "" {
					return textResult("只能在群聊中设置成员称呼"), nil
				}
				if runContext.Mock {
					return textResult("模拟会话模式下不能持久化成员称呼"), nil
				}
				if engine.GroupManager == nil {
					return textResult("群记忆功能未启用"), nil
				}
				if params.PreferredName == nil {
					return textResult("设置成员称呼需要提供 preferred_name 参数"), nil
				}
				name := strings.TrimSpace(*params.PreferredName)
				if name != memory.SanitizeProfileText(*params.PreferredName) {
					return textResult("成员称呼不能包含控制字符且不能超过 64 个字符"), nil
				}
				userID := strings.TrimSpace(params.UserID)
				if userID == "" {
					userID = runContext.ActorUserID
				}
				if userID == "" {
					return textResult("无法确定目标成员的 QQ 号"), nil
				}
				groupStore, err := engine.GroupManager.GetGroupStoreForPlatform(runContext.RouteScope.Platform, groupID)
				if err != nil {
					return textResult(fmt.Sprintf("获取群成员档案失败: %v", err)), nil
				}
				updated, err := groupStore.SetObservedMemberPreferredName(userID, name, nil)
				if err != nil {
					return textResult(fmt.Sprintf("设置成员称呼失败: %v", err)), nil
				}
				if !updated {
					return textResult("当前群尚未观察到该成员，无法设置称呼"), nil
				}
				if name == "" {
					return textResult("成员优先称呼已清除"), nil
				}
				return textResult("成员优先称呼已更新"), nil
			case "write":
				if runContext.Mock {
					return textResult("记忆已记录（模拟会话：断电即丢，不持久化保存）"), nil
				}
				if params.Content == "" {
					return textResult("写入记忆需要提供 content 参数"), nil
				}
				if groupID != "" {
					if engine.GroupManager == nil {
						return textResult("群记忆功能未启用"), nil
					}
					groupStore, err := engine.GroupManager.GetGroupStoreForPlatform(runContext.RouteScope.Platform, groupID)
					if err != nil {
						return textResult(fmt.Sprintf("获取群记忆存储失败: %v", err)), nil
					}
					entry := memory.MemoryEntry{
						ID:        memory.GenerateID(),
						Owner:     memory.GroupOwnerExplicit,
						OwnerType: memory.OwnerGroup,
						ScopeType: memory.ScopeGroup,
						GroupID:   groupID,
						Content:   params.Content,
						Tags:      params.Tags,
						Source:    memory.SourceManual,
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					}
					_, hasTextEvidence := memory.ValidateEvidence(runContext.MemorySource.Content, params.Evidence)
					isText := params.SourceMedia == "text" &&
						(hasTextEvidence || strings.Contains(runContext.MemorySource.Content, params.Content))
					if runContext.MemorySource.ImageDescription != "" && !isText {
						entry.SourceMedia = memory.SourceMediaImage
						entry.Summary = memory.SanitizeSummary(params.Content)
						entry.Evidence, _ = memory.ValidateEvidence(runContext.MemorySource.ImageDescription, params.Evidence)
						entry.SourceMessageID = runContext.MemorySource.MessageID
						entry.SourceSenderID = runContext.ActorUserID
					}
					if err := groupStore.Save(entry); err != nil {
						return textResult(fmt.Sprintf("写入群记忆失败: %v", err)), nil
					}
					return core.ToolExecutionResult{Content: "群记忆已写入", MemoryWritten: true}, nil
				}
				if engine.MemoryWriter == nil {
					return textResult("记忆写入功能未启用"), nil
				}
				if err := engine.MemoryWriter.WriteByOwner(
					currentUser,
					runContext.OwnerType,
					params.Content,
					params.Tags,
				); err != nil {
					return textResult(fmt.Sprintf("写入失败: %v", err)), nil
				}
				return core.ToolExecutionResult{Content: "记忆已写入", MemoryWritten: true}, nil

			case "search":
				// Clean and validate tags
				tags := cleanSearchTags(params.Tags)
				if len(tags) == 0 {
					return textResult("搜索需要提供 tags 参数（1～6个标签）"), nil
				}
				if len(tags) > 6 {
					return textResult("搜索最多支持 6 个不同的 tags 标签"), nil
				}
				if groupID != "" {
					if engine.GroupManager == nil {
						return textResult("群记忆功能未启用"), nil
					}
					groupStore, err := engine.GroupManager.GetGroupStoreForPlatform(runContext.RouteScope.Platform, groupID)
					if err != nil {
						return textResult(fmt.Sprintf("获取群记忆存储失败: %v", err)), nil
					}
					entries, err := groupStore.SearchByTags(tags, 0)
					if err != nil {
						return textResult(fmt.Sprintf("搜索群记忆失败: %v", err)), nil
					}
					filtered := engine.MemoryGateway.FilterGroup(entries)
					filtered = engine.MemoryReader.Limit(filtered)
					if len(filtered) == 0 {
						return textResult("未找到相关群记忆"), nil
					}
					if !runContext.Mock {
						if err := groupStore.RecordRecall(filtered); err != nil {
							engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("更新群记忆召回次数失败: %v", err))
						}
						now := time.Now()
						for i := range filtered {
							filtered[i].AccessCount++
							filtered[i].UpdatedAt = now
						}
					}
					result, _ := json.Marshal(filtered)
					return textResult(string(result)), nil
				}
				if engine.MemoryReader == nil {
					return textResult("记忆搜索功能未启用"), nil
				}
				entries, err := engine.MemoryReader.SearchByTags(context.Background(), tags)
				if err != nil {
					return textResult(fmt.Sprintf("搜索失败: %v", err)), nil
				}
				filtered := engine.MemoryGateway.Filter(entries, currentUser)
				filtered = engine.MemoryReader.Limit(filtered)
				if len(filtered) == 0 {
					return textResult("未找到相关记忆"), nil
				}
				if !runContext.Mock {
					if err := engine.MemoryReader.RecordRecall(filtered); err != nil {
						engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("更新记忆召回次数失败: %v", err))
					}
					now := time.Now()
					for i := range filtered {
						filtered[i].AccessCount++
						filtered[i].UpdatedAt = now
					}
				}
				result, _ := json.Marshal(filtered)
				return textResult(string(result)), nil

			case "list":
				if groupID != "" {
					if engine.GroupManager == nil {
						return textResult("群记忆功能未启用"), nil
					}
					groupStore, err := engine.GroupManager.GetGroupStoreForPlatform(runContext.RouteScope.Platform, groupID)
					if err != nil {
						return textResult(fmt.Sprintf("获取群记忆存储失败: %v", err)), nil
					}
					entries, err := groupStore.ListAll()
					if err != nil {
						return textResult(fmt.Sprintf("列出群记忆失败: %v", err)), nil
					}
					filtered := engine.MemoryGateway.FilterGroup(entries)
					filtered = engine.MemoryReader.Limit(filtered)
					if len(filtered) == 0 {
						return textResult("当前群还没有任何记忆"), nil
					}
					result, _ := json.Marshal(filtered)
					return textResult(string(result)), nil
				}
				if engine.MemoryGateway == nil {
					return textResult("记忆功能未启用"), nil
				}
				entries, err := engine.MemoryReader.Recall(context.Background(), "")
				if err != nil {
					return textResult(fmt.Sprintf("列出失败: %v", err)), nil
				}
				filtered := engine.MemoryGateway.Filter(entries, currentUser)
				filtered = engine.MemoryReader.Limit(filtered)
				if len(filtered) == 0 {
					return textResult("你还没有任何记忆"), nil
				}
				result, _ := json.Marshal(filtered)
				return textResult(string(result)), nil

			case "reflect":
				if groupID != "" {
					return textResult("群聊暂不支持记忆反思整理"), nil
				}
				if runContext.Mock {
					return textResult("模拟会话模式下禁用记忆反思重构"), nil
				}
				if engine.MemoryReflections == nil {
					return textResult("记忆反思功能未启用"), nil
				}
				status, started, err := engine.MemoryReflections.Start(currentUser)
				if err != nil {
					return textResult(fmt.Sprintf("启动记忆反思失败: %v", err)), nil
				}
				if !started {
					target := status.Owner
					if target == "" {
						target = "全部用户"
					}
					return textResult(fmt.Sprintf("记忆反思任务正在运行（范围：%s）", target)), nil
				}
				return textResult("记忆反思任务已在后台启动，完成后会更新主题索引"), nil

			default:
				return textResult("未知操作，支持：write、search、list、reflect、set_member_name"), nil
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
