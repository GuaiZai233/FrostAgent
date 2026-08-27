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

当前系统时间：{current_time}

1. 提取 3～20 个便于未来检索的主题。主题应简短、具体，合并同义表达，并把别名放入 aliases
2. 标记已经明确过时、被新事实取代或不再有保留价值的记忆 ID
3. 将描述同一主体、内容兼容且合并后更利于检索的记忆整合成一条

主题只是检索索引，不要在主题中编造原始记忆没有的事实。不要因为两条记忆相似就擅自删除；只有明确过时或被取代时才放入 outdated_ids。

时效性规则（对照当前系统时间判断）：
- 仅在某时间点之前/当天有效、且该时间已过的记忆（如"用户明天要去极兽聚"、"用户本周在成都"、"用户预约下周一去医院"），属于已过时效性，应放入 outdated_ids 删除
- 长期偏好仍成立、只是夹带已过去临时信息的记忆（如"用户喜欢打舞萌，下周要去参赛"），不要整条删除：可把已失效的临时部分拆出后用 outdated_ids 删除
- 不要因为内容提及过去的日期就一律删除；只有当日期已过且该事实本身已失效时才删除

合并规则：
- 每组合并必须包含 2～8 个不同的 source_ids，每轮最多返回 10 组
- 合并后的 content 必须保留每条来源记忆中的独立事实，不得新增、猜测、纠正或省略信息
- tags 只能整理来源记忆中已有的主题、实体、别名和缩写，不得编造新别名
- 有冲突的记忆不能合并；明确的新旧替代关系应使用 outdated_ids
- 仅仅标签相似或属于同一个大类，不足以合并；必须描述同一主体且能组成一条自然、无冲突的记忆
- 同一个 ID 不能出现在多组合并中，也不能同时出现在 merges 和 outdated_ids 中
- 如果没有安全、必要的合并，返回空数组 "merges": []

返回 JSON：
{
  "topics": [
    {"name": "舞萌", "aliases": ["maimai", "乌蒙"]}
  ],
  "outdated_ids": ["mem_xxx"],
  "merges": [
    {
      "source_ids": ["mem_001", "mem_002"],
      "content": "用户是 dx rating 为 w6 的舞萌爱好者",
      "tags": ["舞萌", "dx rating", "w6", "maimai"]
    }
  ]
}

只返回 JSON，不要输出其他文字。

用户：{owner}
记忆列表：
{memories}`

// CurrentTimeLabel formats the current time with a Chinese weekday so the LLM
// can judge relative dates ("明天", "下周") when evaluating whether
// time-sensitive memory content is still valid.
func CurrentTimeLabel(t time.Time) string {
	weekdays := [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	return t.Format("2006-01-02 15:04") + " " + weekdays[int(t.Weekday())]
}

// Reflector analyzes memories per owner and rebuilds the replaceable topic
// catalog. It never injects a full summary into the conversation.
type Reflector struct {
	store    *Store
	catalog  *CatalogStore
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
	if config.ReflectTimeout <= 0 {
		config.ReflectTimeout = DefaultConfig().ReflectTimeout
	}
	return &Reflector{
		store:    store,
		catalog:  catalog,
		provider: provider,
		model:    model,
		config:   config,
	}
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
			"- [%s] (tags: %s): %s\n",
			entry.ID,
			strings.Join(entry.Tags, ", "),
			entry.Content,
		)
	}

	prompt := strings.NewReplacer(
		"{owner}", owner,
		"{memories}", memories.String(),
		"{current_time}", CurrentTimeLabel(time.Now()),
	).Replace(reflectPrompt)

	callCtx := ctx
	cancel := func() {}
	if r.config.ReflectTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, r.config.ReflectTimeout)
	}
	defer cancel()

	startedAt := time.Now()
	promptBytes := len([]byte(prompt))
	logs.Info(
		logs.SYSTEM,
		fmt.Sprintf(
			"开始记忆反思请求：owner=%s，记忆=%d 条，prompt=%d bytes，timeout=%s",
			owner,
			len(entries),
			promptBytes,
			r.config.ReflectTimeout,
		),
	)

	resp, err := r.provider.Chat(callCtx, core.ChatRequest{
		Model: r.model,
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: prompt},
		},
		MaxTokens:   4096,
		Temperature: 0.2,
		Route:       r.store.RouteForOwner(owner),
	})
	if err != nil {
		return fmt.Errorf(
			"call reflection LLM after %s (memories=%d, prompt_bytes=%d): %w",
			time.Since(startedAt).Round(time.Millisecond),
			len(entries),
			promptBytes,
			err,
		)
	}
	logs.Info(
		logs.SYSTEM,
		fmt.Sprintf(
			"记忆反思响应完成：owner=%s，耗时=%s",
			owner,
			time.Since(startedAt).Round(time.Millisecond),
		),
	)

	raw, ok := resp.Message.Content.(string)
	if !ok {
		return fmt.Errorf("unexpected reflection response type: %T", resp.Message.Content)
	}
	return r.applyResult(owner, entries, raw)
}

type reflectResult struct {
	Topics      []MemoryTopic  `json:"topics"`
	OutdatedIDs []string       `json:"outdated_ids"`
	Merges      []reflectMerge `json:"merges"`
}

type reflectMerge struct {
	SourceIDs []string `json:"source_ids"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
}

type validatedMerge struct {
	Sources []MemoryEntry
	Content string
	Tags    []string
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

	requestedOutdated := make(map[string]bool, len(result.OutdatedIDs))
	for _, rawID := range result.OutdatedIDs {
		id := strings.TrimSpace(rawID)
		if allowedIDs[id] {
			requestedOutdated[id] = true
		}
	}

	merges, protectedIDs, rejectedMerges := validateReflectionMerges(
		entries,
		result.Merges,
		requestedOutdated,
	)
	if rejectedMerges > 0 {
		logs.Warn(
			logs.SYSTEM,
			fmt.Sprintf("反思忽略了 %d 组不安全的记忆合并候选", rejectedMerges),
		)
	}

	outdated := make([]string, 0, len(result.OutdatedIDs))
	seenOutdated := make(map[string]bool)
	for _, rawID := range result.OutdatedIDs {
		id := strings.TrimSpace(rawID)
		if allowedIDs[id] && !protectedIDs[id] && !seenOutdated[id] {
			outdated = append(outdated, id)
			seenOutdated[id] = true
		}
	}

	applied, err := r.store.applyReflectionWithMerges(owner, merges, outdated)
	if err != nil {
		return fmt.Errorf("apply reflection changes: %w", err)
	}
	topics := cleanTopics(result.Topics)
	if len(applied.Remaining) == 0 {
		if err := r.catalog.Delete(owner); err != nil {
			return fmt.Errorf("delete empty memory catalog: %w", err)
		}
	} else {
		if err := r.catalog.Replace(UserMemoryCatalog{
			Owner:       owner,
			Topics:      topics,
			MemoryCount: len(applied.Remaining),
			GeneratedAt: time.Now(),
		}); err != nil {
			return fmt.Errorf("save memory catalog: %w", err)
		}
	}

	logs.Info(
		logs.SYSTEM,
		fmt.Sprintf(
			"反思完成：owner=%s，处理 %d 条记忆，合并 %d 组（归档 %d 条来源），删除 %d 条过时记忆，生成 %d 个主题",
			owner,
			len(entries),
			len(applied.MergedEntries),
			applied.MergedSourceCount,
			len(applied.OutdatedIDs),
			len(topics),
		),
	)
	return nil
}

const (
	maxReflectionMerges = 10
	maxMergeSources     = 8
	maxMergedContent    = 1200
	maxMergedTags       = 8
)

// validateReflectionMerges treats the LLM output as an untrusted proposal.
// Any source mentioned by a merge is protected from outdated deletion for the
// current cycle, even when the merge itself is rejected.
func validateReflectionMerges(
	entries []MemoryEntry,
	candidates []reflectMerge,
	requestedOutdated map[string]bool,
) ([]validatedMerge, map[string]bool, int) {
	entryByID := make(map[string]MemoryEntry, len(entries))
	for _, entry := range entries {
		entryByID[entry.ID] = entry
	}

	normalizedIDs := make([][]string, len(candidates))
	structurallyValid := make([]bool, len(candidates))
	usage := make(map[string]int)
	protected := make(map[string]bool)

	for i, candidate := range candidates {
		valid := true
		seen := make(map[string]bool)
		ids := make([]string, 0, len(candidate.SourceIDs))
		for _, rawID := range candidate.SourceIDs {
			id := strings.TrimSpace(rawID)
			if id == "" || seen[id] {
				valid = false
				continue
			}
			seen[id] = true
			ids = append(ids, id)
			if _, ok := entryByID[id]; !ok {
				valid = false
				continue
			}
			usage[id]++
			protected[id] = true
		}
		if len(ids) < 2 || len(ids) > maxMergeSources {
			valid = false
		}
		normalizedIDs[i] = ids
		structurallyValid[i] = valid
	}

	merges := make([]validatedMerge, 0, min(len(candidates), maxReflectionMerges))
	rejected := 0
	for i, candidate := range candidates {
		valid := structurallyValid[i] && i < maxReflectionMerges
		for _, id := range normalizedIDs[i] {
			if usage[id] > 1 || requestedOutdated[id] {
				valid = false
			}
		}

		content := strings.TrimSpace(candidate.Content)
		if content == "" || len([]rune(content)) > maxMergedContent {
			valid = false
		}
		tags := cleanMergeTags(candidate.Tags)
		if len(tags) == 0 {
			valid = false
		}

		if !valid {
			rejected++
			continue
		}

		sources := make([]MemoryEntry, 0, len(normalizedIDs[i]))
		for _, id := range normalizedIDs[i] {
			sources = append(sources, entryByID[id])
		}
		merges = append(merges, validatedMerge{
			Sources: sources,
			Content: content,
			Tags:    tags,
		})
	}
	return merges, protected, rejected
}

func cleanMergeTags(tags []string) []string {
	cleaned := make([]string, 0, min(len(tags), maxMergedTags))
	seen := make(map[string]bool)
	for _, tag := range tags {
		tag = sanitizeTopicText(tag)
		key := strings.ToLower(tag)
		if tag == "" || seen[key] {
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, tag)
		if len(cleaned) == maxMergedTags {
			break
		}
	}
	return cleaned
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

		cleaned = append(cleaned, MemoryTopic{
			Name:    name,
			Aliases: aliases,
		})
		if len(cleaned) == 32 {
			break
		}
	}
	return cleaned
}
