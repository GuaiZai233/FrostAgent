package astrbot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsclient"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/gorilla/websocket"
)

func waitAstrBotClient(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("AstrBot client did not stop promptly")
	}
}

func TestAstrBotForwardConnectionRoundTripAndClose(t *testing.T) {
	upstream := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-astrbot-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		upgrader := websocket.Upgrader{}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			upstream <- connection
		}
	}))
	defer server.Close()
	provider := &mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "synthetic-answer"}}}}
	engine := newTestEngine(provider)
	engine.Scope = runtimescope.New(nil, nil, nil)
	adapter := NewAdapter(engine)
	adapter.ConfigureConnection(wsclient.Config{Mode: wsclient.Forward, URL: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "synthetic-astrbot-token"})
	done := make(chan struct{})
	go func() { defer close(done); adapter.RunClient(context.Background()) }()
	t.Cleanup(func() {
		adapter.CloseConnections()
		engine.Scope.Cancel()
		waitAstrBotClient(t, done)
		engine.Scope.Wait()
	})
	var peer *websocket.Conn
	select {
	case peer = <-upstream:
	case <-time.After(3 * time.Second):
		t.Fatal("FA did not initiate the AstrBot connection")
	}
	defer peer.Close()
	_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
	event := Event{Type: "event", EventType: "message", MessageType: "private", UserID: "synthetic-user", MessageID: "synthetic-message",
		Platform: "astrbot", Content: "synthetic-question"}
	if err := peer.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	var action Action
	if err := peer.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_message" || action.UserID != event.UserID || action.Content != "synthetic-answer" {
		t.Fatalf("incoming event did not produce an outbound action: %#v", action)
	}
	if err := adapter.Send(context.Background(), core.OutgoingMessage{Platform: "astrbot", MessageType: "private", TargetID: "synthetic-user", Content: "synthetic-push"}); err != nil {
		t.Fatal(err)
	}
	if err := peer.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Content != "synthetic-push" {
		t.Fatal("server role upstream did not receive an FA-initiated push")
	}
	duplicate := make(chan struct{})
	go func() { defer close(duplicate); adapter.RunClient(context.Background()) }()
	waitAstrBotClient(t, duplicate)
	adapter.CloseConnections()
	waitAstrBotClient(t, done)
	adapter.mu.RLock()
	remaining := len(adapter.conns)
	adapter.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("closed client left %d registered connections", remaining)
	}
	restarted := make(chan struct{})
	go func() { defer close(restarted); adapter.RunClient(context.Background()) }()
	waitAstrBotClient(t, restarted)
}

func TestAstrBotConnectionModeAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, mode, query string
		allowed           bool
	}{
		{"default reverse", "", "", true},
		{"explicit reverse", wsclient.Reverse, "", true},
		{"forward blocks upstream", wsclient.Forward, "", false},
		{"forward retains direct chat", wsclient.Forward, "?mock=true", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := NewAdapter(nil)
			adapter.ConfigureConnection(wsclient.Config{Mode: tc.mode, URL: "ws://synthetic.invalid/"})
			server := httptest.NewServer(adapter.Handler())
			defer server.Close()
			defer adapter.CloseConnections()
			connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+tc.query, nil)
			if tc.allowed {
				if err != nil {
					t.Fatalf("expected inbound connection: %v", err)
				}
				_ = connection.Close()
			} else {
				if err == nil {
					_ = connection.Close()
					t.Fatal("forward mode admitted a reverse upstream")
				}
				if response == nil || response.StatusCode != http.StatusConflict {
					t.Fatalf("expected conflict, got %v", response)
				}
				_ = response.Body.Close()
			}
		})
	}
}
