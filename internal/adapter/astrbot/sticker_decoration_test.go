package astrbot

import (
	"FrostAgent/internal/core"
	"encoding/json"
	"reflect"
	"testing"
)

func TestAstrBotStickerSkipsScopedGroupDecorations(t *testing.T) {
	scope := newAstrBotRuntimeScope(t, map[string]string{
		"ENABLE_AT_IN_GROUP_MSG":    "true",
		"ENABLE_REPLY_IN_GROUP_MSG": "true",
	})

	tests := []struct {
		name   string
		action Action
	}{
		{
			name: "sticker message",
			action: Action{
				Action:         "send_message",
				MessageType:    "group",
				UserID:         "usr_sticker",
				ReplyMessageID: "msg_sticker",
				Messages: []ActionMessage{
					{Type: "image", URL: "/api/sticker/test/image", IsSticker: true},
				},
			},
		},
		{
			name: "mixed text and sticker chain",
			action: Action{
				Action:         "send_message",
				MessageType:    "group",
				UserID:         "usr_mixed",
				ReplyMessageID: "msg_mixed",
				Messages: []ActionMessage{
					{Type: "plain", Text: "先说一句"},
					{Type: "image", URL: "/api/sticker/test/image", IsSticker: true},
				},
			},
		},
		{
			name: "sticker attachment",
			action: Action{
				Action:         "send_message",
				MessageType:    "group",
				UserID:         "usr_attachment",
				ReplyMessageID: "msg_attachment",
				Attachments: []core.Attachment{
					{Type: core.AttachmentTypeImage, SubType: 1, URL: "/api/sticker/test/image"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.action.withConfiguredGroupMentionScope(scope).withConfiguredGroupReplyScope(scope)
			if !reflect.DeepEqual(got, tt.action) {
				t.Fatalf("表情包不应受群聊 @/引用开关装饰，before=%+v after=%+v", tt.action, got)
			}
		})
	}
}

func TestAstrBotStickerSkipsLegacyGroupDecorations(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "true")
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "true")

	action := Action{
		Action:         "send_message",
		MessageType:    "group",
		UserID:         "usr_legacy_sticker",
		ReplyMessageID: "msg_legacy_sticker",
		Messages: []ActionMessage{
			{Type: "image", URL: "/api/sticker/test/image", IsSticker: true},
		},
	}

	data, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("序列化表情包 Action 失败: %v", err)
	}
	var got Action
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("解析表情包 Action 失败: %v", err)
	}
	if len(got.Messages) != 1 || !got.Messages[0].IsSticker || got.Messages[0].Type != "image" {
		t.Fatalf("legacy 序列化也不应为表情包注入 @/引用，实际=%+v", got.Messages)
	}
}
