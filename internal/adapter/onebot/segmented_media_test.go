package onebot

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

// runMixedMediaSend drives one group turn whose send_message contains two plain
// components and an image, and returns the outbound group actions it produced.
// It acknowledges each action so every segment completes.
func runMixedMediaSend(t *testing.T, limit, args string) []model.OneBotAction {
	t.Helper()
	t.Setenv("ENABLE_MULTI_SEGMENT_REPLY", "true")
	t.Setenv("MULTI_SEGMENT_REPLY_MAX_SEGMENTS", limit)
	provider := &mockLLMProvider{responses: []*core.ChatResponse{
		{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "call_media", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: args},
		}}}},
		{Message: core.ChatMessage{Role: core.RoleAssistant}},
	}}
	engine := newTestEngine(provider)
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()
	event := model.OneBotEvent{SelfID: 700000031, PostType: "message", MessageType: "group", GroupID: 700000032, UserID: 700000033, MessageID: 700000034,
		Message: json.RawMessage(`[{"type":"at","data":{"qq":"700000031"}},{"type":"text","data":{"text":"你好"}}]`)}
	eventBytes, _ := json.Marshal(event)
	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}
	var sent []model.OneBotAction
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var action model.OneBotAction
		if err := json.Unmarshal(raw, &action); err != nil {
			continue
		}
		switch action.Action {
		case "get_group_info":
			reply, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"group_id": event.GroupID, "group_name": "媒体分段测试群"}, "echo": action.Echo})
			_ = conn.WriteMessage(websocket.TextMessage, reply)
		case "send_group_msg":
			sent = append(sent, action)
			ack, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 9}})
			_ = conn.WriteMessage(websocket.TextMessage, ack)
		}
	}
	return sent
}

func TestMixedMediaSendSegmentsAndAcksEachPart(t *testing.T) {
	sent := runMixedMediaSend(t, "3", `{"messages":[{"type":"plain","text":"第一段"},{"type":"plain","text":"第二段"},{"type":"image","url":"https://example.invalid/a.png"}]}`)
	if len(sent) != 2 {
		t.Fatalf("want 2 outbound group messages (plain 1 | plain 2 + image), got %d", len(sent))
	}
	first, _ := sent[0].Params.(map[string]any)
	second, _ := sent[1].Params.(map[string]any)
	firstSegs, _ := first["message"].([]any)
	secondSegs, _ := second["message"].([]any)
	if len(firstSegs) != 1 || len(secondSegs) != 2 {
		t.Fatalf("segment shapes wrong: first=%d second=%d", len(firstSegs), len(secondSegs))
	}
}

func TestMixedMediaSendOverLimitDeliversNothing(t *testing.T) {
	if sent := runMixedMediaSend(t, "2", `{"messages":[{"type":"plain","text":"一"},{"type":"plain","text":"二"},{"type":"plain","text":"三"},{"type":"image","url":"https://example.invalid/a.png"}]}`); len(sent) != 0 {
		t.Fatalf("over-limit batch must deliver nothing, got %d sends", len(sent))
	}
}
