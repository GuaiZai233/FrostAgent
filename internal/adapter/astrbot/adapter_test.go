package astrbot

import (
	"FrostAgent/internal/core"
	"context"
	"testing"
	"time"
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
