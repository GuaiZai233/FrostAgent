package core

import "context"

type LLMProvider interface {
	// Chat returns a pointer to ChatResponse to allow returning nil on error.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

type ChatRequest struct {
	Model    string
	Messages []ChatMessage
	Tools    []ToolSpec
}

type ContentPartType string

const (
	ContentPartTypeText  ContentPartType = "text"
	ContentPartTypeImage ContentPartType = "image"
)

type ImageURL struct {
	URL string `json:"url"`
}

type ContentPart struct {
	Type     ContentPartType `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *ImageURL       `json:"image_url,omitempty"`
}

type ChatMessage struct {
	Role    MessageRole `json:"role"`
	Content any         `json:"content"` // Can be string or []ContentPart
}

type ChatResponse struct {
	Message ChatMessage
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type AgentService interface {
	Handle(ctx context.Context, input IncomingMessage) ([]OutgoingMessage, error)
}

// MessageAdapter 定义了不同平台（如 OneBot, Discord）发送消息的统一接口
type MessageAdapter interface {
	Send(ctx context.Context, msg OutgoingMessage) error
	ID() string
}

// MessageDispatcher 负责将核心层生成的回复路由到正确的适配器进行发送
type MessageDispatcher interface {
	RegisterAdapter(adapter MessageAdapter)
	Dispatch(ctx context.Context, platform string, msg OutgoingMessage) error
}
