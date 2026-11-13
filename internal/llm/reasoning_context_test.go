package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
)

func TestEngineRejectsReasoningBeyondContextLimit(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests > 1 {
			t.Error("provider called after reasoning exceeded the context limit")
			http.Error(w, "unexpected call", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "content": nil,
				"reasoning_content": strings.Repeat("思", MaxContextTokens),
				"tool_calls": []any{map[string]any{
					"id": "synthetic-call", "type": "function",
					"function": map[string]any{"name": "lookup", "arguments": "{}"},
				}},
			}}},
		})
	}))
	defer server.Close()
	engine := &Engine{
		MaxIterations: 3, ModelName: "test-model",
		Provider:     api.NewClient(server.URL, "", api.ChatCompletions, 0),
		ToolRegistry: map[string]ToolExecutor{"lookup": &staticTool{name: "lookup", result: "synthetic-result"}},
	}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "run lookup"}}, RunContext{})
	if result.Error == nil || !strings.Contains(result.Error.Error(), "context tokens") {
		t.Fatalf("expected context limit rejection, got %v", result.Error)
	}
	if requests != 1 {
		t.Fatalf("provider calls = %d, want 1", requests)
	}
}
