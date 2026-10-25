package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

type runtimeInjectionProvider func(context.Context, core.ChatRequest) (*core.ChatResponse, error)

func (f runtimeInjectionProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	return f(ctx, req)
}

type injectionAdmissionObserver struct{ marker chan struct{} }

func (*injectionAdmissionObserver) IsConfigured() bool { return true }
func (*injectionAdmissionObserver) IsEnabled() bool    { return true }
func (o *injectionAdmissionObserver) DispatchIncoming(_ context.Context, msg core.IncomingMessage) error {
	if msg.Content == "queue-marker" {
		close(o.marker)
	}
	return nil
}

func TestOneBotQueuedPokeStartsIndependentTask(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			billingServer, billingState := mockAlcyoneBillingServer(t)
			defer billingServer.Close()
			var taskIDs []string
			billingState.reserveHandler = func(w http.ResponseWriter, r *http.Request) {
				var req billing.LLMReserveRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					http.Error(w, "invalid reserve request", http.StatusBadRequest)
					return
				}
				taskIDs = append(taskIDs, req.TaskID)
				billingState.reserveCount++
				json.NewEncoder(w).Encode(map[string]any{"data": billing.LLMReservationResult{ReservationID: fmt.Sprintf("synthetic-%d", billingState.reserveCount), Decision: billing.DecisionReserved, BalanceMinor: 10000}})
			}
			started, release := make(chan context.Context, 1), make(chan struct{})
			var releaseOnce sync.Once
			var calls atomic.Int32
			engine := newTestEngine(runtimeInjectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				response := &core.ChatResponse{Usage: &core.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}}
				switch calls.Add(1) {
				case 1:
					response.Message = core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "synthetic-call", Type: "function", Function: core.ToolCallFunction{Name: "synthetic-tool", Arguments: "{}"}}}}
				case 2:
					started <- ctx
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					response.Message = core.ChatMessage{Role: core.RoleAssistant, Content: "first-answer"}
				case 3:
					all := fmt.Sprint(req.Messages)
					if strings.Contains(all, "private-tool-result") || !strings.Contains(all, "first-answer") {
						t.Errorf("queued task has wrong history: %s", all)
					}
					for _, msg := range req.Messages {
						if len(msg.ToolCalls) != 0 || msg.Role == core.RoleTool {
							t.Errorf("queued task inherited completed private trajectory: %+v", msg)
						}
					}
					response.Message = core.ChatMessage{Role: core.RoleAssistant, Content: "poke-answer"}
					response.Usage = &core.Usage{PromptTokens: 300, CompletionTokens: 30, TotalTokens: 330}
				default:
					t.Error("unexpected model call")
					response.Message = core.ChatMessage{Role: core.RoleAssistant, Content: "unexpected-answer"}
				}
				return response, nil
			}))
			engine.ToolRegistry["synthetic-tool"] = &mockTestTool{name: "synthetic-tool", result: "private-tool-result"}
			engine.Scope = newTestScopeWithEnv(t, map[string]string{"ENABLE_POKE": "true", "POKE_COOLDOWN_SECONDS": "0", "ENABLE_PROACTIVE_REPLY": "false", "PROACTIVE_REPLY_PROBABILITY": "0"})
			engine.BillingClient = billing.NewClient(billingServer.URL, "synthetic-token", time.Second)
			engine.BillingConfig = billing.Config{Enabled: true, Timeout: time.Second, MaxOutputTokens: 100, SafetyMultiplier: 1.2}
			observer := &injectionAdmissionObserver{marker: make(chan struct{})}
			adapter := NewAdapter(engine)
			adapter.SetIncomingDispatcher(observer)
			server := httptest.NewServer(adapter.Handler())
			defer server.Close()
			defer releaseOnce.Do(func() { close(release) })
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var writes sync.Mutex
			write := func(value any) error { writes.Lock(); defer writes.Unlock(); return conn.WriteJSON(value) }
			replies := make(chan string, 4)
			go func() {
				for {
					var action model.OneBotAction
					if conn.ReadJSON(&action) != nil {
						return
					}
					if write(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 22, "group_name": "synthetic-room"}}) != nil {
						return
					}
					if action.Action != "get_group_info" {
						raw, _ := json.Marshal(action.Params)
						replies <- string(raw)
					}
				}
			}()
			first := model.OneBotEvent{PostType: "message", SelfID: 99, UserID: 11, MessageID: 1, MessageType: kind, Message: json.RawMessage(`[{"type":"at","data":{"qq":"99"}},{"type":"text","data":{"text":"first-input"}}]`)}
			if kind == "group" {
				first.GroupID = 20
			}
			if err := write(first); err != nil {
				t.Fatal(err)
			}
			var oldCtx context.Context
			select {
			case oldCtx = <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("first task did not reach the model after its tool")
			}
			if err := write(model.OneBotEvent{PostType: "notice", NoticeType: "notify", SubType: "poke", SelfID: 99, TargetID: 99, UserID: 11, GroupID: first.GroupID}); err != nil {
				t.Fatal(err)
			}
			// The same socket processes this unaddressed group marker only after
			// reserving the poke turn, while the predecessor is still running.
			if err := write(model.OneBotEvent{PostType: "message", MessageType: "group", SelfID: 99, GroupID: 21, UserID: 12, MessageID: 2, Message: json.RawMessage(`"queue-marker"`)}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-observer.marker:
			case <-time.After(3 * time.Second):
				t.Fatal("poke admission did not complete")
			}
			if oldCtx.Err() != nil || calls.Load() != 2 {
				t.Fatalf("poke interrupted or overlapped its predecessor: err=%v calls=%d", oldCtx.Err(), calls.Load())
			}
			releaseOnce.Do(func() { close(release) })
			for _, want := range []struct{ answer, usage string }{{"first-answer", "输入: 200, 输出: 20"}, {"poke-answer", "输入: 300, 输出: 30"}} {
				select {
				case got := <-replies:
					if !strings.Contains(got, want.answer) || !strings.Contains(got, want.usage) {
						t.Fatalf("want %s with separate usage %s, got %s", want.answer, want.usage, got)
					}
				case <-time.After(3 * time.Second):
					t.Fatalf("missing %s", want.answer)
				}
			}
			billingState.mu.Lock()
			defer billingState.mu.Unlock()
			if len(taskIDs) != 3 || taskIDs[0] != taskIDs[1] || taskIDs[1] == taskIDs[2] || billingState.commitCount != 3 || billingState.releaseCount != 0 {
				t.Fatalf("tasks were not billed independently: tasks=%v commit=%d release=%d", taskIDs, billingState.commitCount, billingState.releaseCount)
			}
		})
	}
}

func TestOneBotInjectionEarlyRejectionRetainsBillingReceipt(t *testing.T) {
	for _, kind := range []string{"oversize", "vision-balance"} {
		t.Run(kind, func(t *testing.T) {
			alcyone, state := mockAlcyoneBillingServer(t)
			defer alcyone.Close()
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var calls atomic.Int32
			engine := newTestEngine(runtimeInjectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
				calls.Add(1)
				close(started)
				<-release
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "discarded-old-answer"}, Usage: &core.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}}, nil
			}))
			engine.BillingClient = billing.NewClient(alcyone.URL, "synthetic-token", time.Second)
			engine.BillingConfig = billing.Config{Enabled: true, Timeout: time.Second, MaxOutputTokens: 100, SafetyMultiplier: 1.2}
			if kind == "vision-balance" {
				engine.VisionProvider = engine.Provider
				state.balanceHandler = func(w http.ResponseWriter, _ *http.Request) {
					json.NewEncoder(w).Encode(map[string]any{"data": billing.BalanceResult{Exists: true, BalanceMinor: 0}})
				}
			}
			srv, url := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			write := func(id int32, raw json.RawMessage) {
				t.Helper()
				if err := conn.WriteJSON(model.OneBotEvent{PostType: "message", MessageType: "private", UserID: 11, MessageID: id, Message: raw}); err != nil {
					t.Fatal(err)
				}
			}
			write(1, json.RawMessage(`"first"`))
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("first request did not start")
			}
			raw, _ := json.Marshal(strings.Repeat("x", 30001))
			want := "单条消息长度过长"
			if kind == "vision-balance" {
				raw = []byte(`[{"type":"image","data":{"url":"https://example.invalid/synthetic.png"}}]`)
				want = "余额不足"
			}
			write(2, raw)
			read := func() string {
				t.Helper()
				conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var action model.OneBotAction
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(action.Params)
				if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo}); err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			if got := read(); !strings.Contains(got, want) {
				t.Fatalf("expected rejection, got %s", got)
			}
			releaseOnce.Do(func() { close(release) })
			if got := read(); !strings.Contains(got, "输入: 100, 输出: 50") || strings.Contains(got, "discarded-old-answer") {
				t.Fatalf("missing inherited receipt: %s", got)
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if calls.Load() != 1 || state.commitCount != 1 || state.releaseCount != 0 {
				t.Fatalf("unexpected billing: calls=%d commit=%d release=%d", calls.Load(), state.commitCount, state.releaseCount)
			}
		})
	}
}

type injectionWriteGate struct {
	enabled atomic.Bool
	started chan struct{}
	release chan struct{}
	once    sync.Once
}
type injectionGatedConn struct {
	net.Conn
	gate *injectionWriteGate
}

func (c *injectionGatedConn) Write(p []byte) (int, error) {
	if c.gate.enabled.Load() {
		c.gate.once.Do(func() { close(c.gate.started) })
		<-c.gate.release
	}
	return c.Conn.Write(p)
}

type injectionGatedListener struct {
	net.Listener
	gate *injectionWriteGate
}

func (l injectionGatedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &injectionGatedConn{c, l.gate}, nil
}

func TestOneBotInjectionDoesNotWaitForBlockedSocketWrite(t *testing.T) {
	for _, phase := range []string{"tool", "final"} {
		t.Run(phase, func(t *testing.T) {
			first, allowModel, next := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			engine := newTestEngine(runtimeInjectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
				if calls.Add(1) == 1 {
					close(first)
					<-allowModel
					if phase == "tool" {
						return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "send", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{"messages":[{"type":"plain","text":"old-inflight"}]}`}}}}}, nil
					}
					return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "old-inflight"}}, nil
				}
				close(next)
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "new-answer"}}, nil
			}))
			engine.ToolRegistry["send_message"] = tools.SendMsgTool()
			gate := &injectionWriteGate{started: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			srv := httptest.NewUnstartedServer(NewAdapter(engine).Handler())
			srv.Listener = injectionGatedListener{srv.Listener, gate}
			srv.Start()
			defer srv.Close()
			defer releaseOnce.Do(func() { close(gate.release) })
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			send := func(id int32, text string) {
				raw, _ := json.Marshal(text)
				if err := conn.WriteJSON(model.OneBotEvent{PostType: "message", MessageType: "private", UserID: 11, MessageID: id, Message: raw}); err != nil {
					t.Fatal(err)
				}
			}
			send(1, "first")
			select {
			case <-first:
			case <-time.After(3 * time.Second):
				t.Fatal("model not started")
			}
			gate.enabled.Store(true)
			close(allowModel)
			select {
			case <-gate.started:
			case <-time.After(3 * time.Second):
				t.Fatal("socket write not reached")
			}
			send(2, "second")
			select {
			case <-next:
			case <-time.After(3 * time.Second):
				t.Fatal("injection waited for blocked socket")
			}
			releaseOnce.Do(func() { close(gate.release) })
			for i := 0; i < 2; i++ {
				conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var action model.OneBotAction
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestOneBotInjectionPrefersConfirmedACKOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		responses := make(chan oneBotAPIResponse, 1)
		responses <- oneBotAPIResponse{Status: "ok", Data: json.RawMessage(`{"message_id":11}`)}
		response, err := waitActionResponse(ctx, context.Background(), responses, nil, func() {})
		if err != nil || response.Status != "ok" {
			t.Fatalf("confirmed ACK lost: %+v %v", response, err)
		}
	}
}

func TestOneBotInjectionDoesNotWaitForOldToolACK(t *testing.T) {
	var calls int
	var mu sync.Mutex
	engine := newTestEngine(runtimeInjectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "send-first", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{"messages":[{"type":"plain","text":"inflight-message"}]}`}}}}}, nil
		}
		if !strings.Contains(fmt.Sprint(req.Messages), "outcome unknown") {
			t.Errorf("missing unknown delivery status: %+v", req.Messages)
		}
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "resumed-without-ack"}}, nil
	}))
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	srv, url := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	send := func(id int32, text string) {
		raw, _ := json.Marshal(text)
		if err := conn.WriteJSON(model.OneBotEvent{PostType: "message", MessageType: "private", UserID: 11, MessageID: id, Message: raw}); err != nil {
			t.Fatal(err)
		}
	}
	send(1, "first")
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var intermediate model.OneBotAction
	if err := conn.ReadJSON(&intermediate); err != nil {
		t.Fatal(err)
	}
	// Deliberately leave this action unacknowledged, then inject another input.
	send(2, "second")
	var final model.OneBotAction
	if err := conn.ReadJSON(&final); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(final.Params)
	if !strings.Contains(string(data), "resumed-without-ack") {
		t.Fatalf("unexpected final: %s", data)
	}
	if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": final.Echo, "data": map[string]any{"message_id": 22}}); err != nil {
		t.Fatal(err)
	}
}

func TestOneBotRuntimeInjectionAtAliasAndPrivate(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		t.Run(kind, func(t *testing.T) {
			started := make(chan context.Context, 1)
			release, lateDone := make(chan struct{}), make(chan struct{})
			provider := runtimeInjectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				last := fmt.Sprint(req.Messages[len(req.Messages)-1].Content)
				answer := "new-answer"
				switch {
				case strings.HasPrefix(last, "User Message: old-input"):
					started <- ctx
					<-release
					close(lateDone)
					answer = "stale-answer"
				case strings.HasPrefix(last, "User Message: other-input"):
					answer = "other-answer"
					if kind == "group" && !strings.Contains(fmt.Sprint(req.Messages), "old-input") {
						t.Error("approved public input not shared")
					}
				default:
					if !strings.Contains(fmt.Sprint(req.Messages), "old-input") {
						t.Error("injected run lost old input")
					}
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: answer}}, nil
			})
			engine := newTestEngine(provider)
			engine.Scope = newTestScopeWithEnv(t, map[string]string{"BOT_ALIASES": "synthetic-bot", "ENABLE_PROACTIVE_REPLY": "false"})
			srv, url := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var writes sync.Mutex
			write := func(value any) error { writes.Lock(); defer writes.Unlock(); return conn.WriteJSON(value) }
			replies := make(chan string, 10)
			go func() {
				for {
					var action model.OneBotAction
					if conn.ReadJSON(&action) != nil {
						return
					}
					if action.Action == "get_group_info" {
						write(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_name": "synthetic-room"}})
						continue
					}
					data, _ := json.Marshal(action.Params)
					write(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 999}})
					replies <- string(data)
				}
			}()
			send := func(id int32, sender int64, text string, at bool) {
				t.Helper()
				segments := []map[string]any{{"type": "text", "data": map[string]any{"text": text}}}
				if kind == "group" && at {
					segments = append(segments, map[string]any{"type": "at", "data": map[string]any{"qq": "99"}})
				}
				raw, _ := json.Marshal(segments)
				if err := write(model.OneBotEvent{SelfID: 99, PostType: "message", MessageType: kind, GroupID: 20, UserID: sender, MessageID: id, Message: raw}); err != nil {
					t.Fatal(err)
				}
			}
			read := func(want string) {
				t.Helper()
				select {
				case got := <-replies:
					if !strings.Contains(got, want) {
						t.Fatalf("want %s got %s", want, got)
					}
				case <-time.After(4 * time.Second):
					t.Fatalf("no %s", want)
				}
			}
			send(1, 11, "old-input", true)
			var oldCtx context.Context
			select {
			case oldCtx = <-started:
			case <-time.After(4 * time.Second):
				t.Fatal("old run not started")
			}
			send(2, 12, "other-input", true)
			read("other-answer")
			if oldCtx.Err() != nil {
				t.Fatal("different sender interrupted old run")
			}
			send(3, 11, "synthetic-bot new-input", false)
			read("new-answer")
			if oldCtx.Err() == nil {
				t.Fatal("same sender did not interrupt")
			}
			close(release)
			<-lateDone
			select {
			case text := <-replies:
				t.Fatalf("stale reply: %s", text)
			default:
			}
		})
	}
}
