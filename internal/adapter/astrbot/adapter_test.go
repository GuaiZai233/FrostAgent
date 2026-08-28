package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"context"
	"testing"
	"time"
)

type recordingStickerStealer struct {
	urls []string
}

func (s *recordingStickerStealer) TrySteal(imageURL string) {
	s.urls = append(s.urls, imageURL)
}

func TestAdapterID(t *testing.T) {
	adapter := NewAdapter(nil)
	if adapter.ID() != "astrbot" {
		t.Errorf("expected adapter ID 'astrbot', got %s", adapter.ID())
	}
}

func TestAdapterSendNoConns(t *testing.T) {
	adapter := NewAdapter(nil)
	msg := core.OutgoingMessage{
		TargetID:    "123456",
		Content:     "hello",
		Platform:    "astrbot",
		MessageType: "private",
	}

	err := adapter.Send(context.Background(), msg)
	if err == nil {
		t.Fatalf("expected error when sending with no active connections, got nil")
	}
}

func TestAdapterTryStealOnlyQQGroupStickers(t *testing.T) {
	tests := []struct {
		name      string
		event     Event
		wantCalls int
	}{
		{
			name: "aiocqhttp group sticker",
			event: Event{
				Platform:    astrBotQQPlatform,
				MessageType: "group",
				Attachments: []core.Attachment{{Type: core.AttachmentTypeImage, URL: "https://example.com/sticker.gif", SubType: 1}},
			},
			wantCalls: 1,
		},
		{
			name: "aiocqhttp private sticker",
			event: Event{
				Platform:    astrBotQQPlatform,
				MessageType: "private",
				Attachments: []core.Attachment{{Type: core.AttachmentTypeImage, URL: "https://example.com/sticker.gif", SubType: 1}},
			},
		},
		{
			name: "non aiocqhttp group sticker",
			event: Event{
				Platform:    "telegram",
				MessageType: "group",
				Attachments: []core.Attachment{{Type: core.AttachmentTypeImage, URL: "https://example.com/sticker.gif", SubType: 1}},
			},
		},
		{
			name: "aiocqhttp group regular image",
			event: Event{
				Platform:    astrBotQQPlatform,
				MessageType: "group",
				Attachments: []core.Attachment{{Type: core.AttachmentTypeImage, URL: "https://example.com/image.png"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stealer := &recordingStickerStealer{}
			adapter := NewAdapter(nil)
			adapter.stealer = stealer

			adapter.trySteal(tt.event)

			if len(stealer.urls) != tt.wantCalls {
				t.Fatalf("TrySteal calls = %d, want %d", len(stealer.urls), tt.wantCalls)
			}
		})
	}
}

func TestSendDirectReplyRejectsInvalidInput(t *testing.T) {
	event := Event{UserID: "usr_123", MessageType: "private"}

	if err := sendDirectReply(event, nil, "hello"); err == nil || err.Error() != "connection is nil" {
		t.Fatalf("expected nil connection error, got %v", err)
	}
	if err := sendDirectReply(event, &wsConn{}, "   "); err == nil || err.Error() != "message content is empty" {
		t.Fatalf("expected empty message error, got %v", err)
	}
}

func TestToIncomingMessage(t *testing.T) {
	now := time.Now().Unix()
	event := Event{
		MessageID:   "msg_999",
		SessionID:   "astrbot:group:grp_100",
		UserID:      "usr_200",
		SenderName:  "FoxUser",
		SenderCard:  "FoxCard",
		GroupID:     "grp_100",
		GroupName:   "FoxGroup",
		Content:     "hello astrbot",
		Platform:    "astrbot",
		MessageType: "group",
		Timestamp:   now,
		Attachments: []core.Attachment{
			{
				Type: core.AttachmentTypeImage,
				URL:  "https://example.com/fox.png",
			},
		},
	}

	inMsg := ToIncomingMessage(event)
	if inMsg.ID != "msg_999" {
		t.Errorf("expected ID 'msg_999', got %s", inMsg.ID)
	}
	if inMsg.SessionID != "astrbot:group:grp_100" {
		t.Errorf("expected SessionID 'astrbot:group:grp_100', got %s", inMsg.SessionID)
	}
	if inMsg.UserID != "usr_200" {
		t.Errorf("expected UserID 'usr_200', got %s", inMsg.UserID)
	}
	if inMsg.GroupID != "grp_100" {
		t.Errorf("expected GroupID 'grp_100', got %s", inMsg.GroupID)
	}
	if inMsg.GroupName != "FoxGroup" {
		t.Errorf("expected GroupName 'FoxGroup', got %s", inMsg.GroupName)
	}
	if inMsg.SenderName != "FoxUser" {
		t.Errorf("expected SenderName 'FoxUser', got %s", inMsg.SenderName)
	}
	if inMsg.SenderCard != "FoxCard" {
		t.Errorf("expected SenderCard 'FoxCard', got %s", inMsg.SenderCard)
	}
	if inMsg.Content != "hello astrbot" {
		t.Errorf("expected Content 'hello astrbot', got %s", inMsg.Content)
	}
	if inMsg.Platform != "astrbot" {
		t.Errorf("expected Platform 'astrbot', got %s", inMsg.Platform)
	}
	if inMsg.MessageType != "group" {
		t.Errorf("expected MessageType 'group', got %s", inMsg.MessageType)
	}
	if len(inMsg.Attachments) != 1 || inMsg.Attachments[0].URL != "https://example.com/fox.png" {
		t.Errorf("expected 1 attachment with URL, got %+v", inMsg.Attachments)
	}
}

func TestAstrBot_FormatGroupRoleMessages(t *testing.T) {
	event := Event{
		MessageID:   "msg_123",
		UserID:      "usr_555",
		SenderName:  "小明",
		GroupID:     "grp_888",
		MessageType: "group",
	}

	userMsg := formatGroupSpeakerMessage(event, "今天天气怎么样？")
	if userMsg != "[user] 小明 (usr_555): 今天天气怎么样？" {
		t.Errorf("unexpected formatted user message: %q", userMsg)
	}

	botMsg := formatGroupAssistantMessage("霜降", "今天天气晴朗，适合外出哦~")
	if botMsg != "[assistant] 霜降: 今天天气晴朗，适合外出哦~" {
		t.Errorf("unexpected formatted assistant message: %q", botMsg)
	}
}

func TestAstrBot_ExtractBotReplyText(t *testing.T) {
	plain := "这是纯文本回复"
	if got := extractBotReplyText(plain); got != "这是纯文本回复" {
		t.Errorf("expected %q, got %q", plain, got)
	}

	toolJSON := `{"messages":[{"type":"plain","text":"第一段回复"},{"type":"plain","text":"第二段回复"}]}`
	if got := extractBotReplyText(toolJSON); got != "第一段回复 第二段回复" {
		t.Errorf("expected combined plain text, got %q", got)
	}

	mediaOnlyJSON := `{"messages":[{"type":"image","url":"https://example.com/image.png"}]}`
	if got := extractBotReplyText(mediaOnlyJSON); got != "" {
		t.Errorf("expected media-only reply to produce no compact text, got %q", got)
	}
}

func TestAstrBot_CaptureGroupCompactMessage(t *testing.T) {
	sessionManager := llm.NewSessionManager()
	engine := &llm.Engine{
		SessionManager: sessionManager,
	}

	event := Event{
		MessageID:   "msg_321",
		UserID:      "usr_777",
		SenderName:  "小红",
		GroupID:     "grp_999",
		Content:     "我们在讨论周日活动",
		Platform:    "astrbot",
		MessageType: "group",
	}

	captureGroupCompactMessage(event, engine)

	sess := sessionManager.GetOrCreate("astrbot:group:grp_999")
	snap := sess.SnapshotGroupContext(10, 1000, "")

	if len(snap.RecentMessages) != 1 {
		t.Fatalf("expected 1 recent message, got %d", len(snap.RecentMessages))
	}
	expected := "[user] 小红 (usr_777): 我们在讨论周日活动"
	if snap.RecentMessages[0] != expected {
		t.Errorf("expected %q, got %q", expected, snap.RecentMessages[0])
	}
}
