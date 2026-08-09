package memory

import "FrostAgent/internal/core"

// PendingExtractionItem 是一条带 owner 标签的待提取消息。
// Engine 在 buffer 阈值触发时按 owner 分组，对每组调一次 Writer.Extract。
// 这个类型放在 memory 包（而不是 llm 包）以避免反向依赖：llm 通过此类型
// 喂数据给 memory.Writer。
type PendingExtractionItem struct {
	Owner     string           // 主键字符串（userID 或 "group:<群号>"）
	OwnerType OwnerType        // 区分人 / 群
	Message   core.ChatMessage // 消息内容（user 或 assistant）
}
