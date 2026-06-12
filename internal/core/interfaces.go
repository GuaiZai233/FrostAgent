package core

import "context"

// LLMProvider defines the interface for LLM API calls.
type LLMProvider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// AgentService defines the interface for agent message handling.
type AgentService interface {
	Handle(ctx context.Context, input IncomingMessage) ([]OutgoingMessage, error)
}

// MessageAdapter defines the interface for platform-specific message sending.
type MessageAdapter interface {
	Send(ctx context.Context, msg OutgoingMessage) error
	ID() string
}

// MessageDispatcher routes core-layer outputs to the correct adapter.
type MessageDispatcher interface {
	RegisterAdapter(adapter MessageAdapter)
	Dispatch(ctx context.Context, platform string, msg OutgoingMessage) error
}

// ToolRegistry defines the interface for tool registration and lookup.
type ToolRegistry interface {
	Register(t Tool)
	GetTool(name string) (Tool, bool)
	GetExecutor(name string) (func(args string) (string, error), bool)
	ListTools() []Tool
	Execute(name, args string) (string, error)
}

// Session defines a single conversation session.
type Session interface {
	ID() string
	AddMessage(msg ChatMessage)
	Messages() []ChatMessage
	Clear()
}

// SessionStore manages all active sessions.
type SessionStore interface {
	Get(sessionID string) (Session, bool)
	Create(sessionID string) Session
	Delete(sessionID string)
}