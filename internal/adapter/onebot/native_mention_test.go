package onebot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestOneBotSendHookPreservesNativeMention(t *testing.T) {
	for _, autoMention := range []string{"false", "true"} {
		t.Run("auto_mention_"+autoMention, func(t *testing.T) {
			t.Setenv("ENABLE_AT_IN_GROUP_MSG", autoMention)
			t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "false")
			provider := &mockLLMProvider{responses: []*core.ChatResponse{
				{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
					ID: "mention_call", Type: "function", Function: core.ToolCallFunction{
						Name:      "send_message",
						Arguments: `{"messages":[{"type":"plain","text":"请 "},{"type":"mention_user","mention_user_id":"10002"},{"type":"plain","text":" 看一下"}]}`,
					},
				}}}},
				{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "完成"}},
			}}
			engine := newTestEngine(provider)
			engine.ToolRegistry["send_message"] = tools.SendMsgTool()
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

			// Synthetic IDs distinguish the bot, recipient, sender and group.
			event := model.OneBotEvent{
				SelfID: 10001, UserID: 10003, GroupID: 10004, MessageID: 10005,
				PostType: "message", MessageType: "group",
				Message: json.RawMessage(`[{"type":"at","data":{"qq":"10001"}},{"type":"text","data":{"text":"请转告 "}},{"type":"at","data":{"qq":"10002"}},{"type":"text","data":{"text":" 看一下"}}]`),
			}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			var action model.OneBotAction
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Action != "get_group_info" {
				t.Fatalf("unexpected initial action: %+v", action)
			}
			if err := conn.WriteJSON(map[string]any{
				"status": "ok", "retcode": 0, "echo": action.Echo,
				"data": map[string]any{"group_id": event.GroupID, "group_name": "mention test"},
			}); err != nil {
				t.Fatal(err)
			}
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Action != "send_group_msg" {
				t.Fatalf("unexpected tool action: %+v", action)
			}
			params, ok := action.Params.(map[string]any)
			if !ok {
				t.Fatalf("unexpected params: %#v", action.Params)
			}
			wire, err := json.Marshal(params["message"])
			if err != nil {
				t.Fatal(err)
			}
			var chain []tools.OneBotSegment
			if err := json.Unmarshal(wire, &chain); err != nil {
				t.Fatal(err)
			}
			if autoMention == "true" {
				if len(chain) != 4 || chain[0].Type != "at" || chain[0].Data["qq"] != "10003" {
					t.Fatalf("automatic sender mention must coexist with explicit recipient: %+v", chain)
				}
				chain = chain[1:]
			}
			if len(chain) != 3 || chain[0].Type != "text" || chain[0].Data["text"] != "请 " ||
				chain[1].Type != "at" || chain[1].Data["qq"] != "10002" ||
				chain[2].Type != "text" || chain[2].Data["text"] != " 看一下" {
				t.Fatalf("tool chain lost its native mention or ordering: %+v", chain)
			}
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo}); err != nil {
				t.Fatal(err)
			}
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Action != "send_group_msg" {
				t.Fatalf("unexpected final action: %+v", action)
			}
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo}); err != nil {
				t.Fatal(err)
			}

			for _, msg := range engine.SessionManager.GetOrCreate("group:10004").Snapshot() {
				if msg.Role == "user" && strings.Contains(fmt.Sprint(msg.Content), "outbound_message_guidance") {
					t.Fatal("request-local guidance must not accumulate in durable history")
				}
			}

			provider.mu.Lock()
			defer provider.mu.Unlock()
			if len(provider.requests) < 2 {
				t.Fatal("missing model requests")
			}
			var requestText string
			for _, msg := range provider.requests[0].Messages {
				if msg.Role == core.RoleUser {
					requestText += fmt.Sprint(msg.Content)
				}
			}
			if !strings.Contains(requestText, "[@10002]") || !strings.Contains(requestText, "outbound_message_guidance") ||
				!strings.Contains(requestText, tools.NativeMentionGuidance) {
				t.Fatalf("model must receive display-token guidance with the inbound mention: %s", requestText)
			}
		})
	}
}
