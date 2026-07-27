package core

import "time"

type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// ContentPartType 多模态内容类型枚举，描述送给 LLM 的最终结构(目前只有 text/image_url)
type ContentPartType string

const (
	ContentPartTypeText  ContentPartType = "text"
	ContentPartTypeImage ContentPartType = "image_url"
)

// AttachmentType 消息附件类型枚举（描述平台侧的原始物，可能是文件、语音等更宽泛的类型）
type AttachmentType string

const (
	AttachmentTypeImage AttachmentType = "image"
	AttachmentTypeFile  AttachmentType = "file"
)

// Attachment 消息附件（内容/URL/MIME）
type Attachment struct {
	Type     AttachmentType
	Content  []byte
	MimeType string
	URL      string
}

// IncomingMessage 平台上游入站消息（含会话、用户、平台、附件等元数据）。从任何平台流进 core 层的一条消息,是 AgentService.Handle() 的输入。
type IncomingMessage struct {
	ID          string
	SessionID   string
	UserID      string
	Content     string
	Platform    string
	CreatedAt   time.Time
	Metadata    map[string]any
	Attachments []Attachment
}

// OutgoingMessage 送回平台的出站消息
type OutgoingMessage struct {
	Content     string
	Attachments []Attachment
	Metadata    map[string]any
}
