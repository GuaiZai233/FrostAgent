package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
)

func TestEnginePreservesNativeToolContinuation(t *testing.T) {
	for _, tc := range []struct {
		format               api.Format
		first, final, marker string
	}{
		{api.ChatCompletions, `{"choices":[{"message":{"role":"assistant","content":null,"reasoning_content":"synthetic-reasoning","tool_calls":[{"id":"fixture-call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`, `{"choices":[{"message":{"role":"assistant","content":"done","reasoning_content":"synthetic-final-reasoning"}}]}`, "synthetic-reasoning"},
		{api.Responses, `{"output":[{"type":"reasoning","summary":[],"encrypted_content":"opaque-reasoning"},{"type":"function_call","call_id":"fixture-call","name":"lookup","arguments":"{}"}]}`, `{"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`, "opaque-reasoning"},
		{api.Anthropic, `{"content":[{"type":"thinking","thinking":"hidden","signature":"opaque-signature"},{"type":"tool_use","id":"fixture-call","name":"lookup","input":{}}]}`, `{"content":[{"type":"text","text":"done"}]}`, "opaque-signature"},
		{api.Gemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}},"thoughtSignature":"opaque-signature"}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"done"}]}}]}`, "opaque-signature"},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					fmt.Fprint(w, tc.first)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), tc.marker) || !strings.Contains(string(raw), "synthetic-tool-result") {
					t.Errorf("engine dropped continuation: %s", raw)
				}
				fmt.Fprint(w, tc.final)
			}))
			defer server.Close()
			engine := &Engine{MaxIterations: 3, ModelName: "test-model", Provider: api.NewClient(server.URL, "", tc.format, 0), ToolRegistry: map[string]ToolExecutor{"lookup": &staticTool{name: "lookup", result: "synthetic-tool-result"}}}
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{})
			if result.Error != nil || result.Content != "done" || requests != 2 {
				t.Fatalf("engine result: %+v, requests=%d", result, requests)
			}
		})
	}
}

func TestSessionPreservesChatReasoningAndToolResult(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":null,"reasoning_content":"synthetic-reasoning","tool_calls":[{"id":"fixture-call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`)
			return
		}
		var body struct {
			Messages []struct {
				Role             string  `json:"role"`
				ReasoningContent *string `json:"reasoning_content"`
				ToolCallID       string  `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) != 3 {
			t.Errorf("history length = %d", len(body.Messages))
			return
		}
		if got := body.Messages[1].ReasoningContent; got == nil || *got != "synthetic-reasoning" {
			t.Error("session conversion lost assistant reasoning")
		}
		if got := body.Messages[2].ToolCallID; got != "fixture-call" {
			t.Errorf("session conversion lost tool result id: %q", got)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`)
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "", api.ChatCompletions, 0)
	first, err := client.Chat(context.Background(), core.ChatRequest{Model: "test-model", Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	session := &SessionContext{}
	session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "hello"})
	session.AddMessage(first.Message)
	session.AddMessage(core.ChatMessage{Role: core.RoleTool, ToolCallID: "fixture-call", Content: "synthetic-result"})
	raw, err := json.Marshal(session.History)
	if err != nil {
		t.Fatal(err)
	}
	var restored SessionContext
	if err := json.Unmarshal(raw, &restored.History); err != nil {
		t.Fatal(err)
	}
	result, err := client.Chat(context.Background(), core.ChatRequest{Model: "test-model", Messages: restored.Messages()})
	if err != nil || result.Message.Content != "done" || requests != 2 {
		t.Fatalf("restored continuation failed: %+v, %v; requests=%d", result, err, requests)
	}
}
