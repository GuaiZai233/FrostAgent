package astrbot

import (
	"FrostAgent/internal/core"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func autoDecorationEvent(id, groupName, content string) Event {
	return Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_" + id,
		UserID:      "usr_" + id,
		SenderName:  "测试用户",
		GroupID:     "grp_" + id,
		GroupName:   groupName,
		Content:     content,
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
		Timestamp:   time.Now().Unix(),
	}
}

func dialAutoDecorationTest(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func sendAutoDecorationEvent(t *testing.T, conn *websocket.Conn, event Event) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("序列化群聊事件失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("发送群聊事件失败: %v", err)
	}
}

func readAutoDecorationAction(t *testing.T, conn *websocket.Conn, label string) Action {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("设置%s读取超时失败: %v", label, err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取%s失败: %v", label, err)
	}
	var action Action
	if err := json.Unmarshal(data, &action); err != nil {
		t.Fatalf("解析%s失败: %v", label, err)
	}
	return action
}

func autoDecorationSendHookProvider() *mockLLMProvider {
	return &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{{
						ID:   "call_auto_decoration",
						Type: "function",
						Function: core.ToolCallFunction{
							Name:      "send_message",
							Arguments: `{"messages":[{"type":"plain","text":"处理中"}]}`,
						},
					}},
				},
			},
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "处理完成",
				},
			},
		},
	}
}
