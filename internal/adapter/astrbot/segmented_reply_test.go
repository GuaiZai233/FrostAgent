package astrbot

import (
	"context"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/gorilla/websocket"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type segmentHandoffGate struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	content string
}

type segmentHandoffConn struct {
	net.Conn
	gate *segmentHandoffGate
}

func (c *segmentHandoffConn) Write(data []byte) (int, error) {
	content := c.gate.content
	if content == "" {
		content = "second"
	}
	if strings.Contains(string(data), `"content":"`+content+`"`) {
		c.gate.once.Do(func() { close(c.gate.started) })
		<-c.gate.release
	}
	return c.Conn.Write(data)
}

func TestSegmentedReplyLateFirstConfirmationAcrossTwoInterruptions(t *testing.T) {
	requests := make(chan core.ChatRequest, 2)
	var calls atomic.Int32
	engine := newTestEngine(runtimeInjectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		switch calls.Add(1) {
		case 1:
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "first" + replyformat.SegmentMark + "unsent"}}, nil
		case 2:
			requests <- req
			<-ctx.Done()
			return nil, ctx.Err()
		default:
			requests <- req
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "third-answer"}}, nil
		}
	}))
	engine.Scope = newTestScopeWithEnv(t, map[string]string{replyformat.EnvKey: "true", "STICKER_ACTIVITY": "0"})
	gate := &segmentHandoffGate{started: make(chan struct{}), release: make(chan struct{}), content: "first"}
	var release sync.Once
	server := httptest.NewUnstartedServer(NewAdapter(engine).Handler())
	server.Listener = segmentHandoffListener{Listener: server.Listener, gate: gate}
	server.Start()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		release.Do(func() { close(gate.release) })
		conn.Close()
		engine.Scope.Cancel()
		engine.Scope.Wait()
		server.Close()
	})
	event := Event{Type: "event", EventType: "message", Platform: "synthetic", MessageType: "private", UserID: "synthetic-user", MessageID: "synthetic-old", Content: "old-input"}
	send := func(id, input string) {
		t.Helper()
		event.MessageID, event.Content = id, input
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
	}
	readRequest := func() []core.ChatMessage {
		t.Helper()
		select {
		case req := <-requests:
			var messages []core.ChatMessage
			for _, message := range req.Messages {
				if message.Role != core.RoleSystem {
					messages = append(messages, message)
				}
			}
			return messages
		case <-time.After(3 * time.Second):
			t.Fatal("successor did not reach the model")
			return nil
		}
	}
	send("synthetic-old", "old-input")
	select {
	case <-gate.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first write was not blocked")
	}
	send("synthetic-next", "next-input")
	second := readRequest()
	if len(second) != 2 || second[0].Role != core.RoleUser || second[1].Role != core.RoleUser {
		t.Fatalf("successor saw an unconfirmed first segment: %+v", second)
	}
	release.Do(func() { close(gate.release) })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		var action Action
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if action.Action == "noop" {
			seen["old-terminal"] = true
		} else {
			seen[action.Content] = true
		}
	}
	if !seen["first"] || !seen["old-terminal"] {
		t.Fatalf("unexpected old delivery: %v", seen)
	}
	// A socket read can precede the server's confirmation callback. Observe its
	// public commit before issuing the second interruption, avoiding timing luck.
	session := engine.SessionManager.GetOrCreate(sessionKey(event))
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(fmt.Sprint(session.Snapshot()), "first") {
		if time.Now().After(deadline) {
			t.Fatal("late first segment was not recorded")
		}
		time.Sleep(time.Millisecond)
	}
	send("synthetic-third", "third-input")
	third := readRequest()
	if len(third) != 4 || third[0].Role != core.RoleUser || !strings.Contains(fmt.Sprint(third[0].Content), "old-input") || third[1].Role != core.RoleAssistant || third[1].Content != "first" || third[2].Role != core.RoleUser || !strings.Contains(fmt.Sprint(third[2].Content), "next-input") || third[3].Role != core.RoleUser || !strings.Contains(fmt.Sprint(third[3].Content), "third-input") {
		t.Fatalf("late first segment lost, duplicated or before its input: %+v", third)
	}
	if len(second) != 2 || second[0].Role != core.RoleUser || second[1].Role != core.RoleUser {
		t.Fatal("late confirmation mutated an already-issued model request")
	}
	for i := 0; i < 2; i++ {
		var action Action
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if action.Action == "noop" {
			seen["next-terminal"] = true
		} else {
			seen[action.Content] = true
		}
	}
	if !seen["third-answer"] || !seen["next-terminal"] || seen["unsent"] {
		t.Fatalf("unexpected successor delivery: %v", seen)
	}
	waitAstrBotTurnCompletion(t, engine, event)
}

type segmentHandoffListener struct {
	net.Listener
	gate *segmentHandoffGate
}

func (l segmentHandoffListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &segmentHandoffConn{Conn: conn, gate: l.gate}, nil
}

func TestSegmentedReplySuccessorGetsOnlyConfirmedPrefix(t *testing.T) {
	requests := make(chan core.ChatRequest, 1)
	var calls atomic.Int32
	engine := newTestEngine(runtimeInjectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		text := "first" + replyformat.SegmentMark + "second" + replyformat.SegmentMark + "third"
		if calls.Add(1) > 1 {
			requests <- req
			text = "new-answer"
		}
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: text}}, nil
	}))
	engine.Scope = newTestScopeWithEnv(t, map[string]string{replyformat.EnvKey: "true", "STICKER_ACTIVITY": "0"})
	gate := &segmentHandoffGate{started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	server := httptest.NewUnstartedServer(NewAdapter(engine).Handler())
	server.Listener = segmentHandoffListener{Listener: server.Listener, gate: gate}
	server.Start()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		release.Do(func() { close(gate.release) })
		conn.Close()
		engine.Scope.Cancel()
		engine.Scope.Wait()
		server.Close()
	})
	event := Event{Type: "event", EventType: "message", Platform: "synthetic", MessageType: "private", UserID: "synthetic-user", MessageID: "synthetic-old", Content: "old-input"}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.started:
	case <-time.After(3 * time.Second):
		t.Fatal("second write was not reached")
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var first Action
	if err := conn.ReadJSON(&first); err != nil || first.Content != "first" {
		t.Fatalf("missing confirmed prefix: %+v / %v", first, err)
	}
	event.MessageID, event.Content = "synthetic-next", "next-input"
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-requests:
		var dialogue []core.ChatMessage
		for _, message := range req.Messages {
			if message.Role != core.RoleSystem {
				dialogue = append(dialogue, message)
			}
		}
		if len(dialogue) != 3 || dialogue[0].Role != core.RoleUser || !strings.Contains(fmt.Sprint(dialogue[0].Content), "old-input") || dialogue[1].Role != core.RoleAssistant || dialogue[1].Content != "first" || !strings.Contains(fmt.Sprint(dialogue[2].Content), "next-input") {
			t.Fatalf("successor inherited unsent, duplicate or misordered text: %+v", dialogue)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("successor waited for blocked transport")
	}
	release.Do(func() { close(gate.release) })
	// The admitted second write may complete late; third must never be sent.
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		var action Action
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if action.Action == "noop" {
			seen["terminal"] = true
		} else {
			seen[action.Content] = true
		}
	}
	if !seen["second"] || !seen["new-answer"] || !seen["terminal"] || seen["third"] {
		t.Fatalf("unexpected late deliveries: %v", seen)
	}
	waitAstrBotTurnCompletion(t, engine, event)
	deadline := time.Now().Add(3 * time.Second)
	for {
		var answers []string
		for _, message := range engine.SessionManager.GetOrCreate(sessionKey(event)).Snapshot() {
			if message.Role == "assistant" {
				answers = append(answers, fmt.Sprint(message.Content))
			}
		}
		if len(answers) == 2 && answers[0] == "first\n\nsecond" && answers[1] == "new-answer" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("duplicate or unconfirmed public replies: %q", answers)
		}
		time.Sleep(time.Millisecond)
	}
}

// Only the second reply frame fails; the HTTP upgrade is left untouched.
type segmentFailureConn struct {
	net.Conn
	writes int
}

func (c *segmentFailureConn) Write(data []byte) (int, error) {
	if strings.Contains(string(data), `"action":"send_message"`) {
		c.writes++
		if c.writes == 2 {
			return 0, fmt.Errorf("synthetic second-segment write failure")
		}
	}
	return c.Conn.Write(data)
}

type segmentFailureListener struct {
	net.Listener
}

func (l *segmentFailureListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &segmentFailureConn{Conn: conn}, nil
}

func TestSegmentedReplyPartialWriteFailure(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "first" + replyformat.SegmentMark + "second" + replyformat.SegmentMark + "third"}}}})
	engine.Scope = newTestScopeWithEnv(t, map[string]string{replyformat.EnvKey: "true", "STICKER_ACTIVITY": "0"})
	server := httptest.NewUnstartedServer(NewAdapter(engine).Handler())
	listener := &segmentFailureListener{Listener: server.Listener}
	server.Listener = listener
	server.Start()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); server.Close() })
	event := Event{Type: "event", EventType: "message", Platform: "synthetic", MessageType: "private", UserID: "synthetic-user", MessageID: "synthetic-message", Content: "hello"}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var action Action
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Content != "first" || !action.IsIntermediate {
		t.Fatalf("unexpected prefix: %+v", action)
	}
	waitAstrBotTurnCompletion(t, engine, event)
	session := engine.SessionManager.GetOrCreate(sessionKey(event))
	history := session.Snapshot()
	if len(history) != 2 || history[1].Content != "first" {
		t.Fatalf("unwritten suffix in history: %+v", history)
	}
	conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("unexpected suffix after write failure")
	}
	epoch := session.Epoch()
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	event.MessageType = "group"
	recordPartialReply(session, engine, epoch, event, "old delivered prefix")
	if len(session.Snapshot()) != 0 || len(session.SnapshotGroupContext(20, 4096, "").RecentStructuredMessages) != 0 {
		t.Fatal("old prefix restored after reset")
	}
}

func TestSegmentedReplyDelivery(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
			if err != nil {
				t.Fatal(err)
			}
			if err := config.Update(replyformat.EnvKey, fmt.Sprint(enabled), false); err != nil {
				t.Fatal(err)
			}
			if err := config.Update("STICKER_ACTIVITY", "0", false); err != nil {
				t.Fatal(err)
			}
			output := "first" + replyformat.SegmentMark + "second\n\n`" + replyformat.SegmentMark + "`"
			engine := newTestEngine(&mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: output}}}})
			engine.Scope = runtimescope.New(config, nil, nil)
			srv, _, url := startWSTestServer(engine)
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				srv.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); srv.Close() })
			event := Event{Type: "event", EventType: "message", Platform: "astrbot", MessageType: "private", MessageID: "synthetic-msg", UserID: "synthetic-user", Content: "hello"}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			want := []string{"firstsecond\n\n`" + replyformat.SegmentMark + "`"}
			if enabled {
				want = []string{"first", "second", "`" + replyformat.SegmentMark + "`"}
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			for index, part := range want {
				var action Action
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Content != part || action.IsIntermediate != (index < len(want)-1) {
					t.Fatalf("part %d: %+v", index, action)
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				history := engine.SessionManager.GetOrCreate(sessionKey(event)).Snapshot()
				if len(history) == 2 && history[1].Content == strings.Join(want, "\n\n") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("history %+v", history)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
