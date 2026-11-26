package onebot

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestSendMessageDecorationPreferenceThroughAdapter(t *testing.T) {
	// All transport identifiers below are synthetic fixtures.
	text := tools.Msg{Type: "plain", Text: "\tfixture\n "}
	quote := tools.Msg{Type: "quote", MessageID: "14"}
	mention := tools.Msg{Type: "mention_user", MentionUserID: "15"}
	image := tools.Msg{Type: "image", URL: "https://example.invalid/fixture.png"}
	textSegment := tools.OneBotSegment{Type: "text", Data: map[string]any{"text": text.Text}}
	quoteSegment := tools.OneBotSegment{Type: "reply", Data: map[string]any{"id": "14"}}
	mentionSegment := tools.OneBotSegment{Type: "at", Data: map[string]any{"qq": "15"}}
	imageSegment := tools.OneBotSegment{Type: "image", Data: map[string]any{"file": image.URL}}
	markedText := tools.Msg{Type: "plain", Text: "first。" + replyformat.SegmentMark + "second\n\n`" + replyformat.SegmentMark + "`"}
	cleanTextSegment := tools.OneBotSegment{Type: "text", Data: map[string]any{"text": "first。second\n\n`" + replyformat.SegmentMark + "`"}}
	for _, chain := range []struct {
		name     string
		messages []tools.Msg
		want     []tools.OneBotSegment
	}{
		{"plain", []tools.Msg{text}, []tools.OneBotSegment{textSegment}},
		{"explicit quote and mention", []tools.Msg{quote, mention, text}, []tools.OneBotSegment{quoteSegment, mentionSegment, textSegment}},
		{"explicit quote and mention with segment marker", []tools.Msg{quote, mention, markedText}, []tools.OneBotSegment{quoteSegment, mentionSegment, cleanTextSegment}},
		{"media", []tools.Msg{text, image}, []tools.OneBotSegment{textSegment, imageSegment}},
		{"media with explicit quote and mention", []tools.Msg{quote, mention, text, image}, []tools.OneBotSegment{quoteSegment, mentionSegment, textSegment, imageSegment}},
	} {
		for _, preference := range []string{"omitted", "true", "false"} {
			t.Run(chain.name+"/"+preference, func(t *testing.T) {
				input := map[string]any{"messages": chain.messages}
				if preference != "omitted" {
					input["use_frostagent_decoration"] = preference == "true"
				}
				args, _ := json.Marshal(input)
				finished := make(chan struct{})
				calls := 0
				provider := &mockLLMProvider{customChat: func(_ context.Context, _ core.ChatRequest) (*core.ChatResponse, error) {
					calls++
					message := core.ChatMessage{Role: core.RoleAssistant}
					if calls == 1 {
						message.ToolCalls = []core.ToolCall{{ID: "fixture-send", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: string(args)}}}
					} else {
						if calls != 2 {
							t.Errorf("unexpected provider round %d", calls)
						} else {
							close(finished)
						}
					}
					return &core.ChatResponse{Message: message}, nil
				}}
				engine := newTestEngine(provider)
				engine.Scope = newTestScopeWithEnv(t, map[string]string{
					"ENABLE_AT_IN_GROUP_MSG": "true", "ENABLE_REPLY_IN_GROUP_MSG": "true",
					"GROUP_REPLY_ON_MENTION": "true", "ENABLE_PROACTIVE_REPLY": "false",
					replyformat.EnvKey: "true",
				})
				defer engine.Scope.Cancel()
				engine.ToolRegistry["send_message"] = tools.SendMsgTool()
				server, address := startReplyWSTestServer(t, engine)
				defer server.Close()
				ws, _, err := websocket.DefaultDialer.Dial(address, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer ws.Close()
				if err := ws.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatal(err)
				}
				event := model.OneBotEvent{SelfID: 11, UserID: 12, GroupID: 13, MessageID: 14,
					PostType: "message", MessageType: "group",
					Message: json.RawMessage(`[{"type":"at","data":{"qq":"11"}},{"type":"text","data":{"text":"fixture request"}}]`),
				}
				if err := ws.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				var action model.OneBotAction
				for {
					if err := ws.ReadJSON(&action); err != nil {
						t.Fatal(err)
					}
					if action.Action != "get_group_info" {
						break
					}
					if err := ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo,
						"data": map[string]any{"group_id": 13, "group_name": "fixture group"}}); err != nil {
						t.Fatal(err)
					}
				}
				if action.Action != "send_group_msg" {
					t.Fatalf("unexpected action: %+v", action)
				}
				wire, _ := json.Marshal(action.Params.(map[string]any)["message"])
				var got []tools.OneBotSegment
				if err := json.Unmarshal(wire, &got); err != nil {
					t.Fatal(err)
				}
				want := append([]tools.OneBotSegment(nil), chain.want...)
				if preference != "false" {
					// Configured decoration adds the triggering user's mention;
					// existing leading whitespace and explicit quotes are retained.
					body := want
					if want[0].Type == "reply" {
						body = want[1:]
					}
					want = append([]tools.OneBotSegment{quoteSegment, {Type: "at", Data: map[string]any{"qq": "12"}}}, body...)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("wire chain=%s, want=%+v", wire, want)
				}
				if err := ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo,
					"data": map[string]any{"message_id": 16}}); err != nil {
					t.Fatal(err)
				}
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Fatal("tool delivery did not finish after ACK")
				}
			})
		}
	}
}
