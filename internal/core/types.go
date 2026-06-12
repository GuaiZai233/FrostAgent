package core

// ──────────────────────────────────────────────
//  LLM Chat Types — single source of truth
// ──────────────────────────────────────────────

// ChatRequest represents a request to an LLM.
type ChatRequest struct {
	Model       string
	Messages    []ChatMessage
	Tools       []Tool
	ToolChoice  string // "auto", "none", or a specific tool name
	MaxTokens   int
	Temperature float64
}

// ChatResponse represents a response from an LLM.
type ChatResponse struct {
	Message ChatMessage
	Usage   *Usage
}

// ChatStreamEvent represents a single streaming event.
type ChatStreamEvent struct {
	Content string
	Done    bool
	Error   error
}

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role       MessageRole
	Content    any // string | []ContentPart
	ToolCalls  []ToolCall
	ToolCallID string
}

// ContentPart represents a piece of multimodal content (text/image).
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL represents a URL or Base64 image source.
type ImageURL struct {
	URL string `json:"url"`
}

// ToolCall represents an LLM's request to call a tool.
type ToolCall struct {
	ID       string
	Type     string // "function"
	Function ToolCallFunction
}

// ToolCallFunction holds the function details of a tool call.
type ToolCallFunction struct {
	Name      string
	Arguments string // JSON-encoded arguments
}

// Tool is the metadata structure passed to LLM as function definition.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Usage tracks token usage.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}