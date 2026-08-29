package onebot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/model"
	"context"
	"testing"
)

func TestAdapterID(t *testing.T) {
	adapter := NewAdapter(nil)
	if adapter.ID() != "onebot" {
		t.Errorf("expected adapter ID 'onebot', got %s", adapter.ID())
	}
}

func TestAdapterSendNoConns(t *testing.T) {
	adapter := NewAdapter(nil)
	msg := core.OutgoingMessage{
		TargetID:    "123456",
		Content:     "hello",
		Platform:    "onebot",
		MessageType: "private",
	}

	err := adapter.Send(context.Background(), msg)
	if err == nil {
		t.Fatalf("expected error when sending with no active connections, got nil")
	}
}

func TestToIncomingMessage(t *testing.T) {
	event := model.OneBotEvent{
		MessageID:   12345,
		UserID:      10001,
		GroupID:     20002,
		MessageType: "group",
		Sender: &model.OneBotSender{
			Nickname: "FoxUser",
			Card:     "FoxCard",
		},
		Message: []byte(`[{"type":"text","data":{"text":"hi"}}]`),
	}

	inMsg := ToIncomingMessage(event)
	if inMsg.ID != "12345" {
		t.Errorf("expected ID '12345', got %s", inMsg.ID)
	}
	if inMsg.UserID != "10001" {
		t.Errorf("expected UserID '10001', got %s", inMsg.UserID)
	}
	if inMsg.GroupID != "20002" {
		t.Errorf("expected GroupID '20002', got %s", inMsg.GroupID)
	}
	if inMsg.SenderName != "FoxUser" {
		t.Errorf("expected SenderName 'FoxUser', got %s", inMsg.SenderName)
	}
	if inMsg.SenderCard != "FoxCard" {
		t.Errorf("expected SenderCard 'FoxCard', got %s", inMsg.SenderCard)
	}
	if inMsg.Platform != "onebot" {
		t.Errorf("expected Platform 'onebot', got %s", inMsg.Platform)
	}
	if inMsg.MessageType != "group" {
		t.Errorf("expected MessageType 'group', got %s", inMsg.MessageType)
	}
}

func TestStickerURLsFromSegmentsOnlyReturnsStickerImages(t *testing.T) {
	segments := ParseMessageSegments([]byte(`[
		{"type":"image","data":{"url":"https://example.com/a.png","sub_type":1}},
		{"type":"image","data":{"url":"https://example.com/b.png","sub_type":"1"}},
		{"type":"image","data":{"url":"https://example.com/regular.png","sub_type":0}},
		{"type":"text","data":{"text":"hello"}}
	]`))
	urls := stickerURLsFromSegments(segments)
	if len(urls) != 2 || urls[0] != "https://example.com/a.png" || urls[1] != "https://example.com/b.png" {
		t.Fatalf("sticker URLs = %v, want only a.png and b.png", urls)
	}
}
