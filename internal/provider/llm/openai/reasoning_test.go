package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestChatReplaysReasoningAcrossToolAndFinalTurns(t *testing.T) {
	for _, reasoning := range []string{"synthetic reasoning\nwith exact whitespace ", ""} {
		t.Run(fmt.Sprintf("length=%d", len(reasoning)), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body struct {
					Messages []map[string]json.RawMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for _, msg := range body.Messages {
					var role string
					_ = json.Unmarshal(msg["role"], &role)
					if _, ok := msg["provider_state"]; ok {
						t.Error("internal provider state leaked into wire message")
					}
					if role != "assistant" {
						if _, ok := msg["reasoning_content"]; ok {
							t.Error("reasoning added to non-assistant message")
						}
						continue
					}
					var got *string
					if err := json.Unmarshal(msg["reasoning_content"], &got); err != nil || got == nil || *got != reasoning {
						t.Errorf("reasoning did not survive history: %s, %v", msg["reasoning_content"], err)
						http.Error(w, "missing reasoning_content", http.StatusBadRequest)
						return
					}
				}
				message := map[string]any{"role": "assistant", "content": "done", "reasoning_content": reasoning}
				if requests == 1 {
					message["content"] = nil
					message["tool_calls"] = []any{map[string]any{"id": "fixture-call", "type": "function", "function": map[string]any{"name": "lookup", "arguments": "{}"}}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
			}))
			defer server.Close()
			client := NewClient(server.URL, "")
			req := core.ChatRequest{Model: "test-model", Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "hello"}}, Tools: []core.Tool{{Name: "lookup"}}}
			for turn := 0; turn < 3; turn++ {
				response, err := client.Chat(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if response.Message.ProviderState == nil {
					t.Fatal("reasoning was discarded during response parsing")
				}
				// Exercise persisted history and a new transport on every turn.
				raw, err := json.Marshal(response.Message)
				if err != nil {
					t.Fatal(err)
				}
				var reloaded core.ChatMessage
				if err := json.Unmarshal(raw, &reloaded); err != nil {
					t.Fatal(err)
				}
				req.Messages = append(req.Messages, reloaded)
				if turn == 0 {
					req.Messages = append(req.Messages, core.ChatMessage{Role: core.RoleTool, ToolCallID: "fixture-call", Content: "synthetic-result"})
				} else {
					if reloaded.Content != "done" {
						t.Fatalf("visible answer = %v", reloaded.Content)
					}
					req.Messages = append(req.Messages, core.ChatMessage{Role: core.RoleUser, Content: "next turn"})
				}
				client = NewClient(server.URL, "")
			}
			if requests != 3 {
				t.Fatalf("requests = %d, want 3", requests)
			}
		})
	}
}

func TestChatReasoningReplayRejectsChangedMessageAndRoute(t *testing.T) {
	for _, change := range []string{"none", "content", "tool-call", "model", "endpoint", "url", "role", "protocol"} {
		t.Run(change, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"visible","reasoning_content":"synthetic-reasoning","tool_calls":[{"id":"fixture-call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`)
					return
				}
				var body struct {
					Messages []map[string]json.RawMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if len(body.Messages) != 1 {
					t.Errorf("message count = %d", len(body.Messages))
					return
				}
				_, replayed := body.Messages[0]["reasoning_content"]
				if replayed != (change == "none") {
					t.Errorf("reasoning replayed = %v after %s", replayed, change)
				}
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`)
			}))
			defer server.Close()
			client := NewClient(server.URL, "")
			client.EndpointID = "fixture-endpoint"
			req := core.ChatRequest{Model: "test-model", Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "hello"}}}
			first, err := client.Chat(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if first.Message.ProviderState == nil {
				t.Fatal("missing reasoning state")
			}
			req.Messages = []core.ChatMessage{first.Message}
			switch change {
			case "content":
				req.Messages[0].Content = "edited"
			case "tool-call":
				req.Messages[0].ToolCalls[0].Function.Arguments = `{"edited":true}`
			case "model":
				req.Model = "other-model"
			case "endpoint":
				client.EndpointID = "other-endpoint"
			case "url":
				client.RequestURL = server.URL + "/different"
			case "role":
				req.Messages[0].Role = core.RoleUser
			case "protocol":
				req.Messages[0].ProviderState.Format = "openai_responses"
			}
			result, err := client.Chat(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if result.Message.ProviderState != nil {
				t.Error("invented reasoning state for a response without reasoning")
			}
		})
	}
}

func TestChatDoesNotInventMissingReasoning(t *testing.T) {
	for _, field := range []string{"", `,"reasoning_content":null`} {
		t.Run(field, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []map[string]json.RawMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for _, msg := range body.Messages {
					if _, exists := msg["reasoning_content"]; exists {
						t.Error("invented reasoning_content for old or ordinary history")
					}
				}
				fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"done"%s}}]}`, field)
			}))
			defer server.Close()
			client := NewClient(server.URL, "")
			req := core.ChatRequest{Model: "test-model", Messages: []core.ChatMessage{{Role: core.RoleAssistant, Content: "old visible reply"}}}
			for turn := 0; turn < 2; turn++ {
				response, err := client.Chat(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if response.Message.ProviderState != nil {
					t.Fatal("missing reasoning became a non-nil state")
				}
				req.Messages = append(req.Messages, response.Message)
			}
		})
	}
}
