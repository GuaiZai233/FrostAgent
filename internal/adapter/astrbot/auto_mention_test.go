package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/tools"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAstrBotGroupReplyHonorsAutoMention(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "true")

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
		MessageID:   "msg_auto_mention",
		UserID:      "usr_auto_mention",
		SenderName:  "测试用户",
		GroupID:     "grp_auto_mention",
		GroupName:   "自动艾特测试群",
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
		t.Fatalf("开启自动艾特后应产生 mention+plain 两段消息，实际=%+v", action.Messages)
	}
	if action.Messages[0].Type != "mention_user" || action.Messages[0].MentionUserID != event.UserID {
		t.Fatalf("第一段应自动艾特触发用户，实际=%+v", action.Messages[0])
	}
	if action.Messages[1].Type != "plain" || action.Messages[1].Text != action.Content {
		t.Fatalf("第二段应保留原回复文本，实际=%+v", action.Messages[1])
	}
}

func TestAstrBotGroupSendHookHonorsAutoMention(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "true")

	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{{
						ID:   "call_auto_mention",
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
		MessageID:   "msg_auto_mention_hook",
		UserID:      "usr_auto_mention_hook",
		SenderName:  "测试用户",
		GroupID:     "grp_auto_mention_hook",
		GroupName:   "自动艾特工具测试群",
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

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("设置 SendHook 回复读取超时失败: %v", err)
	}
	_, hookBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取 SendHook 回复失败: %v", err)
	}
	var hookAction Action
	if err := json.Unmarshal(hookBytes, &hookAction); err != nil {
		t.Fatalf("解析 SendHook 回复失败: %v", err)
	}
	if !hookAction.IsIntermediate {
		t.Fatalf("第一条应为 SendHook 中间回复: %+v", hookAction)
	}
	if len(hookAction.Messages) != 2 ||
		hookAction.Messages[0].Type != "mention_user" ||
		hookAction.Messages[0].MentionUserID != event.UserID ||
		hookAction.Messages[1].Type != "plain" ||
		hookAction.Messages[1].Text != "处理中" {
		t.Fatalf("SendHook 应前置触发用户 mention 并保留原组件顺序，实际=%+v", hookAction.Messages)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("设置最终回复读取超时失败: %v", err)
	}
	_, finalBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取最终回复失败: %v", err)
	}
	var finalAction Action
	if err := json.Unmarshal(finalBytes, &finalAction); err != nil {
		t.Fatalf("解析最终回复失败: %v", err)
	}
	if len(finalAction.Messages) != 2 || finalAction.Messages[0].MentionUserID != event.UserID {
		t.Fatalf("最终回复也应自动艾特触发用户，实际=%+v", finalAction.Messages)
	}
}

func TestAstrBotConfiguredGroupMentionAvoidsDuplicate(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "true")

	action := Action{
		Action:      "send_message",
		MessageType: "group",
		UserID:      "usr_same",
		Messages: []ActionMessage{
			{Type: "mention_user", MentionUserID: "usr_same"},
			{Type: "plain", Text: "已经艾特过了"},
		},
	}

	normalized := action.withConfiguredGroupMention()
	if len(normalized.Messages) != 2 {
		t.Fatalf("已有同用户 mention 时不应重复注入，实际=%+v", normalized.Messages)
	}
	if normalized.Messages[0].Type != "mention_user" || normalized.Messages[0].MentionUserID != "usr_same" {
		t.Fatalf("原消息顺序不应改变，实际=%+v", normalized.Messages)
	}
}

func TestAstrBotConfiguredGroupMentionRespectsScopeAndSetting(t *testing.T) {
	t.Run("disabled group", func(t *testing.T) {
		t.Setenv("ENABLE_AT_IN_GROUP_MSG", "false")
		action := Action{
			Action:      "send_message",
			MessageType: "group",
			UserID:      "usr_disabled",
			Content:     "保持原样",
		}
		normalized := action.withConfiguredGroupMention()
		if len(normalized.Messages) != 0 || normalized.Content != action.Content {
			t.Fatalf("关闭自动艾特时应保持原样，实际=%+v", normalized)
		}
	})

	t.Run("private", func(t *testing.T) {
		t.Setenv("ENABLE_AT_IN_GROUP_MSG", "true")
		action := Action{
			Action:      "send_message",
			MessageType: "private",
			UserID:      "usr_private",
			Content:     "私聊",
		}
		normalized := action.withConfiguredGroupMention()
		if len(normalized.Messages) != 0 || normalized.Content != action.Content {
			t.Fatalf("私聊不应自动注入 mention，实际=%+v", normalized)
		}
	})
}
