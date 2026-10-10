package memory

import "time"

// Visibility 控制记忆条目的可见范围（已废弃：仅保留用于兼容历史 brain.json 数据反序列化）。
type Visibility string

const (
	// VisibilityPrivate 仅 owner 可见，Gateway 会过滤掉其他用户的 private 记忆。
	VisibilityPrivate Visibility = "private"
	// VisibilityPublic 所有人可见，如公共知识、项目信息等。
	VisibilityPublic Visibility = "public"
)

// ScopeType 标识记忆的作用域类型（私聊或群聊）。
type ScopeType string

const (
	// ScopePrivate 私聊作用域（按用户严格隔离）。
	ScopePrivate ScopeType = "private"
	// ScopeGroup 群聊作用域（按群严格独立持久化）。
	ScopeGroup ScopeType = "group"
)

const (
	// GroupOwnerExplicit 是群聊中涉及第三方、群规、多人关系或无法归属个人的条目的显式 owner 标识值。
	GroupOwnerExplicit = "group"
)

// OwnerType 区分 owner 是「单个用户」还是「某个群」——两套 owner 体系互不干扰：
// 私聊记忆 owner_type=user 跟随 userID；群聊记忆 owner_type=group 跟随 group:ID。
// 零值（""）视为 user，兼容未带此字段的老 brain.json。
type OwnerType string

const (
	// OwnerUser 私聊用户（owner 为 userID 字符串）。
	OwnerUser OwnerType = "user"
	// OwnerGroup 群聊（owner 为 "group:<群号>" 字符串）。
	OwnerGroup OwnerType = "group"
)

// Source 记忆的来源类型。
type Source string

const (
	// SourceExtract 由 LLM 从对话中自动提取。
	SourceExtract Source = "extract"
	// SourceManual 由用户明确指令写入（如"记住xxx"）。
	SourceManual Source = "manual"
	// SourceReflect 由反思系统生成。
	SourceReflect Source = "reflect"
	// SourceCompact 标记旧版本写入 brain.json 的群聊总结（已废弃：新总结不再写入记忆）。
	SourceCompact Source = "compact"
	// SourceDistill 由后台从滚动压缩快照中提炼的群聊长期记忆。
	SourceDistill Source = "distill"
)

// MemoryEntry represents a single memory record.
type MemoryEntry struct {
	ID          string     `json:"id"`                    // 唯一标识
	Owner       string     `json:"owner"`                 // 归属者（如 "frost"、"alice"，群聊中为发信者QQ自述，或显式 "group"）
	OwnerType   OwnerType  `json:"owner_type,omitempty"`  // owner 是人还是群（零值兼容老数据）
	ScopeType   ScopeType  `json:"scope_type,omitempty"`  // 作用域：private 或 group
	GroupID         string     `json:"group_id,omitempty"`          // 群号（ScopeType == group 时）
	Content         string     `json:"content"`                     // 记忆内容（Option A 下群聊提取为权威原始发言字面片段）
	Summary         string     `json:"summary,omitempty"`           // 可选的自然语言展示摘要（不作为权威事实覆盖原文）
	Evidence        string     `json:"evidence,omitempty"`          // 溯源原始发言字面片段
	SourceMessageID string     `json:"source_message_id,omitempty"` // 平台源消息唯一 ID
	SourceSenderID  string     `json:"source_sender_id,omitempty"`  // 平台源消息发送者 ID
	Tags            []string   `json:"tags"`                        // 标签（用于精确匹配和分类）
	Source      Source     `json:"source"`                // 来源
	Visibility  Visibility `json:"visibility,omitempty"`  // 可见性（已废弃）
	CreatedAt   time.Time  `json:"created_at"`            // 创建时间
	UpdatedAt   time.Time  `json:"updated_at"`            // 最后访问/更新时间
	AccessCount int        `json:"access_count"`          // 被召回次数
	MergedFrom  []string   `json:"merged_from,omitempty"` // 反思合并时的直接来源 ID
}

// MemoryMergeArchive keeps the complete source snapshots for a reflected
// merge. Archived entries are not searched or injected, but remain available
// in brain.json if a lossy merge ever needs to be inspected or recovered.
type MemoryMergeArchive struct {
	MergedID string        `json:"merged_id"`
	Owner    string        `json:"owner"`
	Sources  []MemoryEntry `json:"sources"`
	MergedAt time.Time     `json:"merged_at"`
}

// BrainData 统一大脑的持久化结构。
type BrainData struct {
	Entries       []MemoryEntry        `json:"entries"`
	MergeArchives []MemoryMergeArchive `json:"merge_archives,omitempty"`
}
