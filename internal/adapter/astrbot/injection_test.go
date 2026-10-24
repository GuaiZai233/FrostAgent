package astrbot

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/core"
	"FrostAgent/internal/security"
	"FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

type runtimeInjectionProvider func(context.Context, core.ChatRequest) (*core.ChatResponse, error)

func (f runtimeInjectionProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	return f(ctx, req)
}

func TestAstrBotRuntimeInjectionTerminalAndIndependentSender(t *testing.T) {
	for _, scenario := range []string{"group", "private", "filtered-alias"} {
		t.Run(scenario, func(t *testing.T) {
			kind := scenario
			if scenario == "filtered-alias" {
				kind = "group"
			}
			started := make(chan context.Context, 1)
			release, lateDone := make(chan struct{}), make(chan struct{})
			engine := newTestEngine(runtimeInjectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
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
				default:
					if !strings.Contains(fmt.Sprint(req.Messages), "old-input") {
						t.Error("injection lost old input")
					}
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: answer}}, nil
			}))
			engine.Scope = newTestScopeWithEnv(t, map[string]string{"BOT_ALIASES": "synthetic-bot", "ENABLE_PROACTIVE_REPLY": "false"})
			if scenario == "filtered-alias" {
				engine.Security = security.NewController(t.TempDir())
				engine.Security.SetMode(security.ControlModeAggressive)
				engine.Security.SetClassifier(&mockClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
					if strings.Contains(input.Content, "malicious_filter_payload") {
						return security.ClassificationResult{Category: security.RiskCategoryPromptInjection, RiskLevel: security.RiskLevelHigh, Reason: "synthetic filter test"}, nil
					}
					return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
				}})
			}
			srv, _, url := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			send := func(id, user, text string, at bool) {
				t.Helper()
				event := Event{Type: "event", EventType: "message", MessageID: id, UserID: user, Platform: "synthetic", MessageType: kind, Content: text, IsAt: at}
				if kind == "group" {
					event.GroupID = "synthetic-room"
				}
				if err := conn.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
			}
			read := func() Action {
				t.Helper()
				conn.SetReadDeadline(time.Now().Add(4 * time.Second))
				var action Action
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				return action
			}
			send("old", "synthetic-A", "old-input", true)
			var oldCtx context.Context
			select {
			case oldCtx = <-started:
			case <-time.After(4 * time.Second):
				t.Fatal("old model not started")
			}
			send("other", "synthetic-B", "other-input", true)
			if action := read(); action.Content != "other-answer" {
				t.Fatalf("unexpected other output: %+v", action)
			}
			if oldCtx.Err() != nil {
				t.Fatal("other sender interrupted A")
			}
			newInput := "synthetic-bot new-input"
			if scenario == "filtered-alias" {
				newInput += " malicious_filter_payload"
			}
			send("new", "synthetic-A", newInput, false)
			oldTerminal, newReply := false, false
			for i := 0; i < 2; i++ {
				action := read()
				switch action.Echo {
				case "reply_old":
					oldTerminal = action.Action == "noop" && action.SuppressLLM
				case "reply_new":
					newReply = action.Content == "new-answer"
				default:
					t.Fatalf("unexpected action: %+v", action)
				}
			}
			if !oldTerminal || !newReply || oldCtx.Err() == nil {
				t.Fatalf("handoff failed: terminal=%v reply=%v canceled=%v", oldTerminal, newReply, oldCtx.Err())
			}
			close(release)
			<-lateDone
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

func TestAstrBotInjectionDoesNotWaitForBlockedSocketWrite(t *testing.T) {
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
			send := func(id, text string) {
				if err := conn.WriteJSON(Event{Type: "event", EventType: "message", MessageType: "private", Platform: "synthetic", UserID: "synthetic-sender", MessageID: id, Content: text}); err != nil {
					t.Fatal(err)
				}
			}
			send("first", "first")
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
			send("next", "second")
			select {
			case <-next:
			case <-time.After(3 * time.Second):
				t.Fatal("injection waited for blocked socket")
			}
			releaseOnce.Do(func() { close(gate.release) })
			oldVisible, newVisible, terminal := false, false, false
			for i := 0; i < 3; i++ {
				conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var action Action
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				oldVisible = oldVisible || action.Content == "old-inflight"
				newVisible = newVisible || action.Content == "new-answer"
				terminal = terminal || (action.Action == "noop" && action.SuppressLLM)
			}
			if !oldVisible || !newVisible || !terminal {
				t.Fatalf("in-flight transport facts lost: old=%v new=%v terminal=%v", oldVisible, newVisible, terminal)
			}
			session := engine.SessionManager.GetOrCreate(sessionKey(Event{MessageType: "private", Platform: "synthetic", UserID: "synthetic-sender"}))
			deadline := time.Now().Add(3 * time.Second)
			for !strings.Contains(fmt.Sprint(session.Snapshot()), "old-inflight") && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !strings.Contains(fmt.Sprint(session.Snapshot()), "old-inflight") {
				t.Fatal("confirmed in-flight write disappeared from public history")
			}
		})
	}
}
