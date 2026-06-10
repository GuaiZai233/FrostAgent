package core

import "time"

type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

type AttachmentType string

const (
	AttachmentTypeImage AttachmentType = "image"
	AttachmentTypeFile  AttachmentType = "file"
)

type Attachment struct {
	Type     AttachmentType
	Content  []byte
	MimeType string
	URL      string
}

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

type OutgoingMessage struct {
	Content     string
	Attachments []Attachment
	Metadata    map[string]any
}
