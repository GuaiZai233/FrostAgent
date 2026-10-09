package onebot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/model"
	"FrostAgent/internal/security"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

	// 7. Insecure URLs (file://, local path, UNC, ftp) -> returns error
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
			TargetID:    "12345",
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

func TestOneBot_MetadataVetting_NonBlocking(t *testing.T) {
	tmpDir := t.TempDir()
	gm := memory.NewGroupManager(tmpDir, nil)
	secCtrl := security.NewController(tmpDir)
	secCtrl.SetMode(security.ControlModeAggressive)

	var classifierCalled atomic.Int32
	slowClassifier := &mockClassifier{
		fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
			classifierCalled.Add(1)
			// Simulate a slow remote classifier
			select {
			case <-time.After(200 * time.Millisecond):
				return security.ClassificationResult{
					Category:  security.RiskCategoryNone,
					RiskLevel: security.RiskLevelNone,
					Reason:    "benign",
				}, nil
			case <-ctx.Done():
				return security.ClassificationResult{}, ctx.Err()
			}
		},
	}
	secCtrl.SetClassifier(slowClassifier)

	engine := newTestEngine(&mockLLMProvider{})
	engine.Security = secCtrl
	engine.GroupManager = gm

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	// Wait briefly for connection registration
	time.Sleep(50 * time.Millisecond)

	// Send 20 unwoken chatter messages rapidly with identical nickname and card
	unvettedNick := "FoxNick_TestVetting"
	unvettedCard := "FoxCard_TestVetting"
	startTime := time.Now()

	for i := range 20 {
		event := model.OneBotEvent{
			PostType:    "message",
			MessageType: "group",
			MessageID:   int32(2000 + i),
			GroupID:     777888,
			UserID:      888999,
			Sender: &model.OneBotSender{
				UserID:   888999,
				Nickname: unvettedNick,
				Card:     unvettedCard,
			},
			Message: []byte(`[{"type":"text","data":{"text":"unwoken chatter line"}}]`),
		}
		data, mErr := json.Marshal(event)
		if mErr != nil {
			t.Fatalf("marshal event: %v", mErr)
		}
		if wErr := conn.WriteMessage(websocket.TextMessage, data); wErr != nil {
			t.Fatalf("write message: %v", wErr)
		}
	}

	// 1. Non-blocking verification: sending 20 messages with a 200ms classifier took < 500ms
	sendDuration := time.Since(startTime)
	if sendDuration > 500*time.Millisecond {
		t.Errorf("expected non-blocking write to complete quickly, took %v", sendDuration)
	}

	// 2. Unvetted persistence check: immediately after sending, unvetted values must NOT be persisted
	gStore, err := gm.GetGroupStore("777888")
	if err == nil {
		prof, pErr := gStore.GetProfile()
		if pErr == nil {
			mem := prof.GetMember("888999")
			if mem != nil && (mem.Nickname == unvettedNick || mem.Card == unvettedCard) {
				t.Errorf("unvetted nickname/card leaked into persistent profile before vetting completed: %+v", mem)
			}
		}
	}

	// 3. Wait for background vetting to complete
	vetter := secCtrl.GetMetadataVetter()
	deadline := time.Now().Add(2 * time.Second)
	vettedSafe := false
	for time.Now().Before(deadline) {
		safeNick, cachedNick := vetter.Check(unvettedNick)
		safeCard, cachedCard := vetter.Check(unvettedCard)
		if cachedNick && safeNick && cachedCard && safeCard {
			vettedSafe = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !vettedSafe {
		t.Errorf("expected background vetting to finish and record safe cache entries")
	}

	// 4. Bounded evaluation count: across 20 messages, deduplication bounded evaluations to <= 4 (1-2 for nick, 1-2 for card)
	count := classifierCalled.Load()
	if count > 4 {
		t.Errorf("expected deduplicated evaluations (<= 4), got %d", count)
	}

	// 5. Ensure all in-flight background goroutines and disk writes finish before TempDir cleanup on Windows
	for range 50 {
		if vetter.InFlightCount() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
}
