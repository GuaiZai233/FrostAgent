package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestResponsesAcceptsChatCompletionsHistory(t *testing.T) {
	for _, tc := range []struct {
		name, content, text string
	}{
		{"text", `"prior reply"`, "prior reply"},
		{"text-parts", `[{"type":"text","text":"prior "},{"type":"text","text":"reply"}]`, "prior reply"},
		{"empty-tool-call", `""`, ""},
		{"null-tool-call", `null`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := []any{
				map[string]any{"role": "system", "content": []any{map[string]any{"type": "input_text", "text": "synthetic-system"}}},
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "input_text", "text": "hello"},
					map[string]any{"type": "input_image", "image_url": "data:image/png;base64,aGVsbG8="},
				}},
			}
			if tc.text != "" {
				want = append(want, map[string]any{"role": "assistant", "content": tc.text})
			}
			want = append(want,
				map[string]any{"type": "function_call", "call_id": "fixture-call", "name": "lookup", "arguments": "{}"},
				map[string]any{"type": "function_call_output", "call_id": "fixture-call", "output": []any{map[string]any{"type": "input_text", "text": "synthetic-result"}}},
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "continue"}}},
			)
			var received atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/chat/completions":
					fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%s,"tool_calls":[{"id":"fixture-call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`, tc.content)
				case "/v1/responses":
					received.Store(true)
					var payload struct {
						Input []any `json:"input"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
						http.Error(w, "invalid JSON", http.StatusBadRequest)
						return
					}
					if !reflect.DeepEqual(payload.Input, want) {
						t.Errorf("Responses input = %#v, want %#v", payload.Input, want)
						http.Error(w, "invalid history role/content or tool continuation", http.StatusBadRequest)
						return
					}
					fmt.Fprint(w, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			req := core.ChatRequest{Model: "test-model", Messages: []core.ChatMessage{
				{Role: core.RoleSystem, Content: "synthetic-system"},
				{Role: core.RoleUser, Content: []core.ContentPart{{Type: "text", Text: "hello"}, {Type: "image_url", ImageURL: &core.ImageURL{URL: "data:image/png;base64,aGVsbG8="}}}},
			}}
			first, err := NewClient(server.URL, "", ChatCompletions, 0).Chat(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			// Persist the Chat Completions reply before resuming via Responses.
			raw, err := json.Marshal(first.Message)
			if err != nil {
				t.Fatal(err)
			}
			var reloaded core.ChatMessage
			if err := json.Unmarshal(raw, &reloaded); err != nil {
				t.Fatal(err)
			}
			req.Messages = append(req.Messages, reloaded,
				core.ChatMessage{Role: core.RoleTool, ToolCallID: "fixture-call", Content: []core.ContentPart{{Type: "text", Text: "synthetic-result"}}},
				core.ChatMessage{Role: core.RoleUser, Content: "continue"},
			)
			final, err := NewClient(server.URL, "", Responses, 0).Chat(context.Background(), req)
			if err != nil || final.Message.Content != "done" || !received.Load() {
				t.Fatalf("Responses continuation = %+v, %v; received=%v", final, err, received.Load())
			}
		})
	}
}

func TestResponsesRejectsAssistantImageHistory(t *testing.T) {
	var received atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(true)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "", Responses, 0).Chat(context.Background(), core.ChatRequest{
		Model: "test-model", Messages: []core.ChatMessage{{Role: core.RoleAssistant, Content: []core.ContentPart{
			{Type: "text", Text: "prior reply"},
			{Type: "image_url", ImageURL: &core.ImageURL{URL: "https://image.invalid/fixture.png"}},
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported Responses assistant content part") || received.Load() {
		t.Fatalf("assistant image history: err=%v, received=%v", err, received.Load())
	}
}
