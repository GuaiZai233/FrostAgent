package astrbot

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/thinking"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/gorilla/websocket"
	"testing"
	"time"
)

func TestThinkingStatusCapabilityAndFinalReply(t *testing.T) {
	for _, mode := range []string{"supported", "default", "legacy", "string-capability", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			engine := newTestEngine(&mockLLMProvider{customChat: func(ctx context.Context, _ core.ChatRequest) (*core.ChatResponse, error) {
				if mode == "supported" || mode == "default" {
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "answer"}}, nil
			}})
			env := map[string]string{"STICKER_ACTIVITY": "0"}
			if mode != "default" {
				value := "true"
				if mode == "disabled" {
					value = "false"
				}
				env[thinking.EnvKey] = value
			}
			engine.Scope = newTestScopeWithEnv(t, env)
			srv, _, url := startWSTestServer(engine)
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				srv.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); srv.Close() })
			event := Event{Type: "event", EventType: "message", Platform: "telegram", MessageType: "private", UserID: "synthetic-user", MessageID: "synthetic-message", Content: "hello"}
			if mode != "legacy" {
				event.Metadata = map[string]any{"thinking_status_supported": true}
			}
			if mode == "string-capability" {
				event.Metadata["thinking_status_supported"] = "true"
			}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			var action Action
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if mode == "supported" || mode == "default" {
				if action.Action != "thinking_status" || !action.IsIntermediate || action.Echo != "hook_synthetic-message" || action.UserID != event.UserID || action.Platform != "telegram" {
					t.Fatalf("wrong status: %+v", action)
				}
				close(release)
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
			}
			if action.Action != "send_message" || action.Content != "answer" {
				t.Fatalf("wrong final reply: %+v", action)
			}
		})
	}
}
