package onebot

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestOneBotProactiveSpecificReplyEarlierMessage(t *testing.T) {
	firstCalled, replyCompleted := make(chan struct{}), make(chan struct{})
	calls := 0
	provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		message := core.ChatMessage{Role: core.RoleAssistant}
		switch calls {
		case 1:
			message.ToolCalls = []core.ToolCall{{ID: "wait", Type: "function", Function: core.ToolCallFunction{Name: llm.StaySilentToolName, Arguments: `{}`}}}
			close(firstCalled)
		case 2:
			prompt, _ := req.Messages[len(req.Messages)-1].Content.(string)
			if !strings.Contains(prompt, proactive.PromptPrefix) || !strings.Contains(prompt, `"message_id":"101"`) || !strings.Contains(prompt, "earlier central topic") {
				t.Errorf("missing searchable earlier message or proactive guidance: %s", prompt)
			}
			message.ToolCalls = []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{
				Name: "send_message", Arguments: `{"messages":[{"type":"mention_specific","message_id":"101"},{"type":"plain","text":"reply about earlier topic"}]}`,
			}}}
		case 3:
			result, _ := req.Messages[len(req.Messages)-1].Content.(string)
			if result != "消息已发送" {
				t.Errorf("delivery did not wait for ACK: %s", result)
			}
			close(replyCompleted)
		default:
			t.Errorf("unexpected model round %d", calls)
		}
		return &core.ChatResponse{Message: message}, nil
	}}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
		"ENABLE_REPLY_IN_GROUP_MSG":   "true", "ENABLE_AT_IN_GROUP_MSG": "true",
	})
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	engine.ToolRegistry["stay_silent"] = tools.StaySilentTool()
	srv, url := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v)
	}
	actions := make(chan model.OneBotAction, 8)
	readErrors := make(chan error, 1)
	go func() {
		for {
			var action model.OneBotAction
			if err := conn.ReadJSON(&action); err != nil {
				readErrors <- err
				return
			}
			data := map[string]any{"message_id": 103}
			if action.Action == "get_group_info" {
				data = map[string]any{"group_id": 10, "group_name": "fixture-group"}
			} else {
				actions <- action
			}
			if err := write(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": data}); err != nil {
				readErrors <- err
				return
			}
		}
	}()
	// IDs are synthetic fixtures, not real QQ accounts.
	older := model.OneBotEvent{SelfID: 30, PostType: "message", MessageType: "group", GroupID: 10, UserID: 20, MessageID: 101, Message: json.RawMessage(`[{"type":"text","data":{"text":"earlier central topic"}}]`)}
	if err := write(older); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstCalled:
	case err := <-readErrors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("first proactive turn did not start")
	}
	trigger := older
	trigger.MessageID, trigger.UserID = 102, 21
	trigger.Message = json.RawMessage(`[{"type":"text","data":{"text":"later unrelated trigger"}}]`)
	if err := write(trigger); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-actions:
		if action.Action != "send_group_msg" {
			t.Fatalf("unexpected action: %+v", action)
		}
		params, ok := action.Params.(map[string]any)
		if !ok {
			t.Fatalf("unexpected action params: %T", action.Params)
		}
		data, _ := json.Marshal(params["message"])
		var segments []tools.OneBotSegment
		if err := json.Unmarshal(data, &segments); err != nil {
			t.Fatal(err)
		}
		if len(segments) != 2 || segments[0].Type != "reply" || segments[0].Data["id"] != "101" || segments[1].Type != "text" || !strings.Contains(segments[1].Data["text"].(string), "reply about earlier topic") {
			t.Fatalf("wrong quote, trigger mention, or lost text: %s", data)
		}
	case err := <-readErrors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("specific reply not delivered")
	}
	select {
	case <-replyCompleted:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not receive delivery ACK")
	}
	select {
	case duplicate := <-actions:
		t.Fatalf("unexpected duplicate/empty reply: %+v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}
