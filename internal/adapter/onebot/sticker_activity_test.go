package onebot

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"github.com/gorilla/websocket"
	"strings"
	"testing"
	"time"
)

func TestOneBotStickerCountModesAtIngress(t *testing.T) {
	for _, tc := range []struct {
		name, mode, countdown string
		proactive             bool
	}{
		{name: "bot dialogue", mode: sticker.ActivityBotTurns, countdown: "还剩 4 个 Bot 对话轮次"},
		{name: "all incoming messages", mode: sticker.ActivityAllMessages, countdown: "还剩 0 条传入消息"},
		{name: "proactive dialogue", mode: sticker.ActivityBotTurns, countdown: "还剩 4 个 Bot 对话轮次", proactive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(sticker.ActivityEnvKey, "10")
			t.Setenv(sticker.ActivityModeEnvKey, tc.mode)
			t.Setenv("BOT_NAME", "test-bot")
			t.Setenv("BOT_ALIASES", "")
			t.Setenv("GROUP_REPLY_ON_MENTION", "true")
			t.Setenv("ENABLE_PROACTIVE_REPLY", "false")
			if tc.proactive {
				t.Setenv("ENABLE_PROACTIVE_REPLY", "true")
				t.Setenv("PROACTIVE_REPLY_PROBABILITY", "1")
				t.Setenv("ENABLE_PROACTIVE_REPLY_WHITELIST", "false")
			}
			provider := &mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "done"}}}}
			engine := newTestEngine(provider)
			enabled := "false"
			if tc.proactive {
				enabled = "true"
			}
			engine.Scope = newTestScopeWithEnv(t, map[string]string{
				sticker.ActivityEnvKey: "10", sticker.ActivityModeEnvKey: tc.mode,
				"BOT_NAME": "test-bot", "BOT_ALIASES": "", "GROUP_REPLY_ON_MENTION": "true",
				"ENABLE_PROACTIVE_REPLY": enabled, "PROACTIVE_REPLY_PROBABILITY": "1",
				"ENABLE_PROACTIVE_REPLY_WHITELIST": "false", "ENABLE_AT_IN_GROUP_MSG": "false", "ENABLE_REPLY_IN_GROUP_MSG": "false",
			})
			srv, wsURL := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			event := model.OneBotEvent{PostType: "message", MessageType: "group", SelfID: 700000151, UserID: 700000152, GroupID: 700000153, MessageID: 1,
				Message: json.RawMessage(`[{"type":"text","data":{"text":"chat"}}]`)}
			if !tc.proactive {
				for i := 1; i <= 4; i++ {
					event.MessageID = int32(i)
					if err := conn.WriteJSON(event); err != nil {
						t.Fatal(err)
					}
				}
				if err := conn.WriteJSON(map[string]any{"post_type": "meta_event", "meta_event_type": "heartbeat"}); err != nil {
					t.Fatal(err)
				}
				if err := conn.WriteJSON(map[string]any{"post_type": "notice", "notice_type": "test", "message_type": "group"}); err != nil {
					t.Fatal(err)
				}
				event.MessageID = 5
				event.Message = json.RawMessage(`[{"type":"at","data":{"qq":"700000151"}},{"type":"text","data":{"text":"hello"}}]`)
			}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			var action model.OneBotAction
			for {
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Action != "get_group_info" {
					break
				}
				if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_id": event.GroupID, "group_name": "synthetic-group"}}); err != nil {
					t.Fatal(err)
				}
			}
			if action.Action != "send_group_msg" {
				t.Fatalf("unexpected action: %s", action.Action)
			}
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"message_id": 700000154}, "echo": action.Echo}); err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			wantCalls := 1
			if tc.mode == sticker.ActivityAllMessages {
				wantCalls = 4
			}
			if len(provider.requests) != wantCalls {
				t.Fatalf("passive messages triggered inference or enforcement skipped: calls=%d want=%d", len(provider.requests), wantCalls)
			}
			found := false
			for _, msg := range provider.requests[0].Messages {
				if text, ok := msg.Content.(string); ok && strings.Contains(text, tc.countdown) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing countdown %q: %+v", tc.countdown, provider.requests[0].Messages)
			}
		})
	}
}
