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
