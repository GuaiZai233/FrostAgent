package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/openai"
)

func TestEndpointPaths(t *testing.T) {
	for _, tc := range []struct {
		format                       Format
		base, operation, model, want string
	}{
		{Legacy, "https://relay.invalid", "chat/completions", "", "https://relay.invalid/chat/completions"},
		{ChatCompletions, "https://relay.invalid/", "chat/completions", "", "https://relay.invalid/v1/chat/completions"},
		{ChatCompletions, "https://relay.invalid/proxy/v1/", "chat/completions", "", "https://relay.invalid/proxy/v1/chat/completions"},
		{ChatCompletions, "https://relay.invalid/proxy%2Froute", "chat/completions", "", "https://relay.invalid/proxy%2Froute/v1/chat/completions"},
		{Responses, "https://relay.invalid/proxy", "chat/completions", "", "https://relay.invalid/proxy/v1/responses"},
		{Anthropic, "https://relay.invalid/v1", "chat/completions", "", "https://relay.invalid/v1/messages"},
		{Gemini, "https://relay.invalid", "chat/completions", "models/test-model", "https://relay.invalid/v1beta/models/test-model:generateContent"},
		{Gemini, "https://relay.invalid/proxy/v1beta/", "models", "", "https://relay.invalid/proxy/v1beta/models"},
		{Custom, "https://relay.invalid/exact/?route=chat%2Ftest", "chat/completions", "", "https://relay.invalid/exact/?route=chat%2Ftest"},
		{Custom, "https://relay.invalid/embed?api-version=preview", "embeddings", "", "https://relay.invalid/embed?api-version=preview"},
		{Responses, "https://relay.invalid", "embeddings", "", "https://relay.invalid/v1/embeddings"},
	} {
		t.Run(string(tc.format)+tc.base+tc.operation, func(t *testing.T) {
			got, err := tc.format.URL(tc.base, tc.operation, tc.model)
			if err != nil || got != tc.want {
				t.Fatalf("URL = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		format           Format
		operation, model string
	}{
		{Custom, "models", ""}, {Anthropic, "embeddings", ""}, {Gemini, "embeddings", ""}, {Gemini, "chat/completions", "../escape"}, {Format("unknown"), "chat/completions", ""},
	} {
		if _, err := tc.format.URL("https://relay.invalid", tc.operation, tc.model); err == nil {
			t.Fatalf("accepted invalid route: %+v", tc)
		}
	}
}

func TestNativeToolContinuations(t *testing.T) {
	fixtures := []struct {
		format                        Format
		path, first, final, signature string
	}{
		{Responses, "/v1/responses", `{"status":"completed","output":[{"type":"reasoning","id":"reason","summary":[],"encrypted_content":"opaque-reason"},{"type":"function_call","call_id":"call-a","name":"lookup","arguments":"{\"query\":\"fixture\"}"},{"type":"function_call","call_id":"call-b","name":"lookup","arguments":"{}"}],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":2}}}`, `{"status":"completed","output":[{"type":"message","id":"answer","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":10,"output_tokens":3}}`, "opaque-reason"},
		{Anthropic, "/v1/messages", `{"type":"message","content":[{"type":"thinking","thinking":"hidden","signature":"opaque-signature"},{"type":"tool_use","id":"call-a","name":"lookup","input":{"query":"fixture"}},{"type":"tool_use","id":"call-b","name":"lookup","input":{}}],"usage":{"input_tokens":6,"cache_read_input_tokens":2,"cache_creation_input_tokens":2,"output_tokens":3}}`, `{"type":"message","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"output_tokens":3}}`, "opaque-signature"},
		{Gemini, "/v1beta/models/test-model:generateContent", `{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"hidden","thoughtSignature":"opaque-thought"},{"functionCall":{"name":"lookup","args":{"query":"fixture"}},"thoughtSignature":"opaque-call-signature"},{"functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":1,"totalTokenCount":13,"cachedContentTokenCount":2}}`, `{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3}}`, "opaque-call-signature"},
	}
	for _, tc := range fixtures {
		t.Run(string(tc.format), func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if r.URL.Path != tc.path {
					t.Errorf("path = %s", r.URL.Path)
				}
				if r.Method != "POST" {
					t.Errorf("method = %s", r.Method)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing JSON header")
				}
				switch tc.format {
				case Anthropic:
					if r.Header.Get("x-api-key") != "synthetic-key" || r.Header.Get("anthropic-version") == "" || r.Header.Get("Authorization") != "" {
						t.Error("wrong Anthropic auth")
					}
				case Gemini:
					if r.Header.Get("x-goog-api-key") != "synthetic-key" || r.Header.Get("Authorization") != "" {
						t.Error("wrong Gemini auth")
					}
				default:
					if r.Header.Get("Authorization") != "Bearer synthetic-key" {
						t.Error("missing bearer")
					}
				}
				raw, _ := io.ReadAll(r.Body)
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
				}
				if count == 1 {
					if !strings.Contains(string(raw), "aGVsbG8=") || !strings.Contains(string(raw), "lookup") || !strings.Contains(string(raw), "synthetic-system") {
						t.Errorf("lost input: %s", raw)
					}
					switch tc.format {
					case Anthropic:
						if body["max_tokens"] != float64(1024) {
							t.Error("missing max_tokens")
						}
					case Responses:
						if body["store"] != false || body["messages"] != nil {
							t.Error("wrong Responses shape")
						}
					case Gemini:
						if body["model"] != nil || body["messages"] != nil {
							t.Error("wrong Gemini shape")
						}
					}
					fmt.Fprint(w, tc.first)
				} else {
					for _, value := range []string{tc.signature, "synthetic-result-a", "synthetic-result-b"} {
						if !strings.Contains(string(raw), value) {
							t.Errorf("continuation lost %s: %s", value, raw)
						}
					}
					if tc.format == Anthropic {
						messages := body["messages"].([]any)
						last := messages[len(messages)-1].(map[string]any)
						if len(last["content"].([]any)) != 2 {
							t.Error("parallel tool results were not merged")
						}
					}
					fmt.Fprint(w, tc.final)
				}
			}))
			defer server.Close()
			client := NewClient(server.URL, "synthetic-key", tc.format, 0)
			observations := 0
			client.ObserveUsage = func(usage *core.Usage) {
				observations++
				if usage == nil || usage.TotalTokens != 13 || usage.PromptTokens != 10 || usage.CompletionTokens != 3 {
					t.Errorf("usage: %+v", usage)
				}
			}
			req := core.ChatRequest{Model: "test-model", MaxTokens: 1024, Messages: []core.ChatMessage{{Role: core.RoleSystem, Content: "synthetic-system"}, {Role: core.RoleUser, Content: []core.ContentPart{{Type: "text", Text: "hello"}, {Type: "image_url", ImageURL: &core.ImageURL{URL: "data:image/png;base64,aGVsbG8="}}}}}, Tools: []core.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}}
			first, err := client.Chat(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Message.ToolCalls) != 2 || first.Message.Content != "" || first.Message.ProviderState == nil {
				t.Fatalf("tool response: %+v", first.Message)
			}
			// Persist and reload the internal history, matching session serialization.
			raw, _ := json.Marshal(first.Message)
			var reloaded core.ChatMessage
			if err := json.Unmarshal(raw, &reloaded); err != nil {
				t.Fatal(err)
			}
			req.Messages = append(req.Messages, reloaded)
			for i, call := range reloaded.ToolCalls {
				req.Messages = append(req.Messages, core.ChatMessage{Role: core.RoleTool, ToolCallID: call.ID, Content: fmt.Sprintf("synthetic-result-%c", 'a'+i)})
			}
			final, err := client.Chat(context.Background(), req)
			if err != nil || final.Message.Content != "done" || observations != 2 {
				t.Fatalf("continuation: %+v, %v; observations=%d", final, err, observations)
			}
			reloaded.Content = "edited"
			if stateMatches(reloaded, tc.format, req.Model) {
				t.Error("replayed stale protocol state")
			}
			if stateMatches(first.Message, tc.format, "other-model") {
				t.Error("replayed state to another model")
			}
		})
	}
}

func TestCustomChatUsesExactURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/exact/?route=a%2Fb" {
			t.Errorf("request URI = %s", r.RequestURI)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`)
	}))
	defer server.Close()
	result, err := NewClient(server.URL+"/exact/?route=a%2Fb", "", Custom, 0).Chat(context.Background(), core.ChatRequest{Model: "test-model"})
	if err != nil || result.Message.Content != "done" {
		t.Fatalf("custom: %+v, %v", result, err)
	}
}

func TestNativeFailuresPreserveHTTPErrorAndUsage(t *testing.T) {
	for _, format := range []Format{Responses, Anthropic, Gemini} {
		t.Run(string(format), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(429)
				fmt.Fprint(w, "synthetic rate limit")
			}))
			defer server.Close()
			client := NewClient(server.URL, "", format, 0)
			_, err := client.Chat(context.Background(), core.ChatRequest{Model: "test-model"})
			var httpErr *openai.HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != 429 || httpErr.Body != "synthetic rate limit" {
				t.Fatalf("HTTP error: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		format Format
		body   string
	}{
		{Responses, `{"status":"incomplete","usage":{"input_tokens":10,"output_tokens":3}}`},
		{Anthropic, `{"type":"message","content":[],"usage":{"input_tokens":10,"output_tokens":3}}`},
		{Gemini, `{"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3}}`},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
		client := NewClient(server.URL, "", tc.format, 0)
		observed := false
		client.ObserveUsage = func(u *core.Usage) { observed = u != nil && u.TotalTokens == 13 }
		if _, err := client.Chat(context.Background(), core.ChatRequest{Model: "test-model"}); err == nil || !observed {
			t.Errorf("missing failure or usage for %s: %v", tc.format, err)
		}
		server.Close()
	}
}

func TestNativeToolArgumentNumbersSurviveReplay(t *testing.T) {
	for _, tc := range []struct {
		format Format
		raw    string
	}{
		{Responses, `{"output":[{"type":"function_call","call_id":"fixture","name":"lookup","arguments":"{\"value\":9007199254740993}"}]}`},
		{Anthropic, `{"content":[{"type":"tool_use","id":"fixture","name":"lookup","input":{"value":9007199254740993}}]}`},
		{Gemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"value":9007199254740993}}}]}}]}`},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			var response *core.ChatResponse
			var err error
			var payload any
			switch tc.format {
			case Responses:
				response, err = decodeResponses([]byte(tc.raw), "fixture-model")
			case Anthropic:
				response, err = decodeAnthropic([]byte(tc.raw), "fixture-model")
			case Gemini:
				response, err = decodeGemini([]byte(tc.raw), "fixture-model")
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(response.Message.ToolCalls[0].Function.Arguments, "9007199254740993") {
				t.Fatal("response rounded tool arguments")
			}
			req := core.ChatRequest{Model: "fixture-model", Messages: []core.ChatMessage{response.Message}}
			switch tc.format {
			case Responses:
				payload, err = responsesRequest(req)
			case Anthropic:
				payload, err = anthropicRequest(req)
			case Gemini:
				payload, err = geminiRequest(req)
			}
			raw, marshalErr := json.Marshal(payload)
			if err != nil || marshalErr != nil || !strings.Contains(string(raw), "9007199254740993") {
				t.Fatalf("replay rounded arguments: %s, %v %v", raw, err, marshalErr)
			}
		})
	}
}
