package state

import (
	"encoding/json"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestRememberBindsPersistedStateToMessageAndRoute(t *testing.T) {
	for _, change := range []string{"none", "content", "tool-call", "endpoint", "url", "missing-url", "model", "protocol", "missing-state"} {
		t.Run(change, func(t *testing.T) {
			origin := Origin{EndpointID: "synthetic-endpoint", URL: "https://example.invalid/chat/completions"}
			format, model := "openai_chat_completions", "test-model"
			msg := core.ChatMessage{
				Role: core.RoleAssistant, Content: "visible answer",
				ToolCalls: []core.ToolCall{{ID: "synthetic-call", Type: "function", Function: core.ToolCallFunction{Name: "lookup", Arguments: "{}"}}},
			}
			Remember(&msg, format, model, "synthetic reasoning", origin)
			if msg.ProviderState == nil || msg.Content != "visible answer" {
				t.Fatal("reasoning was not stored separately from visible content")
			}
			// A persisted message must retain both its payload and replay binding.
			raw, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			var restored core.ChatMessage
			if err := json.Unmarshal(raw, &restored); err != nil {
				t.Fatal(err)
			}
			var reasoning string
			if err := json.Unmarshal(restored.ProviderState.Content, &reasoning); err != nil || reasoning != "synthetic reasoning" {
				t.Fatalf("reasoning lost on restore: %q, %v", reasoning, err)
			}
			switch change {
			case "content":
				restored.Content = "edited answer"
			case "tool-call":
				restored.ToolCalls[0].Function.Arguments = `{"edited":true}`
			case "endpoint":
				origin.EndpointID = "other-endpoint"
			case "url":
				origin.URL = "https://other.example.invalid/chat/completions"
			case "missing-url":
				origin.URL = ""
			case "model":
				model = "other-model"
			case "protocol":
				format = "openai_responses"
			case "missing-state":
				restored.ProviderState = nil
			}
			if got := Matches(restored, format, model, origin); got != (change == "none") {
				t.Fatalf("state matches after %s = %v", change, got)
			}
		})
	}
}

func TestRememberSkipsUnreplayableState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		origin  Origin
		payload any
	}{
		{name: "missing origin", payload: "synthetic reasoning"},
		{name: "unencodable payload", origin: Origin{URL: "https://example.invalid/chat/completions"}, payload: make(chan int)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := core.ChatMessage{Role: core.RoleAssistant, Content: "visible answer"}
			Remember(&msg, "openai_chat_completions", "test-model", tc.payload, tc.origin)
			if msg.ProviderState != nil {
				t.Fatal("created state without a replayable payload and origin")
			}
		})
	}
}
