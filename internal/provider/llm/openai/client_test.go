package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"FrostAgent/internal/core"
)

func TestClient_Chat_ParsesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello! How can I assist you?",
					},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     15,
				"completion_tokens": 8,
				"total_tokens":      23,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	resp, err := client.Chat(context.Background(), core.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if resp.Usage == nil {
		t.Fatalf("expected non-nil Usage in response")
	}
	if resp.Usage.PromptTokens != 15 || resp.Usage.CompletionTokens != 8 || resp.Usage.TotalTokens != 23 {
		t.Errorf("unexpected Usage values: %+v", resp.Usage)
	}
}

func TestClient_Chat_NilUsageGraceful(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "No usage reported",
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	resp, err := client.Chat(context.Background(), core.ChatRequest{
		Model: "deepseek-chat",
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if resp.Usage != nil {
		t.Errorf("expected nil Usage, got %+v", resp.Usage)
	}
}

func TestClient_Chat_EmptyChoicesFallsBackToStaySilent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{},
			"usage": map[string]any{
				"prompt_tokens":     1957,
				"completion_tokens": 0,
				"total_tokens":      1957,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	resp, err := client.Chat(context.Background(), core.ChatRequest{
		Model: "gemini-3.7-flash",
		Tools: []core.Tool{{
			Name: staySilentFallbackToolName,
		}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.Message.Role != core.RoleAssistant {
		t.Fatalf("expected assistant fallback, got role=%q", resp.Message.Role)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one fallback tool call, got %+v", resp.Message.ToolCalls)
	}
	toolCall := resp.Message.ToolCalls[0]
	if toolCall.ID != staySilentFallbackToolCallID || toolCall.Type != "function" {
		t.Fatalf("unexpected fallback tool call metadata: %+v", toolCall)
	}
	if toolCall.Function.Name != staySilentFallbackToolName || toolCall.Function.Arguments != "{}" {
		t.Fatalf("unexpected fallback tool call function: %+v", toolCall.Function)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 1957 || resp.Usage.CompletionTokens != 0 || resp.Usage.TotalTokens != 1957 {
		t.Fatalf("unexpected fallback usage: %+v", resp.Usage)
	}
}

func TestClient_Chat_EmptyChoicesWithoutStaySilentReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	resp, err := client.Chat(context.Background(), core.ChatRequest{
		Model: "background-model",
	})
	if err == nil || !strings.Contains(err.Error(), "no choices in response") {
		t.Fatalf("expected no choices error, got response=%+v err=%v", resp, err)
	}
}

func TestClient_Chat_PreservesConsecutiveUserRoles(t *testing.T) {
	var received chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": "收到",
				},
			}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	_, err := client.Chat(context.Background(), core.ChatRequest{
		Model: "test-model",
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: "message A"},
			{Role: core.RoleUser, Content: "message B"},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if len(received.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(received.Messages))
	}
	for i, want := range []string{"message A", "message B"} {
		if received.Messages[i].Role != "user" || received.Messages[i].Content != want {
			t.Fatalf("message[%d] expected user %q, got role=%s content=%v", i, want, received.Messages[i].Role, received.Messages[i].Content)
		}
	}
}
