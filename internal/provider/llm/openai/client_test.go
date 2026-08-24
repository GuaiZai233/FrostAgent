package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
