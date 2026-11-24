package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	chat  func(core.ChatRequest) core.ChatMessage
	usage *core.Usage
}

func (p specificReplyProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	return &core.ChatResponse{Message: p.chat(req), Usage: p.usage}, nil
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
		wantCalls := 1
		if failDelivery {
			wantCalls = 2
		}
		if deliveries != 1 || calls != wantCalls || result.Content != "" || result.Error != nil {
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
		{"two valid sends", []core.ToolCall{valid, call("reply2", "send_message", specificReplyArgs)}},
		{"memory then two valid sends", []core.ToolCall{memoryCall, valid, call("reply2", "send_message", specificReplyArgs)}},
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
	for _, memoryFirst := range []bool{true, false} {
		name := "memory after reply"
		if memoryFirst {
			name = "memory before reply"
		}
		t.Run(name, func(t *testing.T) {
			var memoryCalls atomic.Int32
			modelCalls, deliveries := 0, 0
			send := SendMsgTool()
			memoryTool := Tool{name: "memory", executeResult: func(context.Context, string) (core.ToolExecutionResult, error) {
				memoryCalls.Add(1)
				return core.ToolExecutionResult{Content: "记忆已写入", MemoryWritten: true}, nil
			}}
			calls := []core.ToolCall{
				{ID: "memory", Type: "function", Function: core.ToolCallFunction{Name: "memory", Arguments: `{"action":"write"}`}},
				{ID: "reply", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: specificReplyArgs}},
			}
			if !memoryFirst {
				calls[0], calls[1] = calls[1], calls[0]
			}
			engine := &llm.Engine{MaxIterations: 1, ToolRegistry: map[string]llm.ToolExecutor{"memory": memoryTool, "send_message": send},
				Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
					modelCalls++
					return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: calls}
				}},
			}
			run := specificReplyContext(true)
			run.SendHook = func(string) error { deliveries++; return nil }
			session := llm.NewSessionManager().GetOrCreate(run.SessionID)
			run.Turn = session.ReserveSequenceTurn("proactive", false, true)
			defer run.Turn.Done()
			result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "unaddressed"}}, run)
			if memoryCalls.Load() != 1 || deliveries != 1 || modelCalls != 1 || !result.MemoryWritten || result.Content != "" || result.Silent || result.Error != nil {
				t.Fatalf("valid memory/reply batch rejected or echoed: memory=%d sends=%d model=%d result=%+v", memoryCalls.Load(), deliveries, modelCalls, result)
			}
			results := map[string]any{}
			for _, message := range result.Transcript {
				if message.Role == "tool" {
					results[message.ToolCallID] = message.Content
				}
			}
			if len(results) != 2 || results["reply"] != "消息已发送" || results["memory"] != "记忆已写入" {
				t.Fatalf("completed batch lost tool results: %+v", result.Transcript)
			}
		})
	}
}

func TestProactiveSpecificReplyCompletionStateIsRunLocal(t *testing.T) {
	calls, deliveries := 0, 0
	tool := SendMsgTool()
	engine := &llm.Engine{MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool},
		Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
			calls++
			return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: specificReplyArgs}}}}
		}},
	}
	run := specificReplyContext(true)
	run.SendHook = func(string) error { deliveries++; return nil }
	for i := 0; i < 2; i++ {
		result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "fixture turn"}}, run)
		if result.Content != "" || result.Error != nil {
			t.Fatalf("run %d leaked echo state: %+v", i, result)
		}
	}
	if calls != 2 || deliveries != 2 {
		t.Fatalf("calls=%d sends=%d", calls, deliveries)
	}
}

func TestProactiveSpecificReplyRunLimit(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		proactive, failFirst bool
		wantSends, wantCalls int
	}{
		{"successful reply completes run", true, false, 1, 1},
		{"failed delivery permits retry", true, true, 2, 2},
		{"explicit wake unchanged", false, false, 2, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, deliveries, executions := 0, 0, 0
			send := SendMsgTool()
			execute := send.executeResult
			send.executeResult = func(ctx context.Context, args string) (core.ToolExecutionResult, error) {
				executions++
				return execute(ctx, args)
			}
			engine := &llm.Engine{MaxIterations: 3, ToolRegistry: map[string]llm.ToolExecutor{"send_message": send},
				Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
					calls++
					if calls < 3 {
						return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "reply", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: specificReplyArgs}}}}
					}
					return core.ChatMessage{Role: core.RoleAssistant, Content: ""}
				}},
			}
			run := specificReplyContext(tc.proactive)
			run.SendHook = func(string) error {
				deliveries++
				if tc.failFirst && deliveries == 1 {
					return errors.New("fixture failed delivery")
				}
				return nil
			}
			// Reuse the engine to prove the successful-delivery budget is run-local.
			for attempt := 0; attempt < 2; attempt++ {
				calls, deliveries, executions = 0, 0, 0
				result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "unaddressed"}}, run)
				if deliveries != tc.wantSends || executions != tc.wantSends || calls != tc.wantCalls || result.Content != "" || result.Error != nil || result.Silent {
					t.Fatalf("run %d: calls=%d executors=%d sends=%d result=%+v", attempt, calls, executions, deliveries, result)
				}
			}
		})
	}
}

func TestSpecificReplyCompletionScope(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		proactive, awareness, failDelivery, noHook bool
		followUp                                   core.ChatMessage
		wantCalls, wantDeliveries                  int
		wantSilent                                 bool
	}{
		{name: "proactive reply", proactive: true, wantCalls: 1, wantDeliveries: 1},
		{name: "delivery failed", proactive: true, failDelivery: true, wantCalls: 2, wantDeliveries: 2},
		{name: "no delivery hook", proactive: true, noHook: true, wantCalls: 2, wantDeliveries: 0, wantSilent: true},
		{name: "explicit wake", wantCalls: 2, wantDeliveries: 2,
			followUp: sendMessageFinalMessage("follow-up content")},
		{name: "awareness", proactive: true, awareness: true, wantCalls: 2, wantDeliveries: 1,
			followUp: core.ChatMessage{Role: core.RoleAssistant}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, deliveries := 0, 0
			args := specificReplyArgs
			if tc.awareness {
				args = `{"messages":[{"type":"plain","text":"awareness reply"}]}`
			}
			tool := SendMsgTool()
			engine := &llm.Engine{MaxIterations: 2, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool},
				Provider: specificReplyProvider{chat: func(core.ChatRequest) core.ChatMessage {
					calls++
					if calls == 1 {
						return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: args}}}}
					}
					if tc.followUp.ToolCalls != nil || tc.followUp.Content != nil {
						return tc.followUp
					}
					return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: args}}}}
				}},
			}
			run := specificReplyContext(tc.proactive)
			run.Awareness = tc.awareness
			if !tc.noHook {
				run.SendHook = func(string) error {
					deliveries++
					if tc.failDelivery && deliveries == 1 {
						return errors.New("fixture delivery failed")
					}
					return nil
				}
			}
			result := engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "fixture turn"}}, run)
			if result.Content != "" || result.Silent != tc.wantSilent || calls != tc.wantCalls || deliveries != tc.wantDeliveries {
				t.Fatalf("result=%+v calls=%d sends=%d", result, calls, deliveries)
			}
			if !tc.wantSilent && result.Error != nil {
				t.Fatalf("unexpected error: %+v", result)
			}
		})
	}
}

func TestProactiveSpecificReplyWaitsForACKAndCompletesAtIterationLimit(t *testing.T) {
	var modelCalls atomic.Int32
	entered, ack := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(ack) })
	usage := &core.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}
	tool := SendMsgTool()
	engine := &llm.Engine{MaxIterations: 1, ToolRegistry: map[string]llm.ToolExecutor{tool.Name(): tool},
		Provider: specificReplyProvider{usage: usage, chat: func(core.ChatRequest) core.ChatMessage {
			modelCalls.Add(1)
			return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: specificReplyArgs}}}}
		}},
	}
	run := specificReplyContext(true)
	run.SendHook = func(string) error {
		close(entered)
		<-ack
		return nil
	}
	finished := make(chan llm.AgentRunResult, 1)
	go func() {
		finished <- engine.RunMessagesWithContext([]llm.ChatMessage{{Role: "user", Content: "fixture turn"}}, run)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not start")
	}
	select {
	case result := <-finished:
		t.Fatalf("run finished before delivery confirmation: %+v", result)
	default:
	}
	release.Do(func() { close(ack) })
	select {
	case result := <-finished:
		if result.Error != nil || result.Content != "" || result.Silent || result.Usage.TotalTokens != usage.TotalTokens || modelCalls.Load() != 1 {
			t.Fatalf("confirmed delivery did not finish successfully with usage: %+v, calls=%d", result, modelCalls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after ACK")
	}
}
