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

type callbackTool struct {
	name   string
	onExec func() (string, error)
}

func (t *callbackTool) Name() string                   { return t.name }
func (t *callbackTool) Description() string             { return "callback tool" }
func (t *callbackTool) Parameters() map[string]any      { return nil }
func (t *callbackTool) Execute(_ string) (string, error) { return t.onExec() }

func TestEngine_MultiToolAbortedOnSessionReset(t *testing.T) {
	sm := NewSessionManager()
	sess := sm.GetOrCreate("test-session")
	initialEpoch := sess.Epoch()

	var tool1Calls int
	var tool2Calls int

	tool1 := &callbackTool{
		name: "tool_reset",
		onExec: func() (string, error) {
			tool1Calls++
			// Reset session while batch is executing
			_ = sess.ResetSession(nil)
			return `{"result":"reset done"}`, nil
		},
	}

	tool2 := &callbackTool{
		name: "tool_subsequent",
		onExec: func() (string, error) {
			tool2Calls++
			return `{"result":"subsequent done"}`, nil
		},
	}

	provider := &sequentialProvider{
		responses: []core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: "assistant",
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "tool_reset",
								Arguments: `{}`,
							},
						},
						{
							ID:   "call_2",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "tool_subsequent",
								Arguments: `{}`,
							},
						},
					},
				},
			},
		},
	}

	engine := &Engine{
		MaxIterations:  5,
		Provider:       provider,
		SessionManager: sm,
		ToolRegistry: map[string]ToolExecutor{
			"tool_reset":      tool1,
			"tool_subsequent": tool2,
		},
	}

	ctx := WithRunContext(context.Background(), RunContext{
		SessionID: "test-session",
		Owner:     "test-owner",
		Epoch:     initialEpoch,
	})

	result := engine.runLoopWithResult(ctx, []ChatMessage{
		{Role: "user", Content: "execute tools"},
	})

	if tool1Calls != 1 {
		t.Errorf("expected tool_reset to be called once, got %d", tool1Calls)
	}
	if tool2Calls != 0 {
		t.Errorf("expected tool_subsequent NOT to be called after session reset, got %d", tool2Calls)
	}
	if !result.Silent {
		t.Errorf("expected result to be silent after session reset abortion")
	}
}

func TestEngine_SendHookAbortedOnSessionReset(t *testing.T) {
	sm := NewSessionManager()
	sess := sm.GetOrCreate("test-session")
	initialEpoch := sess.Epoch()

	stickerPayload := `{"messages":[{"type":"image","path":"/data/sticker/abc.png","is_sticker":true}]}`

	tool := &callbackTool{
		name: "send_sticker",
		onExec: func() (string, error) {
			// Session is reset during tool execution
			_ = sess.ResetSession(nil)
			return stickerPayload, nil
		},
	}

	provider := &sequentialProvider{
		responses: []core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: "assistant",
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "send_sticker",
								Arguments: `{}`,
							},
						},
					},
				},
			},
		},
	}

	var hookCalled bool
	engine := &Engine{
		MaxIterations:  5,
		Provider:       provider,
		SessionManager: sm,
		ToolRegistry: map[string]ToolExecutor{
			"send_sticker": tool,
		},
	}

	ctx := WithRunContext(context.Background(), RunContext{
		SessionID: "test-session",
		Owner:     "test-owner",
		Epoch:     initialEpoch,
		SendHook: func(payload string) error {
			hookCalled = true
			return nil
		},
	})

	result := engine.runLoopWithResult(ctx, []ChatMessage{
		{Role: "user", Content: "send sticker"},
	})

	if hookCalled {
		t.Errorf("SendHook should NOT be called when session epoch is invalidated")
	}
	if !result.Silent {
		t.Errorf("expected result to be silent when session epoch is invalidated")
	}
}
