package llm

import (
	"FrostAgent/internal/core"
	"context"
	"strings"
	"sync"
	"testing"
)

func TestLooksLikeMessagePayload(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{
			name:   "send_message output",
			input:  `{"messages":[{"type":"plain","text":"hello"}]}`,
			expect: true,
		},
		{
			name:   "send_sticker output",
			input:  `{"messages":[{"type":"image","path":"/data/sticker/abc.png","is_sticker":true}]}`,
			expect: true,
		},
		{
			name:   "empty messages array",
			input:  `{"messages":[]}`,
			expect: false,
		},
		{
			name:   "null messages",
			input:  `{"messages":null}`,
			expect: false,
		},
		{
			name:   "error result",
			input:  `{"error":"no matching sticker found"}`,
			expect: false,
		},
		{
			name:   "plain text",
			input:  `search results: ...`,
			expect: false,
		},
		{
			name:   "no messages key",
			input:  `{"result":"ok"}`,
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeMessagePayload(tt.input)
			if got != tt.expect {
				t.Errorf("looksLikeMessagePayload(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

// --- Engine-level SendHook integration test ---

type sequentialProvider struct {
	mu        sync.Mutex
	callCount int
	responses []core.ChatResponse
}

func (p *sequentialProvider) Chat(_ context.Context, _ core.ChatRequest) (*core.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.callCount
	p.callCount++
	if idx >= len(p.responses) {
		return &core.ChatResponse{Message: core.ChatMessage{Role: "assistant", Content: "done"}}, nil
	}
	r := p.responses[idx]
	return &r, nil
}

type staticTool struct {
	name   string
	result string
}

func (t *staticTool) Name() string                   { return t.name }
func (t *staticTool) Description() string             { return "test tool" }
func (t *staticTool) Parameters() map[string]any      { return nil }
func (t *staticTool) Execute(_ string) (string, error) { return t.result, nil }

func TestEngine_SendHookTriggeredByMessagePayload(t *testing.T) {
	stickerPayload := `{"messages":[{"type":"image","path":"/data/sticker/abc.png","is_sticker":true}]}`

	provider := &sequentialProvider{
		responses: []core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: "assistant",
					ToolCalls: []core.ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: core.ToolCallFunction{
							Name:      "send_sticker",
							Arguments: `{"keyword":"happy"}`,
						},
					}},
				},
			},
			{
				Message: core.ChatMessage{
					Role:    "assistant",
					Content: "已发送表情包",
				},
			},
		},
	}

	tool := &staticTool{name: "send_sticker", result: stickerPayload}

	var hookCalled bool
	var hookPayload string

	engine := &Engine{
		MaxIterations: 5,
		Provider:      provider,
		ToolRegistry:  map[string]ToolExecutor{"send_sticker": tool},
	}

	ctx := WithRunContext(context.Background(), RunContext{
		SessionID: "test-session",
		Owner:     "test-owner",
		SendHook: func(toolResultJSON string) error {
			hookCalled = true
			hookPayload = toolResultJSON
			return nil
		},
	})

	result := engine.runLoopWithResult(ctx, []ChatMessage{
		{Role: "system", Content: "you are a bot"},
		{Role: "user", Content: "send me a sticker"},
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !hookCalled {
		t.Fatal("SendHook was not called")
	}
	if hookPayload != stickerPayload {
		t.Errorf("SendHook payload = %q, want %q", hookPayload, stickerPayload)
	}
	if !strings.Contains(result.Content, "已发送") {
		t.Errorf("expected final content to contain '已发送', got %q", result.Content)
	}
}

func TestEngine_SendHookNotTriggeredForNonMessagePayload(t *testing.T) {
	provider := &sequentialProvider{
		responses: []core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: "assistant",
					ToolCalls: []core.ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: core.ToolCallFunction{
							Name:      "search_memory",
							Arguments: `{"query":"test"}`,
						},
					}},
				},
			},
			{
				Message: core.ChatMessage{
					Role:    "assistant",
					Content: "搜索完成",
				},
			},
		},
	}

	tool := &staticTool{name: "search_memory", result: `{"results":[]}`}

	hookCalled := false

	engine := &Engine{
		MaxIterations: 5,
		Provider:      provider,
		ToolRegistry:  map[string]ToolExecutor{"search_memory": tool},
	}

	ctx := WithRunContext(context.Background(), RunContext{
		SessionID: "test-session",
		Owner:     "test-owner",
		SendHook: func(_ string) error {
			hookCalled = true
			return nil
		},
	})

	result := engine.runLoopWithResult(ctx, []ChatMessage{
		{Role: "system", Content: "you are a bot"},
		{Role: "user", Content: "search something"},
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if hookCalled {
		t.Fatal("SendHook should not be called for non-message payloads")
	}
}
