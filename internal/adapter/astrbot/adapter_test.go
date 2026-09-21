package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/sticker"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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

func TestAdapterObservesCurrentAndQuotedQQStickersInSession(t *testing.T) {
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	stealer := sticker.NewStealer(store, nil)
	adapter := NewAdapter(nil)
	adapter.stealer = stealer

	currentImage := []byte{0x89, 'P', 'N', 'G', 1}
	quotedImage := []byte("GIF89a quoted")
	event := Event{
		MessageID:   "msg_current",
		SessionID:   "aiocqhttp:private:123",
		Platform:    astrBotQQPlatform,
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeImage, Content: currentImage, SubType: 1},
			{Type: core.AttachmentTypeImage, Content: quotedImage, SubType: 1, MessageID: "msg_quoted"},
			{Type: core.AttachmentTypeImage, Content: []byte("regular")},
		},
	}
	adapter.observeStickers(event)
	loader := func(ctx context.Context, messageID string, stickerIndex int) ([]byte, error) {
		return loadObservedStickerFromEvent(ctx, event, messageID, stickerIndex)
	}

	result, messageID, err := stealer.StealObserved(context.Background(), event.SessionID, "msg_quoted", 0, loader)
	if err != nil {
		t.Fatalf("steal quoted sticker: %v", err)
	}
	if messageID != "msg_quoted" || result.ID != sticker.HashBytes(quotedImage) {
		t.Fatalf("quoted result = %+v message=%q", result, messageID)
	}
	if _, _, err := stealer.StealObserved(context.Background(), "aiocqhttp:private:other", "msg_quoted", 0, loader); !errors.Is(err, sticker.ErrStickerNotInScope) {
		t.Fatalf("cross-session error = %v, want ErrStickerNotInScope", err)
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

func TestAdapterSend_OutboundContract(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, adapter, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	// Wait briefly for connection registration
	for range 20 {
		adapter.mu.RLock()
		n := len(adapter.conns)
		adapter.mu.RUnlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx := context.Background()

	// 1. Sticker image with SubType: 1 -> produces ActionMessage with IsSticker: true, SubType: 1
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "grp_123",
		MessageType: "group",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeImage, URL: "https://example.com/fox_sticker.png", SubType: 1},
		},
	})
	if err != nil {
		t.Fatalf("send sticker failed: %v", err)
	}

	var action Action
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatalf("read action: %v", err)
	}
	if action.Action != "send_message" {
		t.Fatalf("expected send_message, got %s", action.Action)
	}
	if action.GroupID != "grp_123" {
		t.Fatalf("expected group_id grp_123, got %s", action.GroupID)
	}
	if len(action.Messages) != 1 {
		t.Fatalf("expected 1 message component, got %+v", action.Messages)
	}
	if action.Messages[0].Type != "image" {
		t.Fatalf("expected type image, got %s", action.Messages[0].Type)
	}
	if action.Messages[0].URL != "https://example.com/fox_sticker.png" {
		t.Fatalf("expected sticker URL, got %s", action.Messages[0].URL)
	}
	if !action.Messages[0].IsSticker {
		t.Fatalf("expected IsSticker=true, got %v", action.Messages[0].IsSticker)
	}
	if action.Messages[0].SubType != 1 {
		t.Fatalf("expected SubType=1, got %d", action.Messages[0].SubType)
	}

	// 2. Text + regular image -> produces 2 components: plain and image
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "usr_456",
		MessageType: "private",
		Content:     "look at this",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeImage, URL: "https://example.com/normal.png"},
		},
	})
	if err != nil {
		t.Fatalf("send text+image failed: %v", err)
	}
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatalf("read action: %v", err)
	}
	if action.UserID != "usr_456" {
		t.Fatalf("expected user_id usr_456, got %s", action.UserID)
	}
	if len(action.Messages) != 2 {
		t.Fatalf("expected 2 components, got %+v", action.Messages)
	}
	if action.Messages[0].Type != "plain" || action.Messages[0].Text != "look at this" {
		t.Fatalf("expected plain component, got %+v", action.Messages[0])
	}
	if action.Messages[1].Type != "image" || action.Messages[1].URL != "https://example.com/normal.png" || action.Messages[1].IsSticker {
		t.Fatalf("expected regular image component, got %+v", action.Messages[1])
	}

	// 3. Unsupported attachment (audio) -> returns error
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "usr_456",
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeAudio, URL: "https://example.com/audio.mp3"},
		},
	})
	if err == nil {
		t.Fatal("expected error for unsupported audio attachment, got nil")
	}

	// 4. Unsupported attachment (video) -> returns error
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "usr_456",
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeVideo, URL: "https://example.com/video.mp4"},
		},
	})
	if err == nil {
		t.Fatal("expected error for unsupported video attachment, got nil")
	}

	// 5. Unsupported attachment (file) -> returns error
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "usr_456",
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeFile, URL: "https://example.com/file.pdf"},
		},
	})
	if err == nil {
		t.Fatal("expected error for unsupported file attachment, got nil")
	}

	// 6. Empty URL -> returns error
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "usr_456",
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeImage, URL: ""},
		},
	})
	if err == nil {
		t.Fatal("expected error for empty attachment url, got nil")
	}

	// 7. Empty message (no text, no attachments) -> returns error
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "usr_456",
		MessageType: "private",
	})
	if err == nil {
		t.Fatal("expected error for empty message, got nil")
	}

	// 8. Insecure URLs (file://, local path, UNC, ftp) -> returns error
	insecureMediaURLs := []string{
		"file:///etc/passwd",
		"file:///C:/Windows/win.ini",
		"C:\\Windows\\System32\\drivers\\etc\\hosts",
		"C:/Windows/win.ini",
		"/etc/passwd",
		"./local.png",
		"\\\\attacker\\share\\fox.png",
		"//attacker.com/fox.png",
		"ftp://attacker.com/fox.png",
		"http:///no-host",
	}
	for _, rawURL := range insecureMediaURLs {
		err = adapter.Send(ctx, core.OutgoingMessage{
			TargetID:    "usr_456",
			MessageType: "private",
			Attachments: []core.Attachment{
				{Type: core.AttachmentTypeImage, URL: rawURL},
			},
		})
		if err == nil {
			t.Fatalf("expected error for insecure media URL %q, got nil", rawURL)
		}
	}
}
