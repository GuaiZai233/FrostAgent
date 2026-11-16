package llm

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestProactiveReplyCompletesAfterConfirmedDelivery(t *testing.T) {
	for _, maxIterations := range []int{1, 3} {
		name := "last iteration"
		if maxIterations > 1 {
			name = "remaining iteration budget"
		}
		t.Run(name, func(t *testing.T) {
			const args = `{"messages":[{"type":"mention_specific","message_id":"fixture-message"},{"type":"plain","text":"confirmed reply"}]}`
			const payload = `{"suppress_auto_mention":true,"messages":[{"type":"quote","message_id":"fixture-message"},{"type":"plain","text":"confirmed reply"}]}`
			tool := &proactiveMessageTool{structuredResultTool: structuredResultTool{
				staticTool: staticTool{name: "send_message"},
				output:     core.ToolExecutionResult{Content: "prepared", MessagePayload: payload},
			}}
			provider := &sequentialProvider{responses: []core.ChatResponse{
				{
					Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
						ID: "reply", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: args},
					}}},
					Usage: &core.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
				},
				{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "（已回复）"}},
			}}
			engine := &Engine{MaxIterations: maxIterations, Provider: provider, ToolRegistry: map[string]ToolExecutor{"send_message": tool}}
			deliveries := 0
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "fixture input"}}, RunContext{
				Proactive: true,
				SendHook: func(got string) error {
					deliveries++
					if got != payload {
						t.Errorf("delivery payload changed: %s", got)
					}
					return nil
				},
			})
			if result.Error != nil || result.Silent || result.Content != "" || result.Usage.TotalTokens != 10 {
				t.Fatalf("confirmed reply did not finish successfully: %+v", result)
			}
			if deliveries != 1 || tool.calls != 1 || provider.callCount != 1 {
				t.Fatalf("unexpected continuation: deliveries=%d tools=%d model=%d", deliveries, tool.calls, provider.callCount)
			}
		})
	}
}
