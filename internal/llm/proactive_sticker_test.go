package llm

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
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
			SendHook: func(string) error { t.Error("proactive sticker reached delivery"); return nil },
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
	for _, tc := range append([]struct{ name, payload, want string }{
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
	}, proactiveStickerEnvelopeCases()...) {
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
					turn := (&SessionContext{}).ReserveSequenceTurn("synthetic-sender", false, proactive)
					turn.Wait()
					result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{Proactive: proactive, AdapterID: adapter, Turn: turn})
					turn.Done()
					want := tc.payload
					if proactive {
						want = tc.want
					}
					assertStickerFilteredContent(t, result.Content, want)
					if len(result.Transcript) == 0 {
						t.Fatal("missing final transcript")
					}
					last := result.Transcript[len(result.Transcript)-1]
					if last.Role != "assistant" || last.Content != result.Content {
						t.Fatalf("final transcript = %+v, returned content = %q", last, result.Content)
					}
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
			e.grantStickerGrace()
			var deliveredPayloads []string
			result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
				Proactive: true,
				SendHook: func(got string) error {
					deliveredPayloads = append(deliveredPayloads, got)
					return nil
				},
			})
			wantDeliveries := 1
			if tc.want == "" {
				wantDeliveries = 0
			}
			if tool.calls != 1 || len(deliveredPayloads) != wantDeliveries || result.Content != "done" || result.Error != nil || len(p.requests) != 2 {
				t.Fatalf("calls=%d deliveries=%d result=%+v requests=%d", tool.calls, len(deliveredPayloads), result, len(p.requests))
			}
			for _, got := range deliveredPayloads {
				assertStickerFilteredContent(t, got, tc.want)
			}
			if _, remaining, grace := e.stickerActivitySnapshot(); remaining != 0 || grace != 2-wantDeliveries {
				t.Fatalf("filtered delivery changed quota or counted incorrectly: remaining=%d grace=%d", remaining, grace)
			}
		})
	}
}

func proactiveStickerEnvelopeCases() []struct{ name, payload, want string } {
	return []struct{ name, payload, want string }{
		{
			name:    "session and unknown fields",
			payload: `{"session":"onebot:group:synthetic-target","messages":[{"type":"plain","text":"before"},{"type":"mface"},{"type":"image","url":"https://example.invalid/image.gif","extra":9007199254740993},{"type":"plain","text":"after"}],"metadata":{"sequence":9007199254740993,"decimal":1.234567890123456789,"nested":[true,null,{"keep":"value"}]},"trace_id":"synthetic","enabled":false,"extension":null}`,
			want:    `{"session":"onebot:group:synthetic-target","messages":[{"type":"plain","text":"before"},{"type":"image","url":"https://example.invalid/image.gif","extra":9007199254740993},{"type":"plain","text":"after"}],"metadata":{"sequence":9007199254740993,"decimal":1.234567890123456789,"nested":[true,null,{"keep":"value"}]},"trace_id":"synthetic","enabled":false,"extension":null}`,
		},
		{
			name:    "capitalized messages key",
			payload: `{"session":"synthetic-target","Messages":[{"type":"sticker"},{"type":"image","path":"/test/image.gif"}],"version":9007199254740993}`,
			want:    `{"session":"synthetic-target","Messages":[{"type":"image","path":"/test/image.gif"}],"version":9007199254740993}`,
		},
		{
			name:    "uppercase messages key",
			payload: `{"session":"synthetic-target","MESSAGES":[{"type":"plain","text":"keep"},{"type":"image","sub_type":1}]}`,
			want:    `{"session":"synthetic-target","MESSAGES":[{"type":"plain","text":"keep"}]}`,
		},
		{
			name:    "message key aliases",
			payload: `{"session":"synthetic-target","messages":[{"type":"sticker"}],"Messages":[{"type":"plain","text":"keep"},{"type":"mface"}]}`,
			want:    `{"session":"synthetic-target","messages":[{"type":"plain","text":"keep"}],"Messages":[{"type":"plain","text":"keep"}]}`,
		},
		{
			name:    "message key aliases reverse order",
			payload: `{"Messages":[{"type":"sticker"}],"messages":[{"type":"plain","text":"keep"},{"type":"mface"}],"session":"synthetic-target"}`,
			want:    `{"Messages":[{"type":"plain","text":"keep"}],"messages":[{"type":"plain","text":"keep"}],"session":"synthetic-target"}`,
		},
		{
			name:    "sticker only with envelope",
			payload: `{"session":"synthetic-target","metadata":{"keep":true},"messages":[{"type":"sticker"}]}`,
			want:    "",
		},
	}
}

func TestFilterStickerMessagesPreservesEnvelope(t *testing.T) {
	for _, tc := range proactiveStickerEnvelopeCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, filtered := filterStickerMessages(tc.payload)
			if !filtered {
				t.Fatal("sticker was not filtered")
			}
			assertStickerFilteredContent(t, got, tc.want)
		})
	}
}

func TestProactiveFailedDeliveryDoesNotConsumeStickerGrace(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	e := &Engine{}
	e.RecordBotTurn()
	e.grantStickerGrace()
	tc := proactiveStickerEnvelopeCases()[0]
	var delivered string
	ctx := WithRunContext(context.Background(), RunContext{
		Proactive: true,
		SendHook: func(payload string) error {
			delivered = payload
			return errors.New("synthetic delivery failure")
		},
	})
	_, sent := e.deliverToolResult(ctx, core.ToolExecutionResult{MessagePayload: tc.payload}, "prepared")
	if sent {
		t.Fatal("failed hook counted as delivered")
	}
	assertStickerFilteredContent(t, delivered, tc.want)
	if _, remaining, grace := e.stickerActivitySnapshot(); remaining != 4 || grace != 2 {
		t.Fatalf("failed delivery changed quota: remaining=%d grace=%d", remaining, grace)
	}
}

func TestFilterStickerMessagesPreservesUnrelatedContent(t *testing.T) {
	for _, input := range []string{"hello", `{"messages":[]}`, `{"messages":null}`, `{"messages":`, `"text"`, `{"other":true}`, `{"messages":[{"type":"plain","text":"text","sub_type":1}]}`} {
		if got, filtered := filterStickerMessages(input); filtered || got != input {
			t.Fatalf("unrelated input changed: %q -> %q filtered=%t", input, got, filtered)
		}
	}
	input := "{\n  \"session\":\"synthetic-target\", \"metadata\":{\"sequence\":9007199254740993}, \"messages\":[{\"type\":\"image\",\"url\":\"https://example.invalid/image.gif\"}]\n}"
	if got, filtered := filterStickerMessages(input); filtered || got != input {
		t.Fatalf("ordinary image envelope changed: %q -> %q filtered=%t", input, got, filtered)
	}
}

func assertStickerFilteredContent(t *testing.T, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	var gotJSON, wantJSON any
	gotDecoder := json.NewDecoder(strings.NewReader(got))
	gotDecoder.UseNumber()
	wantDecoder := json.NewDecoder(strings.NewReader(want))
	wantDecoder.UseNumber()
	if gotDecoder.Decode(&gotJSON) != nil || wantDecoder.Decode(&wantJSON) != nil || !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("content = %q, want %q", got, want)
	}
}
