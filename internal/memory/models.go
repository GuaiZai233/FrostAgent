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

// Source 记忆的来源类型。
type Source string

const (
	// SourceExtract 由 LLM 从对话中自动提取。
	SourceExtract Source = "extract"
	// SourceManual 由用户明确指令写入（如"记住xxx"）。
	SourceManual Source = "manual"
	// SourceReflect 由反思系统生成。
	SourceReflect Source = "reflect"
)

// MemoryEntry represents a single memory record.
type MemoryEntry struct {
	ID          string     `json:"id"`           // 唯一标识
	Owner       string     `json:"owner"`        // 归属者（如 "frost"、"alice"）
	Content     string     `json:"content"`      // 记忆内容（自然语言）
	Tags        []string   `json:"tags"`         // 标签（用于精确匹配和分类）
	Source      Source     `json:"source"`       // 来源
	Visibility  Visibility `json:"visibility"`   // 可见性
	Importance  float64    `json:"importance"`   // 重要度 0.0~1.0（反思时更新）
	CreatedAt   time.Time  `json:"created_at"`   // 创建时间
	UpdatedAt   time.Time  `json:"updated_at"`   // 最后访问/更新时间
	AccessCount int        `json:"access_count"` // 被召回次数
}

// MemorySummary is the product of the reflection system.
type MemorySummary struct {
	Owner       string    `json:"owner"`        // 归属者
	Summary     string    `json:"summary"`      // 自然语言摘要（供注入 system prompt）
	KeyTopics   []string  `json:"key_topics"`   // 关键话题索引
	GeneratedAt time.Time `json:"generated_at"` // 生成时间
}

// BrainData 统一大脑的持久化结构。
type BrainData struct {
	Entries   []MemoryEntry   `json:"entries"`
	Summaries []MemorySummary `json:"summaries,omitempty"`
}
