package memory

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// reflectPrompt 发送给 LLM 的反思提示词。
const reflectPrompt = `你是一个记忆管理助手。请分析以下记忆条目，完成以下任务：

1. 生成一段简洁的摘要（200字以内），概括所有关键信息
2. 标记哪些记忆已经过时或不再重要（给出 ID 列表）
3. 为每条记忆评估重要度（0.0~1.0）

返回 JSON 格式：
{
  "summary": "摘要内容",
  "key_topics": ["话题1", "话题2"],
  "outdated_ids": ["mem_xxx"],
  "importance_updates": {"mem_xxx": 0.9, "mem_yyy": 0.3}
}

只返回 JSON，不要其他文字。

当前记忆列表：
{memories}`

// Reflector handles periodic memory summarization and cleanup.
// It has global visibility — can see all memories regardless of owner.
type Reflector struct {
	store    *Store
	provider core.LLMProvider
	model    string
	config   Config
}

// NewReflector creates a new memory reflector.
func NewReflector(store *Store, provider core.LLMProvider, model string, config Config) *Reflector {
	return &Reflector{
		store:    store,
		provider: provider,
		model:    model,
		config:   config,
	}
}

// Reflect performs a full reflection cycle: summarize, update importance, remove outdated.
func (r *Reflector) Reflect(ctx context.Context) error {
	if r.provider == nil || r.model == "" {
		return fmt.Errorf("LLM provider not configured for reflector")
	}

	allMemories, err := r.store.ListAll()
	if err != nil {
		return fmt.Errorf("failed to list memories: %w", err)
	}

	if len(allMemories) == 0 {
		return nil
	}

	// Format memories for the prompt
	var sb strings.Builder
	for _, m := range allMemories {
		sb.WriteString(fmt.Sprintf("- [%s] (owner: %s, importance: %.1f): %s\n", m.ID, m.Owner, m.Importance, m.Content))
	}

	prompt := strings.Replace(reflectPrompt, "{memories}", sb.String(), 1)

	req := core.ChatRequest{
		Model: r.model,
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: prompt},
		},
		MaxTokens:   2048,
		Temperature: 0.2,
	}

	resp, err := r.provider.Chat(ctx, req)
	if err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("反思LLM调用失败: %v", err))
		return err
	}

	raw, ok := resp.Message.Content.(string)
	if !ok {
		return fmt.Errorf("unexpected response type: %T", resp.Message.Content)
	}

	return r.processReflection(allMemories, raw)
}

// reflectResult represents the LLM's reflection output.
type reflectResult struct {
	Summary            string             `json:"summary"`
	KeyTopics          []string           `json:"key_topics"`
	OutdatedIDs        []string           `json:"outdated_ids"`
	ImportanceUpdates  map[string]float64 `json:"importance_updates"`
}

// processReflection applies the LLM's reflection results.
func (r *Reflector) processReflection(allMemories []MemoryEntry, raw string) error {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result reflectResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("反思JSON解析失败: %v, raw: %s", err, raw))
		return err
	}

	// 1. Delete outdated memories
	outdatedSet := make(map[string]bool)
	for _, id := range result.OutdatedIDs {
		outdatedSet[id] = true
	}
	for _, m := range allMemories {
		if outdatedSet[m.ID] {
			if err := r.store.Delete(m.ID); err != nil {
				logs.Error(logs.SYSTEM, fmt.Sprintf("删除过时记忆失败: %v", err))
			} else {
				logs.Info(logs.SYSTEM, fmt.Sprintf("已删除过时记忆: %s", m.ID))
			}
		}
	}

	// 2. Update importance scores
	for id, newImportance := range result.ImportanceUpdates {
		if err := r.store.UpdateImportance(id, newImportance); err != nil {
			logs.Error(logs.SYSTEM, fmt.Sprintf("更新重要度失败 %s: %v", id, err))
		}
	}

	// 3. Generate summaries per owner
	ownerMemories := make(map[string][]MemoryEntry)
	for _, m := range allMemories {
		if !outdatedSet[m.ID] {
			ownerMemories[m.Owner] = append(ownerMemories[m.Owner], m)
		}
	}

	for owner, memories := range ownerMemories {
		summary := MemorySummary{
			Owner:       owner,
			Summary:     r.buildOwnerSummary(memories),
			KeyTopics:   result.KeyTopics,
			GeneratedAt: time.Now(),
		}
		if err := r.store.SaveSummary(summary); err != nil {
			logs.Error(logs.SYSTEM, fmt.Sprintf("保存摘要失败 (owner: %s): %v", owner, err))
		}
	}

	logs.Info(logs.SYSTEM, fmt.Sprintf("反思完成：处理 %d 条记忆，删除 %d 条过时记忆", len(allMemories), len(result.OutdatedIDs)))
	return nil
}

// buildOwnerSummary creates a natural language summary for one owner's memories.
func (r *Reflector) buildOwnerSummary(memories []MemoryEntry) string {
	var sb strings.Builder
	for _, m := range memories {
		sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
	}
	return sb.String()
}
