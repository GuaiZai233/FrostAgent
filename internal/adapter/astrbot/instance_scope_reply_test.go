package astrbot

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/runtimescope"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newAstrBotRuntimeScope(t *testing.T, values map[string]string) *runtimescope.Scope {
	t.Helper()
	dir := t.TempDir()
	instanceStore, err := instanceconfig.Open(filepath.Join(dir, "instance.env"), false)
	if err != nil {
		t.Fatalf("打开实例配置失败: %v", err)
	}
	globalStore, err := instanceconfig.Open(filepath.Join(dir, "global.env"), true)
	if err != nil {
		t.Fatalf("打开全局配置失败: %v", err)
	}
	for key, value := range values {
		if err := instanceStore.Update(key, value, false); err != nil {
			t.Fatalf("写入实例配置 %s 失败: %v", key, err)
		}
	}
	return runtimescope.New(instanceStore, globalStore, nil)
}

func TestAstrBotGroupDecorationsUseInstanceScope(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "false")
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "false")

	enabledScope := newAstrBotRuntimeScope(t, map[string]string{
		"ENABLE_AT_IN_GROUP_MSG":    "true",
		"ENABLE_REPLY_IN_GROUP_MSG": "true",
	})
	disabledScope := newAstrBotRuntimeScope(t, map[string]string{
		"ENABLE_AT_IN_GROUP_MSG":    "false",
		"ENABLE_REPLY_IN_GROUP_MSG": "false",
	})
	action := Action{
		Action:         "send_message",
		MessageType:    "group",
		UserID:         "usr_scope",
		ReplyMessageID: "msg_scope",
		Content:        "作用域回复",
	}

	enabled := action.withConfiguredGroupMentionScope(enabledScope).withConfiguredGroupReplyScope(enabledScope)
	if len(enabled.Messages) != 3 ||
		enabled.Messages[0].Type != "quote" ||
		enabled.Messages[0].MessageID != action.ReplyMessageID ||
		enabled.Messages[1].Type != "mention_user" ||
		enabled.Messages[1].MentionUserID != action.UserID ||
		enabled.Messages[2].Type != "plain" ||
		enabled.Messages[2].Text != action.Content {
		t.Fatalf("实例开启时应得到 quote+mention+plain，实际=%+v", enabled.Messages)
	}

	disabled := action.withConfiguredGroupMentionScope(disabledScope).withConfiguredGroupReplyScope(disabledScope)
	if len(disabled.Messages) != 0 || disabled.Content != action.Content {
		t.Fatalf("实例关闭时不应受进程环境影响，实际=%+v", disabled)
	}
}

func TestAstrBotWebSocketReplyUsesInstanceScope(t *testing.T) {
	tests := []struct {
		name            string
		processSetting  string
		instanceSetting string
		wantQuote       bool
	}{
		{
			name:            "instance enable overrides process disable",
			processSetting:  "false",
			instanceSetting: "true",
			wantQuote:       true,
		},
		{
			name:            "instance disable overrides process enable",
			processSetting:  "true",
			instanceSetting: "false",
			wantQuote:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ENABLE_AT_IN_GROUP_MSG", "false")
			t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", tt.processSetting)

			engine := newTestEngine(&mockLLMProvider{})
			engine.Scope = newAstrBotRuntimeScope(t, map[string]string{
				"ENABLE_AT_IN_GROUP_MSG":    "false",
				"ENABLE_REPLY_IN_GROUP_MSG": tt.instanceSetting,
			})
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
				MessageID:   "msg_instance_scope",
				UserID:      "usr_instance_scope",
				SenderName:  "测试用户",
				GroupID:     "grp_instance_scope",
				GroupName:   "实例作用域测试群",
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
				t.Fatalf("设置读取超时失败: %v", err)
			}
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("读取群聊回复失败: %v", err)
			}
			var action Action
			if err := json.Unmarshal(respBytes, &action); err != nil {
				t.Fatalf("解析群聊回复失败: %v", err)
			}

			if tt.wantQuote {
				assertAstrBotQuotePrefix(t, action, event.MessageID, "你好！这是 AstrBot 适配器的测试回复。")
				return
			}
			if len(action.Messages) != 0 {
				t.Fatalf("实例关闭引用时不应产生消息链，实际=%+v", action.Messages)
			}
			if action.Content != "你好！这是 AstrBot 适配器的测试回复。" {
				t.Fatalf("关闭引用时应保留兼容 Content，实际=%q", action.Content)
			}
		})
	}
}
