package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/sticker"
	"github.com/gorilla/websocket"
	"strings"
	"testing"
	"time"
)

func TestAstrBotStickerCountModesAtIngress(t *testing.T) {
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
			srv, _, wsURL := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			event := Event{Type: "event", EventType: "message", MessageType: "group", Platform: "astrbot", UserID: "synthetic-user", GroupID: "synthetic-group", SessionID: "synthetic-session", MessageID: "passive", Content: "chat"}
			if !tc.proactive {
				for range 4 {
					if err := conn.WriteJSON(event); err != nil {
						t.Fatal(err)
					}
					var action Action
					if err := conn.ReadJSON(&action); err != nil {
						t.Fatal(err)
					}
					if action.Action != "noop" {
						t.Fatalf("passive message caused reply: %s", action.Action)
					}
				}
				if err := conn.WriteJSON(map[string]any{"type": "heartbeat"}); err != nil {
					t.Fatal(err)
				}
				if err := conn.WriteJSON(map[string]any{"type": "event", "event_type": "notice", "message_type": "group"}); err != nil {
					t.Fatal(err)
				}
				event.IsAt = true
				event.MessageID = "wake"
			}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			var action Action
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Action != "send_message" {
				t.Fatalf("unexpected final: %s", action.Action)
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
			if found == tc.proactive {
				t.Fatalf("countdown %q: found=%t proactive=%t", tc.countdown, found, tc.proactive)
			}
		})
	}
}
