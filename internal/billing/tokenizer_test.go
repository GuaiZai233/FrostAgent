package billing

import (
	"testing"

	"FrostAgent/internal/core"
)

func TestEstimateTextTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		minTokens int
		maxTokens int
	}{
		{
			name:      "empty string",
			text:      "",
			minTokens: 0,
			maxTokens: 0,
		},
		{
			name:      "single english word",
			text:      "hello",
			minTokens: 1,
			maxTokens: 3,
		},
		{
			name:      "english sentence",
			text:      "The quick brown fox jumps over the lazy dog.",
			minTokens: 8,
			maxTokens: 16,
		},
		{
			name:      "chinese sentence",
			text:      "你好，今天天气真不错！",
			minTokens: 10,
			maxTokens: 20,
		},
		{
			name:      "mixed sentence",
			text:      "调用 API 获取当前用户的 balance_minor 并返回结果。",
			minTokens: 12,
			maxTokens: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTextTokens(tt.text)
			if got < tt.minTokens || got > tt.maxTokens {
				t.Errorf("EstimateTextTokens(%q) = %d; want between [%d, %d]",
					tt.text, got, tt.minTokens, tt.maxTokens)
			}
		})
	}
}

func TestEstimateMessageTokens(t *testing.T) {
	// String content
	msg1 := core.ChatMessage{
		Role:    core.RoleUser,
		Content: "查询余额",
	}
	count1, err := EstimateMessageTokens(msg1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count1 < MessageOverheadTokens+4 {
		t.Errorf("expected count >= %d, got %d", MessageOverheadTokens+4, count1)
	}

	// Content parts (multimodal)
	msg2 := core.ChatMessage{
		Role: core.RoleUser,
		Content: []core.ContentPart{
			{Type: "text", Text: "这张图里有什么？"},
			{Type: "image_url", ImageURL: &core.ImageURL{URL: "https://example.com/pic.jpg"}},
		},
	}
	count2, err := EstimateMessageTokens(msg2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count2 < DefaultImageTokens {
		t.Errorf("expected count >= %d, got %d", DefaultImageTokens, count2)
	}

	// Tool call message
	msg3 := core.ChatMessage{
		Role:    core.RoleAssistant,
		Content: nil,
		ToolCalls: []core.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: core.ToolCallFunction{
					Name:      "get_weather",
					Arguments: `{"city": "Beijing"}`,
				},
			},
		},
	}
	count3, err := EstimateMessageTokens(msg3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count3 <= MessageOverheadTokens {
		t.Errorf("expected tool call overhead, got %d", count3)
	}

	// Fail closed on unsupported type
	msgInvalid := core.ChatMessage{
		Role:    core.RoleUser,
		Content: 12345, // invalid int type
	}
	_, err = EstimateMessageTokens(msgInvalid)
	if err == nil {
		t.Fatalf("expected error on invalid content type, got nil")
	}
}

func TestEstimateReservationAmount(t *testing.T) {
	msgs := []core.ChatMessage{
		{Role: core.RoleSystem, Content: "You are a helpful assistant."},
		{Role: core.RoleUser, Content: "你好，请帮我写一段 Go 代码。"},
	}
	tools := []core.Tool{
		{
			Name:        "get_time",
			Description: "Get the current time",
			Parameters:  map[string]any{"type": "object"},
		},
	}

	amount, err := EstimateReservationAmount("deepseek-chat", msgs, tools, 2048, 1.2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amount <= 0 {
		t.Errorf("expected positive reservation amount, got %d", amount)
	}
}
