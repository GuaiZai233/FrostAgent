package onebot

import (
	"encoding/json"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

// Two plain components in one send_message must reach OneBot as two messages,
// each acknowledged separately. Only the first keeps reply and mention decoration.
func TestSendMessageSegmentsArePerPlainMessages(t *testing.T) {
	t.Setenv("ENABLE_MULTI_SEGMENT_REPLY", "true")
	t.Setenv("MULTI_SEGMENT_REPLY_MAX_SEGMENTS", "3")
	provider := &mockLLMProvider{responses: []*core.ChatResponse{
		{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "call_segments", Type: "function",
			Function: core.ToolCallFunction{Name: "send_message", Arguments: `{"messages":[{"type":"plain","text":"第一段"},{"type":"plain","text":"第二段"}]}`},
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
	event := model.OneBotEvent{SelfID: 700000021, PostType: "message", MessageType: "group", GroupID: 700000022, UserID: 700000023, MessageID: 700000024,
		Message: json.RawMessage(`[{"type":"at","data":{"qq":"700000021"}},{"type":"text","data":{"text":"你好"}}]`)}
	eventBytes, _ := json.Marshal(event)
	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// Answer the group-info query the adapter issues before sending.
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取群信息查询失败: %v", err)
	}
	var info model.OneBotAction
	_ = json.Unmarshal(raw, &info)
	reply, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"group_id": event.GroupID, "group_name": "分段测试群"}, "echo": info.Echo})
	if err := conn.WriteMessage(websocket.TextMessage, reply); err != nil {
		t.Fatalf("发送群信息响应失败: %v", err)
	}

	var sent []model.OneBotAction
	for len(sent) < 2 {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("读取第 %d 条发送 action 失败: %v", len(sent)+1, err)
		}
		var action model.OneBotAction
		if err := json.Unmarshal(raw, &action); err != nil {
			t.Fatalf("解析 action 失败: %v", err)
		}
		if action.Action != "send_group_msg" {
			t.Fatalf("期望 send_group_msg, 实际=%s", action.Action)
		}
		sent = append(sent, action)
		ack, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo})
		if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
			t.Fatalf("发送 ACK 失败: %v", err)
		}
	}

	texts := make([]string, 0, 2)
	for i, action := range sent {
		params, _ := action.Params.(map[string]any)
		segments, _ := params["message"].([]any)
		var parts []string
		for _, seg := range segments {
			m, _ := seg.(map[string]any)
			if m["type"] == "text" {
				data, _ := m["data"].(map[string]any)
				parts = append(parts, data["text"].(string))
			} else if i > 0 && (m["type"] == "reply" || m["type"] == "at") {
				t.Fatalf("second segment must not carry decoration, got %v", m["type"])
			}
		}
		if len(parts) != 1 {
			t.Fatalf("segment %d must be one message with one text, got %v", i+1, parts)
		}
		texts = append(texts, parts[0])
	}
	if texts[0] != "第一段" || texts[1] != "第二段" {
		t.Fatalf("unexpected segment texts: %q", texts)
	}
}
