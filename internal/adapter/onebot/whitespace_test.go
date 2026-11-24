package onebot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestOneBotOutboundPreservesText(t *testing.T) {
	for _, route := range []struct {
		name        string
		messageType string
		mention     bool
		quote       bool
	}{
		{name: "private", messageType: "private"},
		{name: "group", messageType: "group"},
		{name: "group quote", messageType: "group", quote: true},
		{name: "group mention", messageType: "group", mention: true},
		{name: "group quote and mention", messageType: "group", quote: true, mention: true},
	} {
		for _, path := range []string{"plain", "structured", "tool", "direct"} {
			for _, text := range []string{"正文", " \t正文\n  "} {
				t.Run(route.name+"/"+path+"/"+strconv.Quote(text), func(t *testing.T) {
					scope := newTestScopeWithEnv(t, map[string]string{
						"ENABLE_AT_IN_GROUP_MSG":    strconv.FormatBool(route.mention),
						"ENABLE_REPLY_IN_GROUP_MSG": strconv.FormatBool(route.quote),
						"GROUP_REPLY_ON_MENTION":    "true",
						"ENABLE_PROACTIVE_REPLY":    "false",
						sticker.ActivityEnvKey:      "0",
					})
					// Short transport IDs are synthetic fixtures, not real accounts.
					event := model.OneBotEvent{
						PostType: "message", MessageType: route.messageType,
						SelfID: 11, UserID: 12, GroupID: 13, MessageID: 14,
						Message: json.RawMessage(`[{"type":"at","data":{"qq":"11"}},{"type":"text","data":{"text":"hello"}}]`),
					}
					var srv *httptest.Server
					var wsURL string
					if path == "direct" {
						srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							ws, err := upgrader.Upgrade(w, r, nil)
							if err != nil {
								t.Errorf("upgrade: %v", err)
								return
							}
							conn := newWSConnection(ws, scope)
							defer conn.Close()
							action, targetKey, target := "send_private_msg", "user_id", "12"
							if route.messageType == "group" {
								action, targetKey, target = "send_group_msg", "group_id", "13"
							}
							sendDirectReply(action, targetKey, target, "synthetic-reply", event, conn, text)
						}))
						wsURL = "ws" + strings.TrimPrefix(srv.URL, "http")
					} else {
						payload, err := json.Marshal(map[string]any{"messages": []tools.Msg{{Type: "plain", Text: text}}})
						if err != nil {
							t.Fatal(err)
						}
						message := core.ChatMessage{Role: core.RoleAssistant, Content: text}
						if path == "structured" {
							message.Content = string(payload)
						} else if path == "tool" {
							message.Content = nil
							message.ToolCalls = []core.ToolCall{{
								ID: "synthetic-call", Type: "function",
								Function: core.ToolCallFunction{Name: "send_message", Arguments: string(payload)},
							}}
						}
						provider := &mockLLMProvider{responses: []*core.ChatResponse{
							{Message: message}, {Message: core.ChatMessage{Role: core.RoleAssistant}},
						}}
						engine := newTestEngine(provider)
						engine.Scope = scope
						engine.ToolRegistry["send_message"] = tools.SendMsgTool()
						srv, wsURL = startReplyWSTestServer(t, engine)
					}
					defer srv.Close()
					conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
						t.Fatal(err)
					}
					if path != "direct" {
						if err := conn.WriteJSON(event); err != nil {
							t.Fatal(err)
						}
					}
					var action model.OneBotAction
					for {
						if err := conn.ReadJSON(&action); err != nil {
							t.Fatal(err)
						}
						if action.Action != "get_group_info" {
							break
						}
						if err := conn.WriteJSON(map[string]any{
							"status": "ok", "retcode": 0, "echo": action.Echo,
							"data": map[string]any{"group_id": event.GroupID, "group_name": "synthetic-group"},
						}); err != nil {
							t.Fatal(err)
						}
					}
					if action.Action != "send_"+route.messageType+"_msg" {
						t.Fatalf("unexpected action: %s", action.Action)
					}
					if path != "direct" {
						if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo}); err != nil {
							t.Fatal(err)
						}
					}
					message := action.Params.(map[string]any)["message"]
					decorated := route.messageType == "group" && (route.quote || route.mention)
					if (path == "plain" || path == "direct") && !decorated {
						if message != text {
							t.Fatalf("message = %q, want %q", message, text)
						}
						return
					}
					var want []tools.OneBotSegment
					if route.quote {
						want = append(want, tools.OneBotSegment{Type: "reply", Data: map[string]any{"id": "14"}})
					}
					if route.mention {
						want = append(want, tools.OneBotSegment{Type: "at", Data: map[string]any{"qq": "12"}})
					}
					wantText := text
					if route.mention && text == "正文" {
						wantText = " 正文"
					}
					want = append(want, tools.OneBotSegment{Type: "text", Data: map[string]any{"text": wantText}})
					data, err := json.Marshal(message)
					if err != nil {
						t.Fatal(err)
					}
					var got []tools.OneBotSegment
					if err := json.Unmarshal(data, &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("segments = %#v, want %#v", got, want)
					}
				})
			}
		}
	}
}
