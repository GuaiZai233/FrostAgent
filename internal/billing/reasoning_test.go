package billing

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestEstimateMessageTokensIncludesChatReasoning(t *testing.T) {
	longReasoning := strings.Repeat("分析工具结果，继续推理。\n", 5000)
	for _, tc := range []struct {
		name      string
		role      core.MessageRole
		format    string
		content   json.RawMessage
		reasoning string
	}{
		{name: "long reasoning", role: core.RoleAssistant, format: "openai_chat_completions", content: mustReasoningJSON(t, longReasoning), reasoning: longReasoning},
		{name: "escaped text", role: core.RoleAssistant, format: "openai_chat_completions", content: json.RawMessage(`"\u5206\u6790\n\"tool\""`), reasoning: "分析\n\"tool\""},
		{name: "empty", role: core.RoleAssistant, format: "openai_chat_completions", content: json.RawMessage(`""`)},
		{name: "null", role: core.RoleAssistant, format: "openai_chat_completions", content: json.RawMessage(`null`)},
		{name: "other protocol", role: core.RoleAssistant, format: "openai_responses", content: mustReasoningJSON(t, longReasoning)},
		{name: "user state is not replayed", role: core.RoleUser, format: "openai_chat_completions", content: mustReasoningJSON(t, longReasoning)},
		{name: "tool state is not replayed", role: core.RoleTool, format: "openai_chat_completions", content: mustReasoningJSON(t, longReasoning)},
		{name: "invalid state", role: core.RoleAssistant, format: "openai_chat_completions", content: json.RawMessage(`{"invalid":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := core.ChatMessage{
				Role:    tc.role,
				Content: "visible answer",
				ToolCalls: []core.ToolCall{{
					ID: "synthetic-call", Type: "function",
					Function: core.ToolCallFunction{Name: "lookup", Arguments: "{}"},
				}},
			}
			baseline, err := EstimateMessageTokens(msg)
			if err != nil {
				t.Fatal(err)
			}
			msg.ProviderState = &core.ProviderState{Format: tc.format, Model: "test-model", Content: tc.content, Fingerprint: "synthetic-fingerprint"}
			got, err := EstimateMessageTokens(msg)
			if err != nil {
				t.Fatal(err)
			}
			if want := baseline + EstimateTextTokens(tc.reasoning); got != want {
				t.Fatalf("estimated tokens = %d, want %d (visible tokens %d)", got, want, baseline)
			}
		})
	}
}

func TestEstimateReservationAmountIncludesChatReasoning(t *testing.T) {
	reasoning := strings.Repeat("检查工具结果，再确定下一步。\n", 5000)
	msgs := []core.ChatMessage{
		{Role: core.RoleUser, Content: "run lookup"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "synthetic-call", Type: "function", Function: core.ToolCallFunction{Name: "lookup", Arguments: "{}"}}}},
		{Role: core.RoleTool, ToolCallID: "synthetic-call", Content: "synthetic-result"},
		{Role: core.RoleAssistant, Content: "done"},
	}
	tools := []core.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}}
	baseline, err := EstimateMessagesTokens(msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	// Both tool-call and final assistant reasoning are replayed in later requests.
	for _, i := range []int{1, 3} {
		msgs[i].ProviderState = &core.ProviderState{Format: "openai_chat_completions", Content: mustReasoningJSON(t, reasoning)}
	}
	wantPrompt := baseline + 2*EstimateTextTokens(reasoning)
	got, err := EstimateMessagesTokens(msgs, tools)
	if err != nil || got != wantPrompt {
		t.Fatalf("prompt estimate = %d, %v; want %d", got, err, wantPrompt)
	}
	price := ModelPrice{PromptPricePerMillion: 1_000_000, CompletionPricePerMillion: 1_000_000}
	amount, err := EstimateReservationAmount("test-model", msgs, tools, 100, 1.2, price)
	wantAmount := int64(math.Ceil(float64(wantPrompt)*1.2)) + 100
	if err != nil || amount != wantAmount {
		t.Fatalf("reservation = %d, %v; want %d", amount, err, wantAmount)
	}
}

func mustReasoningJSON(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
