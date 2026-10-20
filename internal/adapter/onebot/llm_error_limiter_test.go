package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
