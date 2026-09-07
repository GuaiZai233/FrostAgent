package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
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

func TestClient_Chat_RedactsExecuteCommandInResponseLog(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	sentinelSecret := "SENTINEL_SECRET_TOKEN_COMMAND_9999"
	secretCommand := "curl -H 'Authorization: Bearer " + sentinelSecret + "' https://api.internal/exec"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":   "call_cmd_1",
						"type": "function",
						"function": map[string]any{
							"name": "execute_command",
							"arguments": fmt.Sprintf(`{"command":%q,"cwd":"/sandbox","timeout":30}`, secretCommand),
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	resp, err := client.Chat(context.Background(), core.ChatRequest{
		Model: "test-model",
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: "run the command"},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// 1. Verify caller gets the unredacted command for execution
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.Message.ToolCalls))
	}
	if !strings.Contains(resp.Message.ToolCalls[0].Function.Arguments, sentinelSecret) {
		t.Fatalf("expected unredacted argument returned to caller, got: %s", resp.Message.ToolCalls[0].Function.Arguments)
	}

	// 2. Verify log buffer NEVER contains the sentinel secret
	snapshot := logs.Snapshot()
	var foundLLMResponse bool
	for _, entry := range snapshot {
		if strings.Contains(entry.Content, sentinelSecret) {
			t.Fatalf("log entry (%s) leaked sentinel secret: %s", entry.Category, entry.Content)
		}
		if entry.Category == logs.LLM_RESPONSE && strings.Contains(entry.Content, "[REDACTED command:") {
			foundLLMResponse = true
		}
	}
	if !foundLLMResponse {
		t.Fatalf("expected LLM_RESPONSE entry containing [REDACTED command: in logs snapshot")
	}
}

func TestClient_Chat_RedactsExecuteCommandInRequestLog(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	sentinelCommandSecret := "SENTINEL_REQ_COMMAND_SECRET_1111"
	sentinelStdoutSecret := "SENTINEL_STDOUT_SECRET_2222"
	sentinelStderrSecret := "SENTINEL_STDERR_SECRET_3333"

	var wireBodyReceived string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		wireBodyReceived = string(b)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": "command finished",
				},
			}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	_, err := client.Chat(context.Background(), core.ChatRequest{
		Model: "test-model",
		Messages: []core.ChatMessage{
			{
				Role: core.RoleAssistant,
				ToolCalls: []core.ToolCall{{
					ID:   "call_cmd_42",
					Type: "function",
					Function: core.ToolCallFunction{
						Name:      "execute_command",
						Arguments: fmt.Sprintf(`{"command":"echo %s","cwd":"/sandbox","timeout":30}`, sentinelCommandSecret),
					},
				}},
			},
			{
				Role:       core.RoleTool,
				ToolCallID: "call_cmd_42",
				Content: fmt.Sprintf(
					`{"stdout":"output: %s","stderr":"error: %s","exit_code":0,"timed_out":false,"duration_ms":50}`,
					sentinelStdoutSecret, sentinelStderrSecret,
				),
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// 1. Verify wire payload to upstream LLM contains original unredacted contents
	if !strings.Contains(wireBodyReceived, sentinelCommandSecret) {
		t.Fatalf("upstream request should receive unredacted command, got: %s", wireBodyReceived)
	}
	if !strings.Contains(wireBodyReceived, sentinelStdoutSecret) {
		t.Fatalf("upstream request should receive unredacted stdout, got: %s", wireBodyReceived)
	}
	if !strings.Contains(wireBodyReceived, sentinelStderrSecret) {
		t.Fatalf("upstream request should receive unredacted stderr, got: %s", wireBodyReceived)
	}

	// 2. Verify log buffer NEVER contains any of the sentinel secrets
	snapshot := logs.Snapshot()
	var foundLLMRequestRedacted bool
	for _, entry := range snapshot {
		if strings.Contains(entry.Content, sentinelCommandSecret) {
			t.Fatalf("log entry (%s) leaked command secret: %s", entry.Category, entry.Content)
		}
		if strings.Contains(entry.Content, sentinelStdoutSecret) {
			t.Fatalf("log entry (%s) leaked stdout secret: %s", entry.Category, entry.Content)
		}
		if strings.Contains(entry.Content, sentinelStderrSecret) {
			t.Fatalf("log entry (%s) leaked stderr secret: %s", entry.Category, entry.Content)
		}
		if entry.Category == logs.LLM_REQUEST &&
			strings.Contains(entry.Content, "[REDACTED command:") &&
			strings.Contains(entry.Content, "[REDACTED stdout:") {
			foundLLMRequestRedacted = true
		}
	}
	if !foundLLMRequestRedacted {
		t.Fatalf("expected LLM_REQUEST entry containing redacted command and stdout")
	}
}

func TestClient_Chat_PreservesNonExecuteCommandLogging(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	normalToolArg := "hello-world-message"
	normalToolResult := "message-sent-confirmation"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":   "call_msg_1",
						"type": "function",
						"function": map[string]any{
							"name": "send_msg",
							"arguments": fmt.Sprintf(`{"message":%q}`, normalToolArg),
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	_, err := client.Chat(context.Background(), core.ChatRequest{
		Model: "test-model",
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: "send message"},
			{
				Role:       core.RoleTool,
				ToolCallID: "call_msg_0",
				Content:    normalToolResult,
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	snapshot := logs.Snapshot()
	var foundNormalArgInResponse, foundNormalResultInRequest bool
	for _, entry := range snapshot {
		if entry.Category == logs.LLM_RESPONSE && strings.Contains(entry.Content, normalToolArg) {
			foundNormalArgInResponse = true
		}
		if entry.Category == logs.LLM_REQUEST && strings.Contains(entry.Content, normalToolResult) {
			foundNormalResultInRequest = true
		}
	}
	if !foundNormalArgInResponse {
		t.Fatalf("expected normal tool call arguments to be logged in LLM_RESPONSE")
	}
	if !foundNormalResultInRequest {
		t.Fatalf("expected normal tool result to be logged in LLM_REQUEST")
	}
}
