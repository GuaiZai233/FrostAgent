package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/state"
)

func TestChatToolRequestsFillMissingReasoning(t *testing.T) {
	for _, change := range []string{"missing", "null", "content", "tools", "model", "endpoint", "url", "protocol", "none", "without-tools"} {
		t.Run(change, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []map[string]json.RawMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for _, message := range body.Messages {
					var role string
					json.Unmarshal(message["role"], &role)
					raw, exists := message["reasoning_content"]
					if role != "assistant" || change == "without-tools" {
						if exists {
							t.Errorf("unexpected reasoning on %s: %s", role, raw)
						}
						continue
					}
					want := ""
					if change == "none" {
						want = "synthetic-reasoning"
					}
					var got *string
					if err := json.Unmarshal(raw, &got); err != nil || got == nil || *got != want {
						t.Errorf("reasoning=%s want=%q error=%v", raw, want, err)
						http.Error(w, "missing reasoning_content", http.StatusBadRequest)
						return
					}
					if _, exists := message["provider_state"]; exists {
						t.Error("internal state leaked")
					}
				}
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`)
			}))
			defer server.Close()
			client := NewClient(server.URL, "")
			client.EndpointID = "synthetic-endpoint"
			message := core.ChatMessage{Role: core.RoleAssistant, Content: "visible", ToolCalls: []core.ToolCall{{ID: "synthetic-call", Type: "function", Function: core.ToolCallFunction{Name: "lookup", Arguments: "{}"}}}}
			state.Remember(&message, reasoningStateFormat, "synthetic-model", "synthetic-reasoning", state.Origin{EndpointID: client.EndpointID, URL: server.URL + "/chat/completions"})
			req := core.ChatRequest{Model: "synthetic-model", Tools: []core.Tool{{Name: "lookup"}}}
			switch change {
			case "missing", "without-tools":
				message.ProviderState = nil
			case "null":
				message.ProviderState.Content = json.RawMessage("null")
			case "content":
				message.Content = "changed"
			case "tools":
				message.ToolCalls[0].Function.Arguments = `{"changed":true}`
			case "model":
				req.Model = "other-model"
			case "endpoint":
				client.EndpointID = "other-endpoint"
			case "url":
				client.RequestURL = server.URL + "/other"
			case "protocol":
				message.ProviderState.Format = "openai_responses"
			}
			if change == "without-tools" {
				req.Tools = nil
			}
			req.Messages = []core.ChatMessage{{Role: core.RoleSystem, Content: "system"}, {Role: core.RoleUser, Content: "question"}, message, {Role: core.RoleTool, ToolCallID: "synthetic-call", Content: "result"}}
			if _, err := client.Chat(context.Background(), req); err != nil {
				t.Fatal(err)
			}
		})
	}
}
