package onebot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsclient"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/gorilla/websocket"
)

func waitOneBotClient(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("OneBot client did not stop promptly")
	}
}

func TestOneBotForwardConnectionRoundTripAndScopeCancellation(t *testing.T) {
	upstream := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-onebot-token" {
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
	adapter.ConfigureConnection(wsclient.Config{Mode: wsclient.Forward, URL: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "synthetic-onebot-token"})
	done := make(chan struct{})
	go func() { defer close(done); adapter.RunClient(context.Background()) }()
	t.Cleanup(func() {
		engine.Scope.Cancel()
		adapter.CloseConnections()
		waitOneBotClient(t, done)
		engine.Scope.Wait()
	})
	var peer *websocket.Conn
	select {
	case peer = <-upstream:
	case <-time.After(3 * time.Second):
		t.Fatal("FA did not initiate the OneBot connection")
	}
	defer peer.Close()
	_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
	// Small identifiers below are synthetic protocol fixtures, not user accounts.
	event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 11, UserID: 22, MessageID: 33,
		Message: json.RawMessage(`[{"type":"text","data":{"text":"synthetic-question"}}]`)}
	if err := peer.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	var action model.OneBotAction
	if err := peer.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_private_msg" {
		t.Fatalf("incoming event did not produce an outbound action: %s", action.Action)
	}
	encoded, _ := json.Marshal(action.Params)
	if !strings.Contains(string(encoded), "synthetic-answer") {
		t.Fatalf("unexpected reply: %s", encoded)
	}
	if err := peer.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 44}}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Send(context.Background(), core.OutgoingMessage{Platform: "onebot", MessageType: "private", TargetID: "22", Content: "synthetic-push"}); err != nil {
		t.Fatal(err)
	}
	if err := peer.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(action.Params)
	if !strings.Contains(string(encoded), "synthetic-push") {
		t.Fatal("server role upstream did not receive an FA-initiated push")
	}
	duplicate := make(chan struct{})
	go func() { defer close(duplicate); adapter.RunClient(context.Background()) }()
	waitOneBotClient(t, duplicate)
	engine.Scope.Cancel()
	waitOneBotClient(t, done)
	adapter.mu.RLock()
	remaining := len(adapter.conns)
	adapter.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("canceled client left %d registered connections", remaining)
	}
}

func TestOneBotConnectionModeAdmission(t *testing.T) {
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

func TestOneBotCloseConnectionsCancelsHandshakeAndPreventsRestart(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	adapter := NewAdapter(nil)
	adapter.ConfigureConnection(wsclient.Config{Mode: wsclient.Forward, URL: "ws" + strings.TrimPrefix(server.URL, "http")})
	done := make(chan struct{})
	go func() { defer close(done); adapter.RunClient(context.Background()) }()
	defer adapter.CloseConnections()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handshake did not start")
	}
	adapter.CloseConnections()
	waitOneBotClient(t, done)
	restarted := make(chan struct{})
	go func() { defer close(restarted); adapter.RunClient(context.Background()) }()
	waitOneBotClient(t, restarted)
}
