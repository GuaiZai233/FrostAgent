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
	AttachmentTypeAudio AttachmentType = "audio"
	AttachmentTypeVideo AttachmentType = "video"
)

// Attachment 消息附件（内容/URL/MIME）
type Attachment struct {
	Type     AttachmentType `json:"type"`
	SubType  int            `json:"sub_type,omitempty"`
	Content  []byte         `json:"content,omitempty"`
	MimeType string         `json:"mime_type,omitempty"`
	URL      string         `json:"url,omitempty"`
	Name     string         `json:"name,omitempty"`
}

// IncomingMessage 平台上游入站消息（含会话、用户、平台、附件等元数据）。
type IncomingMessage struct {
	ID          string         `json:"id"`
	SessionID   string         `json:"session_id"`
	UserID      string         `json:"user_id"`
	SenderName  string         `json:"sender_name,omitempty"`
	SenderCard  string         `json:"sender_card,omitempty"`
	Content     string         `json:"content"`
	Platform    string         `json:"platform"`     // "qq", "telegram", "discord", "astrbot", etc.
	MessageType string         `json:"message_type"` // "group" | "private"
	GroupID     string         `json:"group_id,omitempty"`
	GroupName   string         `json:"group_name,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	RawMessage  any            `json:"raw_message,omitempty"`
}

// OutgoingMessage 送回平台的出站消息
type OutgoingMessage struct {
	TargetID    string         `json:"target_id"`    // 目标 group_id 或 user_id
	MessageType string         `json:"message_type"` // "group" | "private"
	Platform    string         `json:"platform"`
	Content     string         `json:"content"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}
