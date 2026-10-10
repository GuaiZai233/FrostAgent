package astrbot

import (
	"FrostAgent/internal/tools"
	"testing"
)

func TestAstrBotGroupReplyHonorsAutoMention(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "true")

	engine := newTestEngine(&mockLLMProvider{})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn := dialAutoDecorationTest(t, wsURL)
	event := autoDecorationEvent("auto_mention", "自动艾特测试群", "霜降 你好")
	sendAutoDecorationEvent(t, conn, event)
	action := readAutoDecorationAction(t, conn, "群聊回复")

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

	engine := newTestEngine(autoDecorationSendHookProvider())
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn := dialAutoDecorationTest(t, wsURL)
	event := autoDecorationEvent("auto_mention_hook", "自动艾特工具测试群", "霜降 帮我处理一下")
	sendAutoDecorationEvent(t, conn, event)
	hookAction := readAutoDecorationAction(t, conn, "SendHook 回复")
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

	finalAction := readAutoDecorationAction(t, conn, "最终回复")
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
