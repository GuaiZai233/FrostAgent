package memory

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const reflectPrompt = `你是一个记忆整理助手。请只分析下面属于同一用户的记忆，完成以下任务：

1. 提取 3～20 个便于未来检索的主题。主题应简短、具体，合并同义表达，并把别名放入 aliases
2. 标记已经明确过时、被新事实取代或不再有保留价值的记忆 ID
3. 为每条记忆评估重要度（0.0～1.0）

主题只是检索索引，不要在主题中编造原始记忆没有的事实。不要因为两条记忆相似就擅自删除；只有明确过时或被取代时才放入 outdated_ids。

返回 JSON：
{
  "topics": [
    {"name": "舞萌", "aliases": ["maimai", "乌蒙"], "importance": 0.9}
  ],
  "outdated_ids": ["mem_xxx"],
  "importance_updates": {"mem_xxx": 0.9}
}

只返回 JSON，不要输出其他文字。

用户：{owner}
记忆列表：
{memories}`

// Reflector analyzes memories per owner and rebuilds the replaceable topic
// catalog. It never injects a full summary into the conversation.
type Reflector struct {
	store    *Store
	catalog  *CatalogStore
	vs       *VectorStore
	provider core.LLMProvider
	model    string
	config   Config
}

// NewReflector creates a memory reflector.
func NewReflector(
	store *Store,
	catalog *CatalogStore,
	provider core.LLMProvider,
	model string,
	config Config,
) *Reflector {
	return &Reflector{
		store:    store,
		catalog:  catalog,
		provider: provider,
		model:    model,
		config:   config,
	}
}

// SetVectorStore enables cleanup of vectors for memories removed by reflection.
func (r *Reflector) SetVectorStore(vs *VectorStore) {
	r.vs = vs
}

// Available reports whether reflection has the dependencies required to run.
func (r *Reflector) Available() bool {
	return r != nil && r.store != nil && r.catalog != nil &&
		r.provider != nil && r.model != ""
}

// Reflect performs a full reflection cycle, isolated by owner.
func (r *Reflector) Reflect(ctx context.Context) error {
	if !r.Available() {
		return fmt.Errorf("LLM provider or model not configured for reflector")
	}

	entries, err := r.store.ListAll()
	if err != nil {
		return fmt.Errorf("list memories for reflection: %w", err)
	}

	owners := make(map[string]bool)
	for _, entry := range entries {
		if entry.Owner != "" {
			owners[entry.Owner] = true
		}
	}
	names := make([]string, 0, len(owners))
	for owner := range owners {
		names = append(names, owner)
	}
	sort.Strings(names)

	var reflectionErrors []error
	for _, owner := range names {
		if err := r.ReflectOwner(ctx, owner); err != nil {
			reflectionErrors = append(
				reflectionErrors,
				fmt.Errorf("reflect owner %s: %w", owner, err),
			)
		}
	}
	return errors.Join(reflectionErrors...)
}

// ReflectOwner reflects only one owner's memories.
func (r *Reflector) ReflectOwner(ctx context.Context, owner string) error {
	if !r.Available() {
		return fmt.Errorf("LLM provider or model not configured for reflector")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return fmt.Errorf("owner is required")
	}

	entries, err := r.store.ListByOwner(owner)
	if err != nil {
		return fmt.Errorf("list owner memories: %w", err)
	}
	if len(entries) == 0 {
		return r.catalog.Delete(owner)
	}

	var memories strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(
			&memories,
			"- [%s] (importance: %.2f, tags: %s): %s\n",
			entry.ID,
			entry.Importance,
			strings.Join(entry.Tags, ", "),
			entry.Content,
		)
	}

	prompt := strings.NewReplacer(
		"{owner}", owner,
		"{memories}", memories.String(),
	).Replace(reflectPrompt)

	resp, err := r.provider.Chat(ctx, core.ChatRequest{
		Model: r.model,
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: prompt},
		},
		MaxTokens:   2048,
		Temperature: 0.2,
	})
	if err != nil {
		return fmt.Errorf("call reflection LLM: %w", err)
	}

	raw, ok := resp.Message.Content.(string)
	if !ok {
		return fmt.Errorf("unexpected reflection response type: %T", resp.Message.Content)
	}
	return r.applyResult(owner, entries, raw)
}

type reflectResult struct {
	Topics            []MemoryTopic      `json:"topics"`
	OutdatedIDs       []string           `json:"outdated_ids"`
	ImportanceUpdates map[string]float64 `json:"importance_updates"`
}

func (r *Reflector) applyResult(owner string, entries []MemoryEntry, raw string) error {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result reflectResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return fmt.Errorf("parse reflection result: %w", err)
	}

	allowedIDs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		allowedIDs[entry.ID] = true
	}

	outdated := make([]string, 0, len(result.OutdatedIDs))
	seenOutdated := make(map[string]bool)
	for _, id := range result.OutdatedIDs {
		if allowedIDs[id] && !seenOutdated[id] {
			outdated = append(outdated, id)
			seenOutdated[id] = true
		}
	}

	importance := make(map[string]float64)
	for id, value := range result.ImportanceUpdates {
		if allowedIDs[id] && !seenOutdated[id] {
			importance[id] = value
		}
	}

	remaining, deleted, err := r.store.ApplyReflection(owner, outdated, importance)
	if err != nil {
		return fmt.Errorf("apply reflection changes: %w", err)
	}
	for _, id := range deleted {
		if r.vs != nil {
			if err := r.vs.Remove(id); err != nil {
				logs.Error(logs.SYSTEM, fmt.Sprintf("清理过时记忆向量失败 %s: %v", id, err))
			}
		}
	}

	topics := cleanTopics(result.Topics)
	if len(remaining) == 0 {
		if err := r.catalog.Delete(owner); err != nil {
			return fmt.Errorf("delete empty memory catalog: %w", err)
		}
	} else {
		if err := r.catalog.Replace(UserMemoryCatalog{
			Owner:       owner,
			Topics:      topics,
			MemoryCount: len(remaining),
			GeneratedAt: time.Now(),
		}); err != nil {
			return fmt.Errorf("save memory catalog: %w", err)
		}
	}

	logs.Info(
		logs.SYSTEM,
		fmt.Sprintf(
			"反思完成：owner=%s，处理 %d 条记忆，删除 %d 条，生成 %d 个主题",
			owner,
			len(entries),
			len(deleted),
			len(topics),
		),
	)
	return nil
}

func cleanTopics(topics []MemoryTopic) []MemoryTopic {
	cleaned := make([]MemoryTopic, 0, len(topics))
	seen := make(map[string]bool)
	for _, topic := range topics {
		name := sanitizeTopicText(topic.Name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true

		aliases := make([]string, 0, len(topic.Aliases))
		seenAliases := make(map[string]bool)
		for _, alias := range topic.Aliases {
			alias = sanitizeTopicText(alias)
			aliasKey := strings.ToLower(alias)
			if alias == "" || aliasKey == key || seenAliases[aliasKey] {
				continue
			}
			seenAliases[aliasKey] = true
			aliases = append(aliases, alias)
			if len(aliases) == 8 {
				break
			}
		}

		if topic.Importance < 0 {
			topic.Importance = 0
		} else if topic.Importance > 1 {
			topic.Importance = 1
		}
		cleaned = append(cleaned, MemoryTopic{
			Name:       name,
			Aliases:    aliases,
			Importance: topic.Importance,
		})
		if len(cleaned) == 32 {
			break
		}
	}
	return cleaned
}
