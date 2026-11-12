package onebot

import (
	"context"
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/thinking"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/gorilla/websocket"
	"path/filepath"
	"testing"
	"time"
)

func TestThinkingStatusDeliveryDoesNotGateReply(t *testing.T) {
	for _, mode := range []string{"ok", "rejected", "no-ack", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			provider := &mockLLMProvider{customChat: func(ctx context.Context, _ core.ChatRequest) (*core.ChatResponse, error) {
				if mode != "disabled" {
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "answer"}}, nil
			}}
			engine := newTestEngine(provider)
			cfg, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "disabled" {
				if err := cfg.Update(thinking.EnvKey, "true", false); err != nil {
					t.Fatal(err)
				}
			}
			engine.Scope = runtimescope.New(cfg, nil, nil)
			srv, url := startWSTestServer(engine)
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				srv.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); srv.Close() })
			// Small synthetic IDs are not real account identifiers.
			event := model.OneBotEvent{PostType: "message", SelfID: 7, UserID: 42, MessageID: 1, MessageType: "private", Message: json.RawMessage(`[{"type":"text","data":{"text":"hello"}}]`)}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			var action model.OneBotAction
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if mode != "disabled" {
				if action.Action != "set_input_status" {
					t.Fatalf("first action=%s", action.Action)
				}
				params := action.Params.(map[string]any)
				if params["user_id"] != float64(42) || params["event_type"] != float64(1) {
					t.Fatalf("wrong status: %v", params)
				}
				if mode != "no-ack" {
					status, code := "ok", 0
					if mode == "rejected" {
						status, code = "failed", 1404
					}
					if err := conn.WriteJSON(map[string]any{"status": status, "retcode": code, "echo": action.Echo, "data": nil}); err != nil {
						t.Fatal(err)
					}
				}
				close(release)
				// A missing status ACK must not impose its 2-second timeout on final delivery.
				conn.SetReadDeadline(time.Now().Add(time.Second))
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
			}
			if action.Action != "send_private_msg" {
				t.Fatalf("final action=%s", action.Action)
			}
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 2}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestThinkingStatusSkipsGroupAndMock(t *testing.T) {
	for _, tc := range []struct {
		mode string
		mock bool
	}{{"group", false}, {"private", true}} {
		c := &wsConnection{mock: tc.mock}
		c.startThinkingStatus(model.OneBotEvent{MessageType: tc.mode}, nil, nil)()
	}
}
