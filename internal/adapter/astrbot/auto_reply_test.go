package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/tools"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAstrBotGroupReplyHonorsAutoReply(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "false")
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "true")

	engine := newTestEngine(&mockLLMProvider{})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_auto_reply",
		UserID:      "usr_auto_reply",
		SenderName:  "测试用户",
		GroupID:     "grp_auto_reply",
		GroupName:   "自动引用测试群",
		Content:     "霜降 你好",
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
		Timestamp:   time.Now().Unix(),
	}
	data, _ := json.Marshal(event)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("发送群聊事件失败: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("设置群聊回复读取超时失败: %v", err)
	}
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取群聊回复失败: %v", err)
	}
	var action Action
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析群聊回复失败: %v", err)
	}

	if action.Content != "你好！这是 AstrBot 适配器的测试回复。" {
		t.Fatalf("兼容 Content 不应改变，实际=%q", action.Content)
	}
	if len(action.Messages) != 2 {
		t.Fatalf("开启自动引用后应产生 quote+plain 两段消息，实际=%+v", action.Messages)
	}
	if action.Messages[0].Type != "quote" || action.Messages[0].MessageID != event.MessageID {
		t.Fatalf("第一段应引用当前入站消息，实际=%+v", action.Messages[0])
	}
	if action.Messages[1].Type != "plain" || action.Messages[1].Text != action.Content {
		t.Fatalf("第二段应保留原回复文本，实际=%+v", action.Messages[1])
	}
}

func TestAstrBotGroupSendHookHonorsAutoReply(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "false")
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "true")

	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{{
						ID:   "call_auto_reply",
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
	engine := newTestEngine(provider)
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_auto_reply_hook",
		UserID:      "usr_auto_reply_hook",
		SenderName:  "测试用户",
		GroupID:     "grp_auto_reply_hook",
		GroupName:   "自动引用工具测试群",
		Content:     "霜降 帮我处理一下",
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
		Timestamp:   time.Now().Unix(),
	}
	data, _ := json.Marshal(event)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("发送群聊事件失败: %v", err)
	}

	readAction := func(label string) Action {
		t.Helper()
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("设置%s读取超时失败: %v", label, err)
		}
		_, actionBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("读取%s失败: %v", label, err)
		}
		var action Action
		if err := json.Unmarshal(actionBytes, &action); err != nil {
			t.Fatalf("解析%s失败: %v", label, err)
		}
		return action
	}

	hookAction := readAction("SendHook 回复")
	if !hookAction.IsIntermediate {
		t.Fatalf("第一条应为 SendHook 中间回复: %+v", hookAction)
	}
	assertAstrBotQuotePrefix(t, hookAction, event.MessageID, "处理中")

	finalAction := readAction("最终回复")
	if finalAction.IsIntermediate {
		t.Fatalf("第二条应为最终回复: %+v", finalAction)
	}
	assertAstrBotQuotePrefix(t, finalAction, event.MessageID, "处理完成")
}

func TestAstrBotConfiguredGroupReplyAvoidsDuplicate(t *testing.T) {
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "true")

	action := Action{
		Action:         "send_message",
		MessageType:    "group",
		ReplyMessageID: "msg_current",
		Messages: []ActionMessage{
			{Type: "quote", MessageID: "msg_explicit"},
			{Type: "plain", Text: "显式引用优先"},
		},
	}

	normalized := action.withConfiguredGroupReply()
	if len(normalized.Messages) != 2 {
		t.Fatalf("已有显式 quote 时不应重复注入，实际=%+v", normalized.Messages)
	}
	if normalized.Messages[0].Type != "quote" || normalized.Messages[0].MessageID != "msg_explicit" {
		t.Fatalf("显式 quote 与原消息顺序不应改变，实际=%+v", normalized.Messages)
	}
}

func TestAstrBotConfiguredGroupReplyPrecedesMention(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "true")
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "true")

	action := Action{
		Action:         "send_message",
		MessageType:    "group",
		UserID:         "usr_reply_order",
		ReplyMessageID: "msg_reply_order",
		Content:        "引用并艾特",
	}

	data, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("序列化群聊回复失败: %v", err)
	}
	var normalized Action
	if err := json.Unmarshal(data, &normalized); err != nil {
		t.Fatalf("解析群聊回复失败: %v", err)
	}
	if len(normalized.Messages) != 3 ||
		normalized.Messages[0].Type != "quote" ||
		normalized.Messages[0].MessageID != action.ReplyMessageID ||
		normalized.Messages[1].Type != "mention_user" ||
		normalized.Messages[1].MentionUserID != action.UserID ||
		normalized.Messages[2].Type != "plain" ||
		normalized.Messages[2].Text != action.Content {
		t.Fatalf("群聊回复消息段顺序应为 quote+mention+plain，实际=%+v", normalized.Messages)
	}
}

func TestAstrBotConfiguredGroupReplyRespectsScopeAndSetting(t *testing.T) {
	tests := []struct {
		name    string
		enabled string
		action  Action
	}{
		{
			name:    "disabled group",
			enabled: "false",
			action: Action{
				Action:         "send_message",
				MessageType:    "group",
				ReplyMessageID: "msg_disabled",
				Content:        "保持原样",
			},
		},
		{
			name:    "private",
			enabled: "true",
			action: Action{
				Action:         "send_message",
				MessageType:    "private",
				ReplyMessageID: "msg_private",
				Content:        "私聊",
			},
		},
		{
			name:    "proactive group message",
			enabled: "true",
			action: Action{
				Action:      "send_message",
				MessageType: "group",
				Content:     "主动消息",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", tt.enabled)
			normalized := tt.action.withConfiguredGroupReply()
			if len(normalized.Messages) != 0 || normalized.Content != tt.action.Content {
				t.Fatalf("不满足自动引用条件时应保持原样，实际=%+v", normalized)
			}
		})
	}
}

func assertAstrBotQuotePrefix(t *testing.T, action Action, messageID, text string) {
	t.Helper()
	if len(action.Messages) != 2 ||
		action.Messages[0].Type != "quote" ||
		action.Messages[0].MessageID != messageID ||
		action.Messages[1].Type != "plain" ||
		action.Messages[1].Text != text {
		t.Fatalf("应前置当前消息 quote 并保留原组件顺序，实际=%+v", action.Messages)
	}
}
