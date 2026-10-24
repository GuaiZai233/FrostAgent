package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/sticker"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type proactiveMessageTool struct {
	structuredResultTool
	calls int
}

func (t *proactiveMessageTool) IsProactiveAllowed() bool { return true }
func (t *proactiveMessageTool) ExecuteResult(ctx context.Context, args string) (core.ToolExecutionResult, error) {
	t.calls++
	return t.structuredResultTool.ExecuteResult(ctx, args)
}

func TestProactiveStickerToolDeniedEvenWithOptIn(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	for _, mock := range []bool{false, true} {
		p := &stickerActivityProvider{responses: []core.ChatMessage{{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "sticker", Type: "function", Function: core.ToolCallFunction{Name: "send_sticker", Arguments: `{}`},
		}}}}}
		tool := &proactiveMessageTool{structuredResultTool: structuredResultTool{staticTool: staticTool{name: "send_sticker"}}}
		e := &Engine{Provider: p, MaxIterations: 1, ToolRegistry: map[string]ToolExecutor{"send_sticker": tool}}
		result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
			Proactive: true, Mock: mock,
			SendHook: func(string) error { t.Fatal("proactive sticker reached delivery"); return nil },
		})
		if tool.calls != 0 || !result.Silent || result.Error != nil || len(p.requests) != 1 {
			t.Fatalf("mock=%t calls=%d result=%+v requests=%d", mock, tool.calls, result, len(p.requests))
		}
		for _, offered := range p.requests[0].Tools {
			if offered.Name == "send_sticker" {
				t.Fatal("send_sticker was offered to a proactive turn")
			}
		}
	}
}

func TestProactiveRepliesSkipStickerEnforcement(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	for _, tc := range []struct {
		name      string
		responses []core.ChatMessage
		silent    bool
	}{
		{name: "text", responses: []core.ChatMessage{{Role: core.RoleAssistant, Content: "ordinary reply"}}},
		{name: "silence marker", silent: true, responses: []core.ChatMessage{{Role: core.RoleAssistant, Content: AssistantSilentMarker}}},
		{name: "silence tool", silent: true, responses: []core.ChatMessage{{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: StaySilentToolName, Arguments: `{}`},
		}}}}},
		{name: "memory then text", responses: []core.ChatMessage{
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "memory", Type: "function", Function: core.ToolCallFunction{Name: "memory", Arguments: `{"action":"write"}`}}}},
			{Role: core.RoleAssistant, Content: "ordinary reply"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &stickerActivityProvider{responses: tc.responses}
			e := &Engine{Provider: p, MaxIterations: len(tc.responses), ToolRegistry: map[string]ToolExecutor{
				StaySilentToolName: &staticTool{name: StaySilentToolName},
				"memory":           &staticTool{name: "memory", result: "记忆已写入"},
			}}
			for range 4 {
				e.RecordBotTurn()
			}
			input := []ChatMessage{{Role: "user", Content: "hello"}}
			result := e.RunMessagesWithContext(input, RunContext{Proactive: true})
			if result.Error != nil || result.Silent != tc.silent || len(p.requests) != len(tc.responses) {
				t.Fatalf("result=%+v requests=%d", result, len(p.requests))
			}
			if !tc.silent && result.Content != "ordinary reply" {
				t.Fatalf("ordinary reply changed: %q", result.Content)
			}
			if tc.name == "memory then text" && !result.MemoryWritten {
				t.Fatal("proactive memory write was lost")
			}
			for _, req := range p.requests {
				for _, msg := range req.Messages {
					if text, ok := msg.Content.(string); ok && (strings.Contains(text, "表情包发送") || text == stickerEnforcementPrompt || text == ignoreEnforceStickerReply) {
						t.Fatalf("proactive request contains sticker enforcement: %q", text)
					}
				}
			}
			if _, remaining, grace := e.stickerActivitySnapshot(); remaining != 0 || grace != 0 {
				t.Fatalf("proactive turn changed outstanding quota: remaining=%d grace=%d", remaining, grace)
			}
			if input[0].Content != "hello" {
				t.Fatal("caller history was modified")
			}
		})
	}
}

func TestProactiveMessagePathsFilterStickerRepresentations(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	for _, tc := range []struct{ name, payload, want string }{
		{"sticker only", `{"messages":[{"type":"image","url":"https://example.invalid/sticker.png","is_sticker":true}]}`, ""},
		{"mface type", `{"messages":[{"type":"mface","url":"https://example.invalid/sticker.png"}]}`, ""},
		{"sticker type", `{"messages":[{"type":"sticker","url":"https://example.invalid/sticker.png"}]}`, ""},
		{"normalized sticker type", `{"messages":[{"type":" STICKER ","url":"https://example.invalid/sticker.png"}]}`, ""},
		{"numeric subtype", `{"messages":[{"type":"image","sub_type":1,"url":"https://example.invalid/sticker.png"}]}`, ""},
		{"string subtype", `{"messages":[{"type":"image","sub_type":" 1 ","url":"https://example.invalid/sticker.png"}]}`, ""},
		{"numeric camel subtype", `{"messages":[{"type":"image","subType":1,"url":"https://example.invalid/sticker.png"}]}`, ""},
		{"string camel subtype", `{"messages":[{"type":"image","subType":"1","url":"https://example.invalid/sticker.png"}]}`, ""},
		{"nested flag", `{"messages":[{"type":"image","data":{"is_sticker":true,"file":"https://example.invalid/sticker.png"}}]}`, ""},
		{"nested numeric subtype", `{"messages":[{"type":"image","data":{"sub_type":1,"file":"https://example.invalid/sticker.png"}}]}`, ""},
		{"nested string subtype", `{"messages":[{"type":"image","data":{"sub_type":"1","file":"https://example.invalid/sticker.png"}}]}`, ""},
		{"nested numeric camel subtype", `{"messages":[{"type":"image","data":{"subType":1,"file":"https://example.invalid/sticker.png"}}]}`, ""},
		{"nested string camel subtype", `{"messages":[{"type":"image","data":{"subType":" 1 ","file":"https://example.invalid/sticker.png"}}]}`, ""},
		{"false flag cannot override subtype", `{"messages":[{"type":"image","is_sticker":false,"sub_type":0,"data":{"subType":1}}]}`, ""},
		{"marked component", `{"messages":[{"type":"plain","text":"marked sticker","is_sticker":true}]}`, ""},
		{"malformed optional data cannot hide type", `{"messages":[{"type":"mface","data":"invalid"}]}`, ""},
		{"ordinary image", `{"messages":[{"type":"image","url":"https://example.invalid/image.png"}]}`, `{"messages":[{"type":"image","url":"https://example.invalid/image.png"}]}`},
		{"explicit ordinary image", `{"messages":[{"type":"image","path":"/test/image.gif","is_sticker":false}]}`, `{"messages":[{"type":"image","path":"/test/image.gif","is_sticker":false}]}`},
		{"ordinary image subtypes", `{"messages":[{"type":"image","url":"https://example.invalid/image.gif","sub_type":0,"subType":"0","data":{"sub_type":0,"subType":"0","is_sticker":false}}]}`, `{"messages":[{"type":"image","url":"https://example.invalid/image.gif","sub_type":0,"subType":"0","data":{"sub_type":0,"subType":"0","is_sticker":false}}]}`},
		{"mixed", `{"messages":[{"type":"plain","text":"before"},{"type":"image","path":"/test/sticker.png","is_sticker":true},{"type":"image","url":"https://example.invalid/image.gif"},{"type":"plain","text":"after"}]}`, `{"messages":[{"type":"plain","text":"before"},{"type":"image","url":"https://example.invalid/image.gif"},{"type":"plain","text":"after"}]}`},
		{"mixed representations", `{"messages":[{"type":"plain","text":"before"},{"type":"mface"},{"type":"image","sub_type":1},{"type":"image","data":{"subType":"1"}},{"type":"image","url":"https://example.invalid/image.gif","subType":0},{"type":"sticker"},{"type":"plain","text":"after"}]}`, `{"messages":[{"type":"plain","text":"before"},{"type":"image","url":"https://example.invalid/image.gif","subType":0},{"type":"plain","text":"after"}]}`},
		{"text", "ordinary reply", "ordinary reply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, adapter := range []string{"onebot", "astrbot"} {
				for _, proactive := range []bool{false, true} {
					p := &stickerActivityProvider{responses: []core.ChatMessage{{Role: core.RoleAssistant, Content: tc.payload}}}
					e := &Engine{Provider: p, MaxIterations: 1}
					if proactive {
						for range 4 {
							e.RecordBotTurn()
						}
					}
					result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{Proactive: proactive, AdapterID: adapter})
					want := tc.payload
					if proactive {
						want = tc.want
					}
					assertStickerFilteredContent(t, result.Content, want)
					if result.Error != nil || len(p.requests) != 1 {
						t.Fatalf("adapter=%s proactive=%t result=%+v requests=%d", adapter, proactive, result, len(p.requests))
					}
				}
			}

			// A separately opted-in tool must still pass the common delivery gate.
			tool := &proactiveMessageTool{structuredResultTool: structuredResultTool{
				staticTool: staticTool{name: "test_message"},
				output:     core.ToolExecutionResult{Content: "prepared", MessagePayload: tc.payload},
			}}
			p := &stickerActivityProvider{responses: []core.ChatMessage{
				{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "message", Type: "function", Function: core.ToolCallFunction{Name: tool.Name(), Arguments: `{}`}}}},
				{Role: core.RoleAssistant, Content: "done"},
			}}
			e := &Engine{Provider: p, MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{tool.Name(): tool}}
			for range 4 {
				e.RecordBotTurn()
			}
			deliveries := 0
			result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
				Proactive: true,
				SendHook: func(got string) error {
					deliveries++
					assertStickerFilteredContent(t, got, tc.want)
					return nil
				},
			})
			wantDeliveries := 1
			if tc.want == "" {
				wantDeliveries = 0
			}
			if tool.calls != 1 || deliveries != wantDeliveries || result.Content != "done" || result.Error != nil || len(p.requests) != 2 {
				t.Fatalf("calls=%d deliveries=%d result=%+v requests=%d", tool.calls, deliveries, result, len(p.requests))
			}
			if _, remaining, grace := e.stickerActivitySnapshot(); remaining != 0 || grace != 0 {
				t.Fatalf("filtered sticker reset quota: remaining=%d grace=%d", remaining, grace)
			}
		})
	}
}

func TestFilterStickerMessagesPreservesUnrelatedContent(t *testing.T) {
	for _, input := range []string{"hello", `{"messages":[]}`, `{"messages":null}`, `{"messages":`, `"text"`, `{"other":true}`, `{"messages":[{"type":"plain","text":"text","sub_type":1}]}`} {
		if got, filtered := filterStickerMessages(input); filtered || got != input {
			t.Fatalf("unrelated input changed: %q -> %q filtered=%t", input, got, filtered)
		}
	}
}

func assertStickerFilteredContent(t *testing.T, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	var gotJSON, wantJSON any
	if json.Unmarshal([]byte(got), &gotJSON) != nil || json.Unmarshal([]byte(want), &wantJSON) != nil || !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("content = %q, want %q", got, want)
	}
}
