package core

import "context"

type LLMProvider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

type ChatRequest struct {
	Model    string
	Messages []ChatMessage
	Tools    []ToolSpec
}

type ChatMessage struct {
	Role    MessageRole
	Content any // Can be string or multi-modal parts
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
