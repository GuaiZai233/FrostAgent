package onebot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/model"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

func TestAdapterSend_OutboundContract(t *testing.T) {
	mux := http.NewServeMux()
	engine := newTestEngine(&mockLLMProvider{})
	adapter := NewAdapter(engine)
	mux.HandleFunc("/ws/onebot", adapter.Handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/onebot"
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

	// 1. Image with SubType: 1 -> produces segment with sub_type: 1 and subType: 1
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "12345",
		MessageType: "group",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeImage, URL: "https://example.com/fox_sticker.png", SubType: 1},
		},
	})
	if err != nil {
		t.Fatalf("send sticker failed: %v", err)
	}

	var action model.OneBotAction
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatalf("read action: %v", err)
	}
	if action.Action != "send_group_msg" {
		t.Fatalf("expected send_group_msg, got %s", action.Action)
	}
	params, ok := action.Params.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any params, got %T", action.Params)
	}
	msgBytes, _ := json.Marshal(params["message"])
	var segs []tools.OneBotSegment
	if err := json.Unmarshal(msgBytes, &segs); err != nil {
		t.Fatalf("unmarshal segs: %v", err)
	}
	if len(segs) != 1 || segs[0].Type != "image" {
		t.Fatalf("expected 1 image segment, got %+v", segs)
	}
	if segs[0].Data["sub_type"] != float64(1) && segs[0].Data["sub_type"] != 1 {
		t.Fatalf("expected sub_type=1, got %v", segs[0].Data["sub_type"])
	}
	if segs[0].Data["subType"] != float64(1) && segs[0].Data["subType"] != 1 {
		t.Fatalf("expected subType=1, got %v", segs[0].Data["subType"])
	}

	// 2. Audio with URL -> record segment
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "12345",
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeAudio, URL: "https://example.com/voice.silk"},
		},
	})
	if err != nil {
		t.Fatalf("send audio failed: %v", err)
	}
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatalf("read action: %v", err)
	}
	params = action.Params.(map[string]any)
	msgBytes, _ = json.Marshal(params["message"])
	if err := json.Unmarshal(msgBytes, &segs); err != nil {
		t.Fatalf("unmarshal segs: %v", err)
	}
	if len(segs) != 1 || segs[0].Type != "record" {
		t.Fatalf("expected 1 record segment, got %+v", segs)
	}

	// 3. Video with URL -> video segment
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "12345",
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeVideo, URL: "https://example.com/video.mp4"},
		},
	})
	if err != nil {
		t.Fatalf("send video failed: %v", err)
	}
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatalf("read action: %v", err)
	}
	params = action.Params.(map[string]any)
	msgBytes, _ = json.Marshal(params["message"])
	if err := json.Unmarshal(msgBytes, &segs); err != nil {
		t.Fatalf("unmarshal segs: %v", err)
	}
	if len(segs) != 1 || segs[0].Type != "video" {
		t.Fatalf("expected 1 video segment, got %+v", segs)
	}

	// 4. Unsupported attachment (file) -> returns error
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "12345",
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeFile, URL: "https://example.com/doc.pdf"},
		},
	})
	if err == nil {
		t.Fatal("expected error for unsupported attachment type 'file', got nil")
	}

	// 5. Empty URL -> returns error
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "12345",
		MessageType: "private",
		Attachments: []core.Attachment{
			{Type: core.AttachmentTypeImage, URL: ""},
		},
	})
	if err == nil {
		t.Fatal("expected error for empty attachment url, got nil")
	}

	// 6. Empty message (no text, no attachments) -> returns error
	err = adapter.Send(ctx, core.OutgoingMessage{
		TargetID:    "12345",
		MessageType: "private",
	})
	if err == nil {
		t.Fatal("expected error for empty message, got nil")
	}
}
