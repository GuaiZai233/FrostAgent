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

func TestStickerSourcesFromSegmentsOnlyReturnsStickerImages(t *testing.T) {
	segments := ParseMessageSegments([]byte(`[
		{"type":"image","data":{"url":"https://example.com/a.png","sub_type":1}},
		{"type":"image","data":{"url":"https://example.com/b.png","sub_type":"1"}},
		{"type":"image","data":{"file":"base64://c3RpY2tlcg==","sub_type":1}},
		{"type":"image","data":{"url":"https://gxh.vip.qq.com/club/item/parcel/item/ab/abcdef/raw300.gif","emoji_id":"abcdef","emoji_package_id":123}},
		{"type":"mface","data":{"emoji_id":"123456","emoji_package_id":"789","key":"key"}},
		{"type":"mface","data":{"file":"base64://bWZhY2U=","emoji_id":"654321"}},
		{"type":"image","data":{"url":"https://example.com/regular.png","sub_type":0}},
		{"type":"text","data":{"text":"hello"}}
	]`))
	want := []string{
		"https://example.com/a.png",
		"https://example.com/b.png",
		"base64://c3RpY2tlcg==",
		"https://gxh.vip.qq.com/club/item/parcel/item/ab/abcdef/raw300.gif",
		"https://gxh.vip.qq.com/club/item/parcel/item/12/123456/raw300.gif",
		"base64://bWZhY2U=",
	}
	sources := stickerSourcesFromSegments(segments)
	if len(sources) != len(want) {
		t.Fatalf("sticker sources = %v, want %v", sources, want)
	}
	for i := range want {
		if sources[i] != want[i] {
			t.Fatalf("sticker sources[%d] = %q, want %q", i, sources[i], want[i])
		}
	}
}
