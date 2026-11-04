package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

const specificReplyArgs = `{"messages":[{"type":"mention_specific","message_id":"fixture-old"},{"type":"plain","text":"reply to earlier topic"}]}`

func specificReplyContext(proactive bool) llm.RunContext {
	return llm.RunContext{SessionID: "fixture-session", Proactive: proactive,
		CanReplyToMessage: func(id string) bool { return id == "fixture-old" },
	}
}

func TestSendMsgToolSpecificReply(t *testing.T) {
	for _, proactive := range []bool{false, true} {
		ctx := llm.WithRunContext(context.Background(), specificReplyContext(proactive))
		result, err := SendMsgTool().ExecuteResult(ctx, specificReplyArgs)
		if err != nil || result.Content == "" || result.MessagePayload != result.Content {
			t.Fatalf("structured result = %+v, %v", result, err)
		}
		var payload struct {
			Messages            []Msg `json:"messages"`
			SuppressAutoMention bool  `json:"suppress_auto_mention"`
		}
		if err := json.Unmarshal([]byte(result.MessagePayload), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) != 2 || payload.Messages[0].Type != "quote" || payload.Messages[0].MessageID != "fixture-old" || !payload.SuppressAutoMention {
			t.Fatalf("specific reply was not normalized: %+v", payload)
		}
		segments, err := BuildOneBotMessage(payload.Messages)
		if err != nil || len(segments) != 2 || segments[0].Type != "reply" || segments[0].Data["id"] != "fixture-old" {
			t.Fatalf("native quote = %+v, %v", segments, err)
		}
	}
}

func TestSendMsgToolSpecificReplyRejectsUntrustedOrAmbiguousTarget(t *testing.T) {
	for _, proactive := range []bool{false, true} {
		for _, args := range []string{
			strings.Replace(specificReplyArgs, "fixture-old", "foreign-or-invented", 1),
			strings.Replace(specificReplyArgs, `"fixture-old"`, `""`, 1),
			strings.Replace(specificReplyArgs, `"messages":`, `"session":"other","messages":`, 1),
			`{"messages":[{"type":"plain","text":"reply"},{"type":"mention_specific","message_id":"fixture-old"}]}`,
			`{"messages":[{"type":"mention_specific","message_id":"fixture-old"},{"type":"quote","message_id":"another"}]}`,
		} {
			ctx := llm.WithRunContext(context.Background(), specificReplyContext(proactive))
			result, err := SendMsgTool().ExecuteResult(ctx, args)
			if err == nil || result.MessagePayload != "" {
				t.Errorf("unsafe specific reply accepted: %s -> %+v, %v", args, result, err)
			}
		}
	}
	for _, run := range []llm.RunContext{{}, {SessionID: "fixture-session"}} {
		result, err := SendMsgTool().ExecuteResult(llm.WithRunContext(context.Background(), run), specificReplyArgs)
		if err == nil || result.MessagePayload != "" {
			t.Fatalf("missing trusted adapter scope accepted: %+v, %v", result, err)
		}
	}
}

type specificReplyProvider struct {
	chat func(core.ChatRequest) core.ChatMessage
}

func (p specificReplyProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	return &core.ChatResponse{Message: p.chat(req)}, nil
}

func TestProactiveSpecificReplyEngine(t *testing.T) {
	for _, failDelivery := range []bool{false, true} {
		calls, deliveries := 0, 0
		tool := SendMsgTool()
		engine := &llm.Engine{MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool},
			Provider: specificReplyProvider{chat: func(req core.ChatRequest) core.ChatMessage {
				calls++
				if calls == 1 {
					if len(req.Tools) != 1 || req.Tools[0].Name != "send_message" {
						t.Fatalf("specific reply tool not exposed: %+v", req.Tools)
					}
					schema, _ := json.Marshal(req.Tools[0].Parameters)
					for _, hidden := range []string{`"session"`, `"image"`, `"mention_user"`, `"quote"`, `"url"`} {
						if strings.Contains(string(schema), hidden) {
							t.Errorf("proactive schema exposes %s: %s", hidden, schema)
						}
					}
					return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: specificReplyArgs}}}}
				}
				result, _ := req.Messages[len(req.Messages)-1].Content.(string)
				if (result == "消息已发送") == failDelivery {
					t.Errorf("incorrect delivery confirmation: %q, fail=%v", result, failDelivery)
				}
				return core.ChatMessage{Role: core.RoleAssistant, Content: ""}
			}},
		}
		run := specificReplyContext(true)
		run.SendHook = func(string) error {
			deliveries++
			if failDelivery {
				return errors.New("fixture transport failure")
			}
			return nil
		}
		result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "later unrelated message"}}, run)
		if deliveries != 1 || calls != 2 || result.Content != "" || result.Error != nil {
			t.Fatalf("unexpected run: deliveries=%d calls=%d result=%+v", deliveries, calls, result)
		}
	}
}

func TestProactiveSpecificReplyEngineRejectsGeneralSend(t *testing.T) {
	for _, args := range []string{
		`{"messages":[{"type":"plain","text":"unquoted"}]}`,
		strings.Replace(specificReplyArgs, `"plain"`, `"image"`, 1),
		strings.Replace(specificReplyArgs, `"messages":`, `"session":"foreign","messages":`, 1),
	} {
		calls, deliveries := 0, 0
		tool := SendMsgTool()
		engine := &llm.Engine{MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool},
			Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
				calls++
				return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "unsafe", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: args}}}}
			}},
		}
		run := specificReplyContext(true)
		run.SendHook = func(string) error { deliveries++; return nil }
		result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "unaddressed"}}, run)
		if deliveries != 0 || calls != 1 || !result.Silent {
			t.Fatalf("unsafe proactive send did not fuse: %s, %+v", args, result)
		}
	}
}

func TestProactiveSpecificReplyBatchPreflight(t *testing.T) {
	call := func(id, name, args string) core.ToolCall {
		return core.ToolCall{ID: id, Type: "function", Function: core.ToolCallFunction{Name: name, Arguments: args}}
	}
	valid := call("reply", "send_message", specificReplyArgs)
	forbidden := call("forbidden", "execute_command", `{}`)
	malformed := call("malformed", "send_message", `{"messages":[{"type":"plain","text":"unquoted"}]}`)
	memoryCall := call("memory", "memory", `{"action":"write"}`)
	for _, tc := range []struct {
		name  string
		calls []core.ToolCall
	}{
		{"send then forbidden", []core.ToolCall{valid, forbidden}},
		{"forbidden then send", []core.ToolCall{forbidden, valid}},
		{"send then malformed send", []core.ToolCall{valid, malformed}},
		{"memory then send then forbidden", []core.ToolCall{memoryCall, valid, forbidden}},
		{"silence conflict with forbidden", []core.ToolCall{valid, call("silent", "stay_silent", `{}`), forbidden}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var executions atomic.Int32
			deliveries, modelCalls := 0, 0
			send := SendMsgTool()
			executeSend := send.executeResult
			send.executeResult = func(ctx context.Context, args string) (core.ToolExecutionResult, error) {
				executions.Add(1)
				return executeSend(ctx, args)
			}
			spy := func(name string) Tool {
				return Tool{name: name, execute: func(string) (string, error) {
					executions.Add(1)
					return "done", nil
				}}
			}
			engine := &llm.Engine{MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{
				"send_message": send, "execute_command": spy("execute_command"), "memory": spy("memory"),
			}, Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
				modelCalls++
				return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: tc.calls}
			}}}
			run := specificReplyContext(true)
			run.SendHook = func(string) error { deliveries++; return nil }
			result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "unaddressed"}}, run)
			if executions.Load() != 0 || deliveries != 0 || modelCalls != 1 || !result.Silent || result.MemoryWritten {
				t.Fatalf("illegal batch partially executed: executors=%d sends=%d model=%d result=%+v", executions.Load(), deliveries, modelCalls, result)
			}
		})
	}
}

func TestProactiveSpecificReplyBatchAllowsMemoryAndReply(t *testing.T) {
	var memoryCalls atomic.Int32
	modelCalls, deliveries := 0, 0
	send := SendMsgTool()
	memoryTool := Tool{name: "memory", executeResult: func(context.Context, string) (core.ToolExecutionResult, error) {
		memoryCalls.Add(1)
		return core.ToolExecutionResult{Content: "记忆已写入", MemoryWritten: true}, nil
	}}
	engine := &llm.Engine{MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{"memory": memoryTool, "send_message": send},
		Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
			modelCalls++
			if modelCalls == 1 {
				return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
					{ID: "memory", Type: "function", Function: core.ToolCallFunction{Name: "memory", Arguments: `{"action":"write"}`}},
					{ID: "reply", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: specificReplyArgs}},
				}}
			}
			return core.ChatMessage{Role: core.RoleAssistant, Content: "reply to earlier topic"}
		}},
	}
	run := specificReplyContext(true)
	run.SendHook = func(string) error { deliveries++; return nil }
	result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "unaddressed"}}, run)
	if memoryCalls.Load() != 1 || deliveries != 1 || modelCalls != 2 || !result.MemoryWritten || result.Content != "" || result.Silent {
		t.Fatalf("valid memory/reply batch rejected or echoed: memory=%d sends=%d model=%d result=%+v", memoryCalls.Load(), deliveries, modelCalls, result)
	}
}

func TestProactiveSpecificReplyEchoStateIsRunLocal(t *testing.T) {
	calls, deliveries := 0, 0
	tool := SendMsgTool()
	engine := &llm.Engine{MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool},
		Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
			calls++
			if calls == 1 {
				return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: specificReplyArgs}}}}
			}
			return core.ChatMessage{Role: core.RoleAssistant, Content: "reply to earlier topic"}
		}},
	}
	run := specificReplyContext(true)
	run.SendHook = func(string) error { deliveries++; return nil }
	for i, want := range []string{"", "reply to earlier topic"} {
		result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "fixture turn"}}, run)
		if result.Content != want || result.Error != nil {
			t.Fatalf("run %d leaked echo state: %+v", i, result)
		}
	}
	if calls != 3 || deliveries != 1 {
		t.Fatalf("calls=%d sends=%d", calls, deliveries)
	}
}

func TestProactiveSpecificReplyFinalEcho(t *testing.T) {
	const sent = "reply to earlier topic"
	for _, tc := range []struct {
		name                    string
		proactive, failDelivery bool
		final, want             string
	}{
		{"echo", true, false, sent, ""},
		{"whitespace echo", true, false, " \n" + sent + "\n", ""},
		{"different reply", true, false, "different follow-up", "different follow-up"},
		{"delivery failed", true, true, sent, sent},
		{"explicit wake unchanged", false, false, sent, sent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, deliveries := 0, 0
			tool := SendMsgTool()
			engine := &llm.Engine{MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool},
				Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
					calls++
					if calls == 1 {
						return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: specificReplyArgs}}}}
					}
					return core.ChatMessage{Role: core.RoleAssistant, Content: tc.final}
				}},
			}
			run := specificReplyContext(tc.proactive)
			run.SendHook = func(string) error {
				deliveries++
				if tc.failDelivery {
					return errors.New("fixture delivery failed")
				}
				return nil
			}
			result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "fixture turn"}}, run)
			if result.Content != tc.want || result.Error != nil || result.Silent || calls != 2 || deliveries != 1 {
				t.Fatalf("final=%q want=%q result=%+v calls=%d sends=%d", result.Content, tc.want, result, calls, deliveries)
			}
		})
	}
}
