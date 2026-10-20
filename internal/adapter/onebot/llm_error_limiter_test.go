package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/model"
	"FrostAgent/internal/security"

	"github.com/gorilla/websocket"
)

func TestLLMErrorReplyLimiterRollingWindow(t *testing.T) {
	var limiter llmErrorReplyLimiter
	start := time.Unix(1000, 0)
	for _, tc := range []struct {
		offset time.Duration
		want   bool
	}{
		{0, true}, {10 * time.Second, true}, {20 * time.Second, true},
		{59 * time.Second, false}, {time.Minute - time.Nanosecond, false},
		{time.Minute, true}, {time.Minute, false},
		{70*time.Second - time.Nanosecond, false}, {70 * time.Second, true},
		{80 * time.Second, true}, {80 * time.Second, false},
	} {
		if got := limiter.allowAt(start.Add(tc.offset)); got != tc.want {
			t.Fatalf("allow at %v = %v, want %v", tc.offset, got, tc.want)
		}
	}
}

func TestLLMErrorReplyLimiterConcurrent(t *testing.T) {
	var limiter llmErrorReplyLimiter
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.allow() {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := admitted.Load(); got != 3 {
		t.Fatalf("admitted %d errors, want 3", got)
	}
}

func TestLLMErrorReplyLimiterClassificationAndIsolation(t *testing.T) {
	var limiter llmErrorReplyLimiter
	conn := &wsConnection{llmErrorReplies: &limiter}
	failure := llm.AgentRunResult{Error: errors.New("synthetic provider failure"), LLMFailed: true}
	mock := &wsConnection{mock: true, llmErrorReplies: &limiter}
	for i := 0; i < 5; i++ {
		if !mock.allowLLMErrorReply(failure) {
			t.Fatal("mock error was suppressed")
		}
	}
	for i := 0; i < 3; i++ {
		if !conn.allowLLMErrorReply(failure) {
			t.Fatal("mock consumed production quota")
		}
	}
	if conn.allowLLMErrorReply(failure) {
		t.Fatal("fourth production error admitted")
	}
	for _, result := range []llm.AgentRunResult{
		{Content: "FrostAgent 错误：LLM 响应失败：quoted normal answer"},
		{Error: security.ErrLocked, OutputBlocked: true},
		{Error: billing.ErrInsufficientFunds},
		{Error: llm.ErrMaxIterationsReached},
	} {
		if !conn.allowLLMErrorReply(result) {
			t.Fatalf("non-LLM result suppressed: %+v", result)
		}
	}
	other := &wsConnection{llmErrorReplies: &llmErrorReplyLimiter{}}
	if !other.allowLLMErrorReply(failure) {
		t.Fatal("one instance consumed another's quota")
	}
}

func TestWSLLMErrorsShareQuotaAcrossConnections(t *testing.T) {
	for _, messageType := range []string{"private", "group"} {
		for _, billed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/billing=%v", messageType, billed), func(t *testing.T) {
				provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
					text, _ := req.Messages[len(req.Messages)-1].Content.(string)
					if !strings.Contains(text, "sentinel") {
						return nil, errors.New("synthetic provider failure")
					}
					for _, message := range req.Messages {
						content, _ := message.Content.(string)
						if message.Role == core.RoleAssistant && strings.Contains(content, "LLM 响应失败") {
							return nil, errors.New("dropped error polluted history")
						}
					}
					return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "sentinel success"}}, nil
				}}
				engine := newTestEngine(provider)
				engine.Scope = newTestScopeWithEnv(t, map[string]string{"PROACTIVE_REPLY_PROBABILITY": "1"})
				engine.Security = security.NewController(t.TempDir())
				engine.Security.Watchdog.SetClassifier(&mockClassifier{})
				var state *mockAlcyoneState
				if billed {
					billingServer, billingState := mockAlcyoneBillingServer(t)
					defer billingServer.Close()
					state = billingState
					engine.BillingClient = billing.NewClient(billingServer.URL, "test-token", 2*time.Second)
					engine.BillingConfig = billing.Config{Enabled: true, Timeout: 2 * time.Second, MaxOutputTokens: 2048, SafetyMultiplier: 1.2, ModelName: "test-model"}
				}
				server, url := startWSTestServer(engine)
				defer server.Close()
				dial := func() *websocket.Conn {
					t.Helper()
					conn, _, err := websocket.DefaultDialer.Dial(url, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					return conn
				}
				first := dial()
				var messageID int32
				send := func(conn *websocket.Conn, user int64, text string) {
					t.Helper()
					messageID++
					segments := []map[string]any{{"type": "text", "data": map[string]string{"text": text}}}
					if messageType == "group" && text != "proactive fail" {
						segments = append([]map[string]any{{"type": "at", "data": map[string]string{"qq": "103"}}}, segments...)
					}
					message, _ := json.Marshal(segments)
					if err := conn.WriteJSON(model.OneBotEvent{SelfID: 103, PostType: "message", MessageType: messageType, GroupID: 200 + user, UserID: user, MessageID: messageID, Message: message}); err != nil {
						t.Fatal(err)
					}
				}
				read := func(conn *websocket.Conn, want string) {
					t.Helper()
					conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					var action model.OneBotAction
					for {
						if err := conn.ReadJSON(&action); err != nil {
							t.Fatal(err)
						}
						if action.Action != "get_group_info" {
							break
						}
						if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"group_id": action.Params.(map[string]any)["group_id"], "group_name": "synthetic group"}, "echo": action.Echo}); err != nil {
							t.Fatal(err)
						}
					}
					raw, _ := json.Marshal(action.Params)
					if action.Action != "send_"+messageType+"_msg" || !strings.Contains(string(raw), want) {
						t.Fatalf("want %q, got %s %s", want, action.Action, raw)
					}
					if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"message_id": messageID + 100}, "echo": action.Echo}); err != nil {
						t.Fatal(err)
					}
				}
				if messageType == "group" {
					send(first, 101, "proactive fail")
				}
				for i := 0; i < 3; i++ {
					send(first, 101, "fail")
					read(first, "LLM 响应失败")
				}
				first.Close()
				second := dial()
				// The success in the same session waits for the preceding dropped turn.
				send(second, 102, "fail")
				send(second, 102, "sentinel")
				read(second, "sentinel success")
				if state != nil {
					state.mu.Lock()
					released := state.releaseCount
					state.mu.Unlock()
					if released != 4 {
						t.Fatalf("released %d failed reservations, want 4", released)
					}
				}
				principal, err := security.NewPrincipal("onebot", "104")
				if err != nil {
					t.Fatal(err)
				}
				if err := engine.Security.Lock(principal, "synthetic lock"); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 4; i++ {
					send(second, 104, "blocked")
					read(second, "Request rejected by security gateway")
				}
			})
		}
	}
}

func TestWSLLMErrorLimitPreservesSettledReceipt(t *testing.T) {
	for _, messageType := range []string{"private", "group"} {
		for _, exhausted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exhausted=%v", messageType, exhausted), func(t *testing.T) {
				billingServer, state := mockAlcyoneBillingServer(t)
				defer billingServer.Close()
				tool := &mockTestTool{name: "test_tool", result: "synthetic tool result"}
				provider := &mockLLMProvider{
					responses: []*core.ChatResponse{{
						Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
							ID: "test-call", Type: "function", Function: core.ToolCallFunction{Name: tool.name, Arguments: "{}"},
						}}},
						Usage: &core.Usage{PromptTokens: 500, CompletionTokens: 50, TotalTokens: 550},
					}},
					errs: []error{nil, errors.New("synthetic failure after settled iteration")},
				}
				engine := newTestEngine(provider)
				engine.Scope = newTestScopeWithEnv(t, map[string]string{
					"PROACTIVE_REPLY_PROBABILITY": "0",
					"ENABLE_AT_IN_GROUP_MSG":      "true",
					"ENABLE_REPLY_IN_GROUP_MSG":   "true",
				})
				engine.ToolRegistry[tool.name] = tool
				engine.BillingClient = billing.NewClient(billingServer.URL, "test-token", 2*time.Second)
				engine.BillingConfig = billing.Config{Enabled: true, Timeout: 2 * time.Second, MaxOutputTokens: 2048, SafetyMultiplier: 1.2, ModelName: "test-model"}
				adapter := NewAdapter(engine)
				if exhausted {
					for i := 0; i < 3; i++ {
						if !adapter.llmErrorReplies.allow() {
							t.Fatal("could not fill error quota")
						}
					}
				}
				server := httptest.NewServer(adapter.Handler())
				defer server.Close()
				conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				event := model.OneBotEvent{SelfID: 103, PostType: "message", MessageType: messageType, UserID: 101, MessageID: 1,
					Message: json.RawMessage(`[{"type":"at","data":{"qq":"103"}},{"type":"text","data":{"text":"run tool then fail"}}]`)}
				if messageType == "group" {
					event.GroupID = 201
				}
				if err := conn.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatal(err)
				}
				var action model.OneBotAction
				for {
					if err := conn.ReadJSON(&action); err != nil {
						t.Fatalf("expected receipt after settled iteration: %v", err)
					}
					if action.Action != "get_group_info" {
						break
					}
					if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"group_id": event.GroupID, "group_name": "synthetic group"}, "echo": action.Echo}); err != nil {
						t.Fatal(err)
					}
				}
				if action.Action != "send_"+messageType+"_msg" {
					t.Fatalf("unexpected action: %+v", action)
				}
				state.mu.Lock()
				reserves, commits, releases := state.reserveCount, state.commitCount, state.releaseCount
				var cost int64
				if len(state.committedActuals) > 0 {
					cost = state.committedActuals[0]
				}
				reasons := append([]string(nil), state.releasedReasons...)
				state.mu.Unlock()
				if reserves != 2 || commits != 1 || releases != 1 || cost <= 0 || len(reasons) != 1 || reasons[0] != billing.ReasonModelFailed {
					t.Fatalf("unexpected billing: reserves=%d commits=%d releases=%d cost=%d reasons=%v", reserves, commits, releases, cost, reasons)
				}
				receipt := billing.FormatReceipt(cost, 500, 50, 10000, false)
				params := action.Params.(map[string]any)
				raw, _ := json.Marshal(params["message"])
				if !strings.Contains(string(raw), receipt) {
					t.Fatalf("missing settled receipt %q in %s", receipt, raw)
				}
				if exhausted {
					// Receipt-only group messages must not acquire @ or reply decoration.
					if params["message"] != receipt {
						t.Fatalf("expected plain receipt only, got %s", raw)
					}
				} else if !strings.Contains(string(raw), "LLM 响应失败") {
					t.Fatalf("unlimited error body should remain visible: %s", raw)
				}
				if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"message_id": 301}, "echo": action.Echo}); err != nil {
					t.Fatal(err)
				}
				// A following turn waits for receipt delivery and exposes committed history.
				event.MessageID = 2
				event.Message = json.RawMessage(`[{"type":"at","data":{"qq":"103"}},{"type":"text","data":{"text":"sentinel"}}]`)
				if err := conn.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Action != "send_"+messageType+"_msg" {
					t.Fatalf("unexpected sentinel action: %+v", action)
				}
				provider.mu.Lock()
				if len(provider.requests) != 3 {
					t.Errorf("expected 3 provider calls, got %d", len(provider.requests))
				}
				if exhausted && len(provider.requests) == 3 {
					for _, msg := range provider.requests[2].Messages {
						if msg.Role == core.RoleAssistant {
							t.Errorf("receipt-only failure wrote assistant history: %+v", msg)
						}
					}
				}
				provider.mu.Unlock()
				if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"message_id": 302}, "echo": action.Echo}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
