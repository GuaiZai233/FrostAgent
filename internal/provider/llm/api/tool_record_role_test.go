package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

// Tool timeline notes are system messages in session history. OpenAI-style
// endpoints must still receive them as assistant text; Anthropic keeps them as
// conversation context outside the leading system prompt.
func TestToolRecordWireRole(t *testing.T) {
	messages := func() []core.ChatMessage {
		return []core.ChatMessage{
			{Role: core.RoleSystem, Content: "lead"},
			{Role: core.RoleUser, Content: "hi"},
			{Role: core.RoleSystem, Content: "synthetic tool record", ToolRecord: true},
			{Role: core.RoleSystem, Content: "synthetic notice"},
		}
	}
	for _, tc := range []struct {
		format Format
		body   string
		reply  string
	}{
		{ChatCompletions, "messages", `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`},
		{Responses, "input", `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`},
		{Anthropic, "messages", `{"content":[{"type":"text","text":"ok"}]}`},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			var raw string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				raw = string(data)
				fmt.Fprint(w, tc.reply)
			}))
			defer server.Close()
			client := NewClient(server.URL, "key", tc.format, time.Second)
			input := messages()
			if _, err := client.Chat(t.Context(), core.ChatRequest{Model: "m", Messages: input}); err != nil {
				t.Fatal(err)
			}
			if input[2].Role != core.RoleSystem {
				t.Fatal("request messages were mutated")
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				t.Fatal(err)
			}
			var items []map[string]any
			if err := json.Unmarshal(payload[tc.body], &items); err != nil {
				t.Fatal(err)
			}
			role := ""
			for _, item := range items {
				if strings.Contains(fmt.Sprint(item["content"]), "synthetic tool record") {
					role, _ = item["role"].(string)
				}
				if strings.Contains(fmt.Sprint(item["content"]), "synthetic notice") && tc.format != Anthropic {
					if got, _ := item["role"].(string); got != "system" {
						t.Fatalf("untagged system message role = %q", got)
					}
				}
			}
			want := "assistant"
			if tc.format == Anthropic {
				want = "user" // mid-conversation system is kept in place as user text
			}
			if role != want {
				t.Fatalf("tool record role = %q, want %q; body=%s", role, want, raw)
			}
		})
	}
}

// History trimming can leave a tool record right after the primary system
// prompt. It must stay conversation data, never join the instruction prefix.
func TestLeadingToolRecordStaysOutOfSystemInstruction(t *testing.T) {
	for _, tc := range []struct {
		format Format
		field  string
		reply  string
	}{
		{Anthropic, "system", `{"content":[{"type":"text","text":"ok"}]}`},
		{Gemini, "systemInstruction", `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			var raw string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				raw = string(data)
				fmt.Fprint(w, tc.reply)
			}))
			defer server.Close()
			client := NewClient(server.URL, "key", tc.format, time.Second)
			_, err := client.Chat(t.Context(), core.ChatRequest{Model: "m", Messages: []core.ChatMessage{
				{Role: core.RoleSystem, Content: "primary prompt"},
				{Role: core.RoleSystem, Content: "synthetic tool record", ToolRecord: true},
				{Role: core.RoleUser, Content: "hi"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				t.Fatal(err)
			}
			system := string(payload[tc.field])
			if !strings.Contains(system, "primary prompt") || strings.Contains(system, "synthetic tool record") {
				t.Fatalf("%s = %s", tc.field, system)
			}
			if !strings.Contains(raw, "synthetic tool record") {
				t.Fatalf("tool record missing from conversation: %s", raw)
			}
		})
	}
}
