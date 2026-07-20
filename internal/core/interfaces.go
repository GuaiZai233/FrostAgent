package core

import "context"

// LLMProvider defines the interface for LLM API calls.
//
// 屏蔽不同 LLM 厂商（OpenAI / DeepSeek / Claude / 通义千问……）的 API 差异。
// - 输入：一个统一的 ChatRequest（包含 model、messages、tools等,定义在 types.go）
// - 输出：统一的 *ChatResponse,或 error
// - 意义：切换供应商只需换实现,上层代码零改动
type LLMProvider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// AgentService defines the interface for agent message handling.
// - 输入：一条平台无关的 IncomingMessage（用户来的消息）
// - 输出：一个或多个 OutgoingMessage(返回 slice 是关键设计——支持分条回复、图文混合、流式分段等)
type AgentService interface {
	Handle(ctx context.Context, input IncomingMessage) ([]OutgoingMessage, error)
}

// MessageAdapter defines the interface for platform-specific message sending.
// 把消息真正发送到具体平台（QQ、微信、HTTP 回调、WebSocket 等）。
type MessageAdapter interface {
	Send(ctx context.Context, msg OutgoingMessage) error // 执行发送动作
	ID() string                                          // 返回平台唯一标识（如 "qq"、"http"),用于 Dispatcher 路由
}

// MessageDispatcher 把 core 层输出的消息按 platform 字符串路由到正确的 MessageAdapter。
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
	AddMessage(msg ChatMessage) // 追加一条消息到历史
	Messages() []ChatMessage    // 取出全部历史（供下次 LLM 调用时拼上下文）
	Clear()
}

// SessionStore manages all active sessions.
type SessionStore interface {
	Get(sessionID string) (Session, bool) // 按 ID 查会话，返回值表示"是否存在"
	Create(sessionID string) Session
	Delete(sessionID string)
}
