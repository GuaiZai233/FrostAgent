package memory

import "time"

// Visibility 控制记忆条目的可见范围。
type Visibility string

const (
	// VisibilityPrivate 仅 owner 可见，Gateway 会过滤掉其他用户的 private 记忆。
	VisibilityPrivate Visibility = "private"
	// VisibilityPublic 所有人可见，如公共知识、项目信息等。
	VisibilityPublic Visibility = "public"
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
	// SourceCompact 标记旧版本写入 brain.json 的群聊总结，仅用于迁移清理。
	SourceCompact Source = "compact"
)

// MemoryEntry represents a single memory record.
type MemoryEntry struct {
	ID          string     `json:"id"`                    // 唯一标识
	Owner       string     `json:"owner"`                 // 归属者（如 "frost"、"alice"，群聊则为 "group:<群号>"）
	OwnerType   OwnerType  `json:"owner_type"`            // owner 是人还是群（零值兼容老数据）
	Content     string     `json:"content"`               // 记忆内容（自然语言）
	Tags        []string   `json:"tags"`                  // 标签（用于精确匹配和分类）
	Source      Source     `json:"source"`                // 来源
	Visibility  Visibility `json:"visibility"`            // 可见性
	Importance  float64    `json:"importance"`            // 重要度 0.0~1.0（反思时更新）
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
