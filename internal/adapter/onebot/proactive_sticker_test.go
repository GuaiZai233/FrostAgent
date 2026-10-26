package onebot

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/parity"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
	"testing"
	"time"
)

func TestFilterProactiveStickerCQ(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"before[CQ:mface,emoji_id=synthetic]after", "beforeafter"},
		{"[CQ:sticker,id=synthetic]", ""},
		{"[CQ:image,file=sticker.gif,sub_type=1]", ""},
		{"[CQ:image,subType=1,file=sticker.png]", ""},
		{"[CQ:image,file=sticker.png,is_sticker=true]", ""},
		{"[CQ:image,file=photo.gif,sub_type=0]", "[CQ:image,file=photo.gif,sub_type=0]"},
		{"[CQ:image,file=photo.png]", "[CQ:image,file=photo.png]"},
		{"[CQ:face,id=1] hello", "[CQ:face,id=1] hello"},
		{"[CQ:image,file=a&#44;b.png]", "[CQ:image,file=a&#44;b.png]"},
		{"&#91;CQ:mface,id=synthetic&#93;", "&#91;CQ:mface,id=synthetic&#93;"},
		{`{"messages":[{"type":"plain","text":"[CQ:mface,id=synthetic]"}]}`, `{"messages":[{"type":"plain","text":"[CQ:mface,id=synthetic]"}]}`},
	} {
		if got := filterProactiveStickerCQ(tc.input); got != tc.want {
			t.Errorf("filterProactiveStickerCQ(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestOneBotProactiveReplyKeepsImagesWithoutStickers(t *testing.T) {
	for _, tc := range []struct {
		name, reply, wantText string
		structured            bool
	}{
		{
			name: "structured mixed message", structured: true,
			reply: `{"messages":[{"type":"plain","text":"hello"},{"type":"image","url":"https://example.invalid/sticker.png","is_sticker":true},{"type":"mface"},{"type":"sticker"},{"type":"image","url":"https://example.invalid/sticker.png","sub_type":1},{"type":"image","url":"https://example.invalid/sticker.png","data":{"subType":"1"}},{"type":"image","url":"https://example.invalid/photo.gif"}]}`,
		},
		{
			name:     "CQ mixed message",
			reply:    "hello[CQ:mface,id=synthetic][CQ:image,file=sticker.png,subType=1][CQ:image,file=photo.gif]",
			wantText: "hello[CQ:image,file=photo.gif]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: tc.reply}}}}
			engine := newTestEngine(provider)
			engine.MaxIterations = 1
			engine.Scope = newTestScopeWithEnv(t, map[string]string{
				sticker.ActivityEnvKey: "10", sticker.ActivityModeEnvKey: sticker.ActivityBotTurns,
				"BOT_NAME": "test-bot", "BOT_ALIASES": "", "GROUP_REPLY_ON_MENTION": "true",
				"ENABLE_PROACTIVE_REPLY": "true", "PROACTIVE_REPLY_PROBABILITY": "1",
				"ENABLE_PROACTIVE_REPLY_WHITELIST": "false", "ENABLE_AT_IN_GROUP_MSG": "false", "ENABLE_REPLY_IN_GROUP_MSG": "false",
			})
			for range 4 {
				engine.RecordBotTurn()
			}
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
			// These short IDs are synthetic transport fixtures, not real users.
			event := model.OneBotEvent{PostType: "message", MessageType: "group", SelfID: 11, UserID: 12, GroupID: 13, MessageID: 1,
				Message: json.RawMessage(`[{"type":"text","data":{"text":"hello"}}]`)}
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
			params, ok := action.Params.(map[string]any)
			if !ok {
				t.Fatalf("unexpected params: %T", action.Params)
			}
			if tc.structured {
				data, err := json.Marshal(params["message"])
				if err != nil {
					t.Fatal(err)
				}
				var segments []tools.OneBotSegment
				if err := json.Unmarshal(data, &segments); err != nil {
					t.Fatal(err)
				}
				if len(segments) != 2 || parity.ContainsStickerOneBot(segments) || segments[0].Type != "text" || segments[0].Data["text"] != " hello" || segments[1].Type != "image" || segments[1].Data["file"] != "https://example.invalid/photo.gif" {
					t.Fatalf("text/image lost or sticker delivered: %+v", segments)
				}
			} else if params["message"] != tc.wantText {
				t.Fatalf("message=%v want=%q", params["message"], tc.wantText)
			}
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"message_id": 2}, "echo": action.Echo}); err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if len(provider.requests) != 1 {
				t.Fatalf("proactive reply retried sticker enforcement: %d requests", len(provider.requests))
			}
		})
	}
}
